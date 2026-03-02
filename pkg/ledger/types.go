package ledger

import "time"

// Account is an address-based ledger account.
// Addresses use colon-separated segments (e.g., "users:alice:main", "platform:fees").
type Account struct {
	ID        int64             `json:"id"`
	Ledger    string            `json:"ledger"`
	Address   string            `json:"address"`
	Type      string            `json:"type"` // asset, liability, equity, revenue, expense
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// Transaction is an atomic, multi-posting double-entry transaction.
type Transaction struct {
	ID         int64             `json:"id"`
	Ledger     string            `json:"ledger"`
	Reference  string            `json:"reference,omitempty"` // idempotency key
	Postings   []Posting         `json:"postings"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Timestamp  time.Time         `json:"timestamp"`
	RevertedAt *time.Time        `json:"reverted_at,omitempty"`
}

// Posting is a single source→destination movement within a transaction.
// Asset format: "USD/2" (currency/decimals), "BTC/8", "LQDTY/18".
type Posting struct {
	ID            int64  `json:"id,omitempty"`
	TransactionID int64  `json:"transaction_id,omitempty"`
	Source        string `json:"source"`      // source account address
	Destination   string `json:"destination"` // destination account address
	Asset         string `json:"asset"`       // e.g., "USD/2"
	Amount        string `json:"amount"`      // arbitrary precision decimal string
}

// Volumes tracks total inputs (credits) and outputs (debits) for an account+asset.
type Volumes struct {
	Inputs  string `json:"inputs"`
	Outputs string `json:"outputs"`
}

// AccountBalance is the computed balance for an account+asset pair.
type AccountBalance struct {
	Account string  `json:"account"`
	Asset   string  `json:"asset"`
	Balance string  `json:"balance"` // inputs - outputs
	Volumes Volumes `json:"volumes"`
}

// LogEntry is an immutable audit trail record.
type LogEntry struct {
	ID             int64             `json:"id"`
	Ledger         string            `json:"ledger"`
	Type           LogType           `json:"type"`
	Data           map[string]any    `json:"data"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	Hash           []byte            `json:"hash,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
}

type LogType string

const (
	LogNewTransaction      LogType = "NEW_TRANSACTION"
	LogRevertedTransaction LogType = "REVERTED_TRANSACTION"
	LogSetMetadata         LogType = "SET_METADATA"
)

// CreateTransactionRequest is the input for posting a new transaction.
type CreateTransactionRequest struct {
	Reference string            `json:"reference,omitempty"`
	Postings  []Posting         `json:"postings"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Timestamp *time.Time        `json:"timestamp,omitempty"` // defaults to now
}

// Move is an individual account-level movement for balance tracking.
// Two moves are created per posting: one for source (debit), one for destination (credit).
type Move struct {
	ID                int64     `json:"id"`
	Ledger            string    `json:"ledger"`
	TransactionID     int64     `json:"transaction_id"`
	PostingID         int64     `json:"posting_id"`
	AccountAddress    string    `json:"account_address"`
	Asset             string    `json:"asset"`
	Amount            string    `json:"amount"`
	IsSource          bool      `json:"is_source"` // true=debit (outflow), false=credit (inflow)
	PostCommitInputs  string    `json:"post_commit_inputs"`
	PostCommitOutputs string    `json:"post_commit_outputs"`
	CreatedAt         time.Time `json:"created_at"`
}
