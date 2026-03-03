package ledger

import (
	"fmt"
	"sync"
	"time"

	"github.com/luxfi/treasury/pkg/types"
)

// Ledger provides double-entry bookkeeping for treasury operations.
// Every payment creates balanced debit/credit entries.
type Ledger struct {
	mu           sync.RWMutex
	accounts     map[string]*types.LedgerAccount
	entries      []types.LedgerEntry
	nextEntryID  int
	nextTxID     int
}

func New() *Ledger {
	return &Ledger{
		accounts: make(map[string]*types.LedgerAccount),
	}
}

// CreateAccount creates a ledger account.
func (l *Ledger) CreateAccount(name, acctType, currency string) (*types.LedgerAccount, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	id := fmt.Sprintf("la_%d", len(l.accounts)+1)
	acct := &types.LedgerAccount{
		ID:        id,
		Name:      name,
		Type:      acctType,
		Currency:  currency,
		Balance:   "0",
		Status:    "active",
		CreatedAt: time.Now().UTC(),
	}
	l.accounts[id] = acct
	return acct, nil
}

// GetAccount returns a ledger account.
func (l *Ledger) GetAccount(id string) (*types.LedgerAccount, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	acct, ok := l.accounts[id]
	if !ok {
		return nil, fmt.Errorf("ledger account %s not found", id)
	}
	return acct, nil
}

// ListAccounts returns all ledger accounts.
func (l *Ledger) ListAccounts() []*types.LedgerAccount {
	l.mu.RLock()
	defer l.mu.RUnlock()
	result := make([]*types.LedgerAccount, 0, len(l.accounts))
	for _, a := range l.accounts {
		result = append(result, a)
	}
	return result
}

// PostTransaction records a balanced double-entry transaction.
// Debits and credits must balance (same total amount).
func (l *Ledger) PostTransaction(description string, entries []TransactionEntry) (string, error) {
	if len(entries) < 2 {
		return "", fmt.Errorf("transaction requires at least 2 entries")
	}

	// Validate balance
	var totalDebit, totalCredit float64
	for _, e := range entries {
		if e.Direction == "debit" {
			totalDebit += e.AmountFloat
		} else if e.Direction == "credit" {
			totalCredit += e.AmountFloat
		} else {
			return "", fmt.Errorf("invalid direction %q, must be debit or credit", e.Direction)
		}
	}
	if totalDebit != totalCredit {
		return "", fmt.Errorf("transaction does not balance: debit=%.2f credit=%.2f", totalDebit, totalCredit)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.nextTxID++
	txID := fmt.Sprintf("tx_%d", l.nextTxID)
	now := time.Now().UTC()

	for _, e := range entries {
		l.nextEntryID++
		entry := types.LedgerEntry{
			ID:            fmt.Sprintf("le_%d", l.nextEntryID),
			LedgerID:      "default",
			TransactionID: txID,
			AccountID:     e.AccountID,
			Amount:        fmt.Sprintf("%.2f", e.AmountFloat),
			Currency:      e.Currency,
			Direction:     e.Direction,
			Status:        "posted",
			Description:   description,
			EffectiveDate: now.Format("2006-01-02"),
			PostedAt:      now,
		}
		l.entries = append(l.entries, entry)
	}

	return txID, nil
}

// TransactionEntry is a single entry in a double-entry transaction.
type TransactionEntry struct {
	AccountID   string
	AmountFloat float64
	Currency    string
	Direction   string // debit or credit
}

// GetEntries returns all entries for an account.
func (l *Ledger) GetEntries(accountID string) []types.LedgerEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var result []types.LedgerEntry
	for _, e := range l.entries {
		if e.AccountID == accountID {
			result = append(result, e)
		}
	}
	return result
}

// GetTransaction returns all entries for a transaction ID.
func (l *Ledger) GetTransaction(txID string) []types.LedgerEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var result []types.LedgerEntry
	for _, e := range l.entries {
		if e.TransactionID == txID {
			result = append(result, e)
		}
	}
	return result
}
