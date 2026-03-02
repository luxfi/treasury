package wallet

import "time"

// Wallet is a multi-currency virtual account backed by the ledger.
// Each wallet maps to a ledger account at "wallets:{wallet_id}:main".
type Wallet struct {
	ID        string            `json:"id"`
	Ledger    string            `json:"ledger"`
	Name      string            `json:"name"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// Hold is a reservation of funds on a wallet.
// Held funds are moved to "wallets:{wallet_id}:hold:{hold_id}" in the ledger.
type Hold struct {
	ID          string    `json:"id"`
	WalletID    string    `json:"wallet_id"`
	Asset       string    `json:"asset"`
	Amount      string    `json:"amount"`
	Description string    `json:"description,omitempty"`
	Destination string    `json:"destination,omitempty"` // target account when confirmed
	Remaining   string    `json:"remaining"`             // amount not yet confirmed/voided
	Status      string    `json:"status"`                // pending, confirmed, voided, partial
	CreatedAt   time.Time `json:"created_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
}

// WalletBalance is the balance of a wallet for a specific asset.
type WalletBalance struct {
	Asset     string `json:"asset"`
	Available string `json:"available"` // main balance (excluding holds)
	Held      string `json:"held"`      // total held
	Total     string `json:"total"`     // available + held
}

// CreditRequest funds a wallet.
type CreditRequest struct {
	Asset    string            `json:"asset"`
	Amount   string            `json:"amount"`
	Source   string            `json:"source,omitempty"` // source ledger account (default: "world")
	Metadata map[string]string `json:"metadata,omitempty"`
}

// DebitRequest withdraws from a wallet.
type DebitRequest struct {
	Asset       string            `json:"asset"`
	Amount      string            `json:"amount"`
	Destination string            `json:"destination,omitempty"` // dest ledger account (default: "world")
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// CreateHoldRequest reserves funds.
type CreateHoldRequest struct {
	Asset       string `json:"asset"`
	Amount      string `json:"amount"`
	Description string `json:"description,omitempty"`
	Destination string `json:"destination,omitempty"` // where funds go on confirm
}

// ConfirmHoldRequest settles a hold (full or partial).
type ConfirmHoldRequest struct {
	Amount string `json:"amount,omitempty"` // partial confirm; empty = full remaining
}
