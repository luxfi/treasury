package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Service provides double-entry ledger operations.
type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) Migrate(ctx context.Context) error {
	return s.store.Migrate(ctx)
}

func (s *Service) Close() error {
	return s.store.Close()
}

// CreateAccount creates a named ledger account.
func (s *Service) CreateAccount(ctx context.Context, ledger, address, acctType string, metadata map[string]string) (*Account, error) {
	if err := validateAddress(address); err != nil {
		return nil, err
	}
	if !isValidAccountType(acctType) {
		return nil, fmt.Errorf("invalid account type %q", acctType)
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	now := time.Now().UTC()
	acct := &Account{
		Ledger:    ledger,
		Address:   address,
		Type:      acctType,
		Metadata:  metadata,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.CreateAccount(ctx, ledger, acct); err != nil {
		return nil, err
	}
	return acct, nil
}

// GetAccount returns an account by address.
func (s *Service) GetAccount(ctx context.Context, ledger, address string) (*Account, error) {
	return s.store.GetAccount(ctx, ledger, address)
}

// ListAccounts returns all accounts in a ledger.
func (s *Service) ListAccounts(ctx context.Context, ledger string) ([]*Account, error) {
	return s.store.ListAccounts(ctx, ledger)
}

// PostTransaction creates an atomic multi-posting transaction.
// All postings must have positive amounts and reference valid assets.
// Accounts are auto-created if they don't exist.
func (s *Service) PostTransaction(ctx context.Context, ledger string, req CreateTransactionRequest) (*Transaction, error) {
	if len(req.Postings) == 0 {
		return nil, fmt.Errorf("transaction requires at least one posting")
	}

	// Idempotency: if reference exists, return existing transaction
	if req.Reference != "" {
		existing, err := s.store.GetTransactionByReference(ctx, ledger, req.Reference)
		if err == nil && existing != nil {
			return existing, nil
		}
	}

	// Validate postings
	for i, p := range req.Postings {
		if err := validateAddress(p.Source); err != nil {
			return nil, fmt.Errorf("posting[%d] source: %w", i, err)
		}
		if err := validateAddress(p.Destination); err != nil {
			return nil, fmt.Errorf("posting[%d] destination: %w", i, err)
		}
		if p.Source == p.Destination {
			return nil, fmt.Errorf("posting[%d]: source and destination cannot be the same", i)
		}
		if err := validateAsset(p.Asset); err != nil {
			return nil, fmt.Errorf("posting[%d] asset: %w", i, err)
		}
		amt, ok := new(big.Int).SetString(p.Amount, 10)
		if !ok || amt.Sign() <= 0 {
			return nil, fmt.Errorf("posting[%d]: amount must be a positive integer, got %q", i, p.Amount)
		}
	}

	ts := time.Now().UTC()
	if req.Timestamp != nil {
		ts = req.Timestamp.UTC()
	}

	tx := &Transaction{
		Ledger:    ledger,
		Reference: req.Reference,
		Postings:  req.Postings,
		Metadata:  req.Metadata,
		Timestamp: ts,
	}
	if tx.Metadata == nil {
		tx.Metadata = map[string]string{}
	}

	// Build moves: for each posting, create source (debit) and destination (credit) moves.
	// We need current volumes to compute post-commit volumes.
	var moves []Move
	// Track running volume adjustments within this transaction for accounts touched multiple times.
	volAdj := map[string]*[2]*big.Int{} // key="address:asset" → [inputs_adj, outputs_adj]

	for i, p := range req.Postings {
		amt, _ := new(big.Int).SetString(p.Amount, 10)

		// Auto-create accounts if needed
		for _, addr := range []string{p.Source, p.Destination} {
			if _, err := s.store.GetAccount(ctx, ledger, addr); err != nil {
				now := time.Now().UTC()
				_ = s.store.CreateAccount(ctx, ledger, &Account{
					Ledger:    ledger,
					Address:   addr,
					Type:      "unknown",
					Metadata:  map[string]string{},
					CreatedAt: now,
					UpdatedAt: now,
				})
			}
		}

		// Source move (debit/outflow)
		srcKey := p.Source + ":" + p.Asset
		srcInputs, srcOutputs := s.getVolumes(ctx, ledger, p.Source, p.Asset)
		if adj, ok := volAdj[srcKey]; ok {
			srcInputs.Add(srcInputs, adj[0])
			srcOutputs.Add(srcOutputs, adj[1])
		}
		newSrcOutputs := new(big.Int).Add(srcOutputs, amt)
		moves = append(moves, Move{
			Ledger:            ledger,
			AccountAddress:    p.Source,
			Asset:             p.Asset,
			Amount:            amt.String(),
			IsSource:          true,
			PostCommitInputs:  srcInputs.String(),
			PostCommitOutputs: newSrcOutputs.String(),
			CreatedAt:         ts,
		})
		if _, ok := volAdj[srcKey]; !ok {
			volAdj[srcKey] = &[2]*big.Int{new(big.Int), new(big.Int)}
		}
		volAdj[srcKey][1].Add(volAdj[srcKey][1], amt)

		// Destination move (credit/inflow)
		dstKey := p.Destination + ":" + p.Asset
		dstInputs, dstOutputs := s.getVolumes(ctx, ledger, p.Destination, p.Asset)
		if adj, ok := volAdj[dstKey]; ok {
			dstInputs.Add(dstInputs, adj[0])
			dstOutputs.Add(dstOutputs, adj[1])
		}
		newDstInputs := new(big.Int).Add(dstInputs, amt)
		moves = append(moves, Move{
			Ledger:            ledger,
			AccountAddress:    p.Destination,
			Asset:             p.Asset,
			Amount:            amt.String(),
			IsSource:          false,
			PostCommitInputs:  newDstInputs.String(),
			PostCommitOutputs: dstOutputs.String(),
			CreatedAt:         ts,
		})
		if _, ok := volAdj[dstKey]; !ok {
			volAdj[dstKey] = &[2]*big.Int{new(big.Int), new(big.Int)}
		}
		volAdj[dstKey][0].Add(volAdj[dstKey][0], amt)

		_ = i
	}

	if err := s.store.InsertTransaction(ctx, ledger, tx, moves); err != nil {
		return nil, err
	}

	// Audit log
	logData := map[string]any{
		"transaction_id": tx.ID,
		"reference":      tx.Reference,
		"postings_count": len(tx.Postings),
	}
	logJSON, _ := json.Marshal(logData)
	hash := sha256.Sum256(logJSON)
	_ = s.store.InsertLog(ctx, &LogEntry{
		Ledger:         ledger,
		Type:           LogNewTransaction,
		Data:           logData,
		IdempotencyKey: req.Reference,
		Hash:           hash[:],
		CreatedAt:      ts,
	})

	return tx, nil
}

// GetTransaction returns a transaction by ID with its postings.
func (s *Service) GetTransaction(ctx context.Context, ledger string, id int64) (*Transaction, error) {
	return s.store.GetTransaction(ctx, ledger, id)
}

// ListTransactions returns transactions in a ledger.
func (s *Service) ListTransactions(ctx context.Context, ledger string, limit, offset int) ([]*Transaction, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.store.ListTransactions(ctx, ledger, limit, offset)
}

// RevertTransaction creates a new transaction that reverses the original.
func (s *Service) RevertTransaction(ctx context.Context, ledger string, id int64) (*Transaction, error) {
	orig, err := s.store.GetTransaction(ctx, ledger, id)
	if err != nil {
		return nil, err
	}
	if orig.RevertedAt != nil {
		return nil, fmt.Errorf("transaction %d already reverted", id)
	}

	// Build reverse postings (swap source↔destination)
	var reversePostings []Posting
	for _, p := range orig.Postings {
		reversePostings = append(reversePostings, Posting{
			Source:      p.Destination,
			Destination: p.Source,
			Asset:       p.Asset,
			Amount:      p.Amount,
		})
	}

	revertRef := fmt.Sprintf("revert:%d", id)
	revertTx, err := s.PostTransaction(ctx, ledger, CreateTransactionRequest{
		Reference: revertRef,
		Postings:  reversePostings,
		Metadata: map[string]string{
			"revert_of": fmt.Sprintf("%d", id),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create reversal: %w", err)
	}

	if err := s.store.RevertTransaction(ctx, ledger, id); err != nil {
		return nil, err
	}

	return revertTx, nil
}

// GetAccountBalance returns the balance for an account+asset pair.
func (s *Service) GetAccountBalance(ctx context.Context, ledger, address, asset string) (*AccountBalance, error) {
	inputs, outputs, err := s.store.GetAccountBalance(ctx, ledger, address, asset)
	if err != nil {
		return nil, err
	}
	in, _ := new(big.Int).SetString(inputs, 10)
	out, _ := new(big.Int).SetString(outputs, 10)
	if in == nil {
		in = new(big.Int)
	}
	if out == nil {
		out = new(big.Int)
	}
	balance := new(big.Int).Sub(in, out)
	return &AccountBalance{
		Account: address,
		Asset:   asset,
		Balance: balance.String(),
		Volumes: Volumes{Inputs: in.String(), Outputs: out.String()},
	}, nil
}

// GetAccountBalances returns all balances for an account across all assets.
func (s *Service) GetAccountBalances(ctx context.Context, ledger, address string) ([]AccountBalance, error) {
	return s.store.GetAccountBalances(ctx, ledger, address)
}

func (s *Service) getVolumes(ctx context.Context, ledger, address, asset string) (*big.Int, *big.Int) {
	inputs, outputs, err := s.store.GetAccountBalance(ctx, ledger, address, asset)
	if err != nil {
		return new(big.Int), new(big.Int)
	}
	in, _ := new(big.Int).SetString(inputs, 10)
	out, _ := new(big.Int).SetString(outputs, 10)
	if in == nil {
		in = new(big.Int)
	}
	if out == nil {
		out = new(big.Int)
	}
	return in, out
}

// Validation helpers

func validateAddress(addr string) error {
	if addr == "" {
		return fmt.Errorf("address cannot be empty")
	}
	for _, seg := range strings.Split(addr, ":") {
		if seg == "" {
			return fmt.Errorf("address %q has empty segment", addr)
		}
	}
	return nil
}

func validateAsset(asset string) error {
	if asset == "" {
		return fmt.Errorf("asset cannot be empty")
	}
	// Allow simple names (USD, BTC) or with decimals (USD/2, BTC/8)
	return nil
}

func isValidAccountType(t string) bool {
	switch t {
	case "asset", "liability", "equity", "revenue", "expense", "unknown":
		return true
	}
	return false
}
