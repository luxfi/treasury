package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"
	"time"
)

// MemStore implements Store using in-memory maps. Used for testing.
type MemStore struct {
	mu           sync.RWMutex
	accounts     map[string]*Account // key: "ledger:address"
	transactions map[string][]*Transaction
	postings     map[int64][]Posting // key: transaction ID
	moves        []Move
	logs         []*LogEntry
	nextID       int64
}

func NewMemStore() *MemStore {
	return &MemStore{
		accounts:     make(map[string]*Account),
		transactions: make(map[string][]*Transaction),
		postings:     make(map[int64][]Posting),
	}
}

func (s *MemStore) Migrate(_ context.Context) error { return nil }
func (s *MemStore) Close() error                    { return nil }

func (s *MemStore) nextSeq() int64 {
	s.nextID++
	return s.nextID
}

func (s *MemStore) CreateAccount(_ context.Context, ledger string, acct *Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := ledger + ":" + acct.Address
	if existing, ok := s.accounts[key]; ok {
		acct.ID = existing.ID
		return nil
	}
	acct.ID = s.nextSeq()
	acct.Ledger = ledger
	cp := *acct
	s.accounts[key] = &cp
	return nil
}

func (s *MemStore) GetAccount(_ context.Context, ledger, address string) (*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := ledger + ":" + address
	acct, ok := s.accounts[key]
	if !ok {
		return nil, fmt.Errorf("account %q not found in ledger %q", address, ledger)
	}
	cp := *acct
	return &cp, nil
}

func (s *MemStore) ListAccounts(_ context.Context, ledger string) ([]*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Account
	for _, a := range s.accounts {
		if a.Ledger == ledger {
			cp := *a
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (s *MemStore) UpdateAccountMetadata(_ context.Context, ledger, address string, metadata map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := ledger + ":" + address
	acct, ok := s.accounts[key]
	if !ok {
		return fmt.Errorf("account %q not found", address)
	}
	if acct.Metadata == nil {
		acct.Metadata = make(map[string]string)
	}
	for k, v := range metadata {
		acct.Metadata[k] = v
	}
	acct.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *MemStore) InsertTransaction(_ context.Context, ledger string, tx *Transaction, moves []Move) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx.ID = s.nextSeq()
	tx.Ledger = ledger

	for i := range tx.Postings {
		tx.Postings[i].ID = s.nextSeq()
		tx.Postings[i].TransactionID = tx.ID
	}

	for i := range moves {
		moves[i].ID = s.nextSeq()
		moves[i].TransactionID = tx.ID
	}

	// Deep copy
	txCopy := *tx
	postCopy := make([]Posting, len(tx.Postings))
	copy(postCopy, tx.Postings)
	txCopy.Postings = postCopy

	s.transactions[ledger] = append(s.transactions[ledger], &txCopy)
	s.postings[tx.ID] = postCopy

	movesCopy := make([]Move, len(moves))
	copy(movesCopy, moves)
	s.moves = append(s.moves, movesCopy...)

	// Assign posting IDs to moves
	moveIdx := 0
	for _, p := range tx.Postings {
		for j := 0; j < 2 && moveIdx < len(moves); j++ {
			moves[moveIdx].PostingID = p.ID
			moveIdx++
		}
	}

	return nil
}

func (s *MemStore) GetTransaction(_ context.Context, ledger string, id int64) (*Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, tx := range s.transactions[ledger] {
		if tx.ID == id {
			cp := *tx
			cp.Postings = make([]Posting, len(tx.Postings))
			copy(cp.Postings, tx.Postings)
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("transaction %d not found in ledger %q", id, ledger)
}

func (s *MemStore) GetTransactionByReference(_ context.Context, ledger, reference string) (*Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, tx := range s.transactions[ledger] {
		if tx.Reference == reference {
			cp := *tx
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("transaction with reference %q not found", reference)
}

func (s *MemStore) ListTransactions(_ context.Context, ledger string, limit, offset int) ([]*Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := s.transactions[ledger]
	// Reverse order (newest first)
	var reversed []*Transaction
	for i := len(all) - 1; i >= 0; i-- {
		reversed = append(reversed, all[i])
	}
	if offset >= len(reversed) {
		return nil, nil
	}
	end := offset + limit
	if end > len(reversed) {
		end = len(reversed)
	}
	return reversed[offset:end], nil
}

func (s *MemStore) RevertTransaction(_ context.Context, ledger string, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, tx := range s.transactions[ledger] {
		if tx.ID == id {
			if tx.RevertedAt != nil {
				return fmt.Errorf("transaction %d already reverted", id)
			}
			now := time.Now().UTC()
			tx.RevertedAt = &now
			return nil
		}
	}
	return fmt.Errorf("transaction %d not found", id)
}

func (s *MemStore) GetAccountBalance(_ context.Context, ledger, address, asset string) (string, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Find the latest move for this account+asset
	var latest *Move
	for i := len(s.moves) - 1; i >= 0; i-- {
		m := &s.moves[i]
		if m.Ledger == ledger && m.AccountAddress == address && m.Asset == asset {
			latest = m
			break
		}
	}
	if latest == nil {
		return "0", "0", nil
	}
	return latest.PostCommitInputs, latest.PostCommitOutputs, nil
}

func (s *MemStore) GetAccountBalances(_ context.Context, ledger, address string) ([]AccountBalance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Find latest move per asset
	latestByAsset := map[string]*Move{}
	for i := range s.moves {
		m := &s.moves[i]
		if m.Ledger == ledger && m.AccountAddress == address {
			latestByAsset[m.Asset] = m
		}
	}

	var result []AccountBalance
	for asset, m := range latestByAsset {
		in, _ := new(big.Int).SetString(m.PostCommitInputs, 10)
		out, _ := new(big.Int).SetString(m.PostCommitOutputs, 10)
		if in == nil {
			in = new(big.Int)
		}
		if out == nil {
			out = new(big.Int)
		}
		bal := new(big.Int).Sub(in, out)
		result = append(result, AccountBalance{
			Account: address,
			Asset:   asset,
			Balance: bal.String(),
			Volumes: Volumes{Inputs: in.String(), Outputs: out.String()},
		})
	}
	return result, nil
}

func (s *MemStore) InsertLog(_ context.Context, log *LogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	log.ID = s.nextSeq()
	cp := *log
	// Deep copy data
	data, _ := json.Marshal(log.Data)
	_ = json.Unmarshal(data, &cp.Data)
	s.logs = append(s.logs, &cp)
	return nil
}

func (s *MemStore) ListLogs(_ context.Context, ledger string, limit, offset int) ([]*LogEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var filtered []*LogEntry
	for i := len(s.logs) - 1; i >= 0; i-- {
		if s.logs[i].Ledger == ledger {
			filtered = append(filtered, s.logs[i])
		}
	}
	if offset >= len(filtered) {
		return nil, nil
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[offset:end], nil
}
