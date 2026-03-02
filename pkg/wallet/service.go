package wallet

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/luxfi/treasury/pkg/ledger"
)

// Service manages virtual wallets backed by the ledger.
type Service struct {
	ledger  *ledger.Service
	mu      sync.Mutex
	wallets map[string]*Wallet // key: walletID
	holds   map[string]*Hold   // key: holdID
	nextID  int
}

func NewService(ledgerSvc *ledger.Service) *Service {
	return &Service{
		ledger:  ledgerSvc,
		wallets: make(map[string]*Wallet),
		holds:   make(map[string]*Hold),
	}
}

func (s *Service) genID(prefix string) string {
	s.nextID++
	return fmt.Sprintf("%s_%d", prefix, s.nextID)
}

// CreateWallet creates a new virtual wallet.
func (s *Service) CreateWallet(ctx context.Context, ledgerName, name string, metadata map[string]string) (*Wallet, error) {
	s.mu.Lock()
	id := s.genID("wal")
	s.mu.Unlock()

	if metadata == nil {
		metadata = map[string]string{}
	}

	// Create the main ledger account
	mainAddr := fmt.Sprintf("wallets:%s:main", id)
	_, err := s.ledger.CreateAccount(ctx, ledgerName, mainAddr, "asset", metadata)
	if err != nil {
		return nil, fmt.Errorf("create wallet account: %w", err)
	}

	w := &Wallet{
		ID:        id,
		Ledger:    ledgerName,
		Name:      name,
		Metadata:  metadata,
		CreatedAt: time.Now().UTC(),
	}

	s.mu.Lock()
	s.wallets[id] = w
	s.mu.Unlock()

	return w, nil
}

// GetWallet returns a wallet by ID.
func (s *Service) GetWallet(_ context.Context, walletID string) (*Wallet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.wallets[walletID]
	if !ok {
		return nil, fmt.Errorf("wallet %q not found", walletID)
	}
	return w, nil
}

// Credit funds a wallet from a source account.
func (s *Service) Credit(ctx context.Context, walletID string, req CreditRequest) error {
	w, err := s.GetWallet(ctx, walletID)
	if err != nil {
		return err
	}
	source := req.Source
	if source == "" {
		source = "world"
	}
	dest := fmt.Sprintf("wallets:%s:main", walletID)
	_, err = s.ledger.PostTransaction(ctx, w.Ledger, ledger.CreateTransactionRequest{
		Postings: []ledger.Posting{
			{Source: source, Destination: dest, Asset: req.Asset, Amount: req.Amount},
		},
		Metadata: req.Metadata,
	})
	return err
}

// Debit withdraws from a wallet to a destination account.
func (s *Service) Debit(ctx context.Context, walletID string, req DebitRequest) error {
	w, err := s.GetWallet(ctx, walletID)
	if err != nil {
		return err
	}
	dest := req.Destination
	if dest == "" {
		dest = "world"
	}
	source := fmt.Sprintf("wallets:%s:main", walletID)
	_, err = s.ledger.PostTransaction(ctx, w.Ledger, ledger.CreateTransactionRequest{
		Postings: []ledger.Posting{
			{Source: source, Destination: dest, Asset: req.Asset, Amount: req.Amount},
		},
		Metadata: req.Metadata,
	})
	return err
}

// GetBalances returns all balances for a wallet.
func (s *Service) GetBalances(ctx context.Context, walletID string) ([]WalletBalance, error) {
	w, err := s.GetWallet(ctx, walletID)
	if err != nil {
		return nil, err
	}
	mainAddr := fmt.Sprintf("wallets:%s:main", walletID)
	mainBalances, err := s.ledger.GetAccountBalances(ctx, w.Ledger, mainAddr)
	if err != nil {
		return nil, err
	}

	// Aggregate holds per asset
	holdTotals := map[string]*big.Int{}
	s.mu.Lock()
	for _, h := range s.holds {
		if h.WalletID == walletID && (h.Status == "pending" || h.Status == "partial") {
			rem, _ := new(big.Int).SetString(h.Remaining, 10)
			if rem == nil {
				continue
			}
			if _, ok := holdTotals[h.Asset]; !ok {
				holdTotals[h.Asset] = new(big.Int)
			}
			holdTotals[h.Asset].Add(holdTotals[h.Asset], rem)
		}
	}
	s.mu.Unlock()

	var result []WalletBalance
	for _, mb := range mainBalances {
		avail, _ := new(big.Int).SetString(mb.Balance, 10)
		if avail == nil {
			avail = new(big.Int)
		}
		held := holdTotals[mb.Asset]
		if held == nil {
			held = new(big.Int)
		}
		total := new(big.Int).Add(avail, held)
		result = append(result, WalletBalance{
			Asset:     mb.Asset,
			Available: avail.String(),
			Held:      held.String(),
			Total:     total.String(),
		})
	}
	return result, nil
}

// CreateHold reserves funds by moving them from main to a hold sub-account.
func (s *Service) CreateHold(ctx context.Context, walletID string, req CreateHoldRequest) (*Hold, error) {
	w, err := s.GetWallet(ctx, walletID)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	holdID := s.genID("hold")
	s.mu.Unlock()

	mainAddr := fmt.Sprintf("wallets:%s:main", walletID)
	holdAddr := fmt.Sprintf("wallets:%s:hold:%s", walletID, holdID)

	// Create hold sub-account
	_, err = s.ledger.CreateAccount(ctx, w.Ledger, holdAddr, "asset", nil)
	if err != nil {
		return nil, err
	}

	// Move funds from main to hold
	_, err = s.ledger.PostTransaction(ctx, w.Ledger, ledger.CreateTransactionRequest{
		Reference: fmt.Sprintf("hold:%s", holdID),
		Postings: []ledger.Posting{
			{Source: mainAddr, Destination: holdAddr, Asset: req.Asset, Amount: req.Amount},
		},
		Metadata: map[string]string{
			"hold_id": holdID,
			"type":    "hold",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create hold tx: %w", err)
	}

	h := &Hold{
		ID:          holdID,
		WalletID:    walletID,
		Asset:       req.Asset,
		Amount:      req.Amount,
		Description: req.Description,
		Destination: req.Destination,
		Remaining:   req.Amount,
		Status:      "pending",
		CreatedAt:   time.Now().UTC(),
	}

	s.mu.Lock()
	s.holds[holdID] = h
	s.mu.Unlock()

	return h, nil
}

// ConfirmHold settles a hold (fully or partially).
// Funds move from the hold sub-account to the destination.
func (s *Service) ConfirmHold(ctx context.Context, holdID string, req ConfirmHoldRequest) (*Hold, error) {
	s.mu.Lock()
	h, ok := s.holds[holdID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("hold %q not found", holdID)
	}
	if h.Status != "pending" && h.Status != "partial" {
		s.mu.Unlock()
		return nil, fmt.Errorf("hold %q is %s, cannot confirm", holdID, h.Status)
	}
	s.mu.Unlock()

	w, err := s.GetWallet(ctx, h.WalletID)
	if err != nil {
		return nil, err
	}

	remaining, _ := new(big.Int).SetString(h.Remaining, 10)
	confirmAmt := new(big.Int).Set(remaining) // default: full remaining
	if req.Amount != "" {
		amt, ok := new(big.Int).SetString(req.Amount, 10)
		if !ok || amt.Sign() <= 0 {
			return nil, fmt.Errorf("invalid confirm amount %q", req.Amount)
		}
		if amt.Cmp(remaining) > 0 {
			return nil, fmt.Errorf("confirm amount %s exceeds remaining %s", amt, remaining)
		}
		confirmAmt = amt
	}

	holdAddr := fmt.Sprintf("wallets:%s:hold:%s", h.WalletID, holdID)
	dest := h.Destination
	if dest == "" {
		dest = "world"
	}

	_, err = s.ledger.PostTransaction(ctx, w.Ledger, ledger.CreateTransactionRequest{
		Reference: fmt.Sprintf("confirm:%s:%s", holdID, confirmAmt.String()),
		Postings: []ledger.Posting{
			{Source: holdAddr, Destination: dest, Asset: h.Asset, Amount: confirmAmt.String()},
		},
		Metadata: map[string]string{
			"hold_id": holdID,
			"type":    "confirm",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("confirm hold tx: %w", err)
	}

	s.mu.Lock()
	newRemaining := new(big.Int).Sub(remaining, confirmAmt)
	h.Remaining = newRemaining.String()
	if newRemaining.Sign() == 0 {
		h.Status = "confirmed"
		now := time.Now().UTC()
		h.ClosedAt = &now
	} else {
		h.Status = "partial"
	}
	s.mu.Unlock()

	return h, nil
}

// VoidHold cancels a hold, returning funds to the wallet's main account.
func (s *Service) VoidHold(ctx context.Context, holdID string) (*Hold, error) {
	s.mu.Lock()
	h, ok := s.holds[holdID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("hold %q not found", holdID)
	}
	if h.Status != "pending" && h.Status != "partial" {
		s.mu.Unlock()
		return nil, fmt.Errorf("hold %q is %s, cannot void", holdID, h.Status)
	}
	s.mu.Unlock()

	w, err := s.GetWallet(ctx, h.WalletID)
	if err != nil {
		return nil, err
	}

	remaining, _ := new(big.Int).SetString(h.Remaining, 10)
	if remaining.Sign() <= 0 {
		return nil, fmt.Errorf("hold %q has no remaining funds", holdID)
	}

	holdAddr := fmt.Sprintf("wallets:%s:hold:%s", h.WalletID, holdID)
	mainAddr := fmt.Sprintf("wallets:%s:main", h.WalletID)

	_, err = s.ledger.PostTransaction(ctx, w.Ledger, ledger.CreateTransactionRequest{
		Reference: fmt.Sprintf("void:%s", holdID),
		Postings: []ledger.Posting{
			{Source: holdAddr, Destination: mainAddr, Asset: h.Asset, Amount: remaining.String()},
		},
		Metadata: map[string]string{
			"hold_id": holdID,
			"type":    "void",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("void hold tx: %w", err)
	}

	s.mu.Lock()
	h.Remaining = "0"
	h.Status = "voided"
	now := time.Now().UTC()
	h.ClosedAt = &now
	s.mu.Unlock()

	return h, nil
}

// GetHold returns a hold by ID.
func (s *Service) GetHold(_ context.Context, holdID string) (*Hold, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.holds[holdID]
	if !ok {
		return nil, fmt.Errorf("hold %q not found", holdID)
	}
	return h, nil
}
