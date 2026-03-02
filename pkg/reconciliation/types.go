package reconciliation

import "time"

// Policy defines a reconciliation rule: compare ledger state vs provider state.
type Policy struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	LedgerName   string    `json:"ledger_name"`
	LedgerQuery  string    `json:"ledger_query"`   // account address pattern (e.g., "providers:stripe:*")
	ProviderName string    `json:"provider_name"`
	CreatedAt    time.Time `json:"created_at"`
}

// Result is the outcome of running a reconciliation.
type Result struct {
	ID               string          `json:"id"`
	PolicyID         string          `json:"policy_id"`
	Status           Status          `json:"status"` // ok, drift, error
	LedgerBalances   map[string]string `json:"ledger_balances"`   // asset → balance
	ProviderBalances map[string]string `json:"provider_balances"` // asset → balance
	Drifts           []Drift         `json:"drifts,omitempty"`
	Error            string          `json:"error,omitempty"`
	RunAt            time.Time       `json:"run_at"`
}

type Status string

const (
	StatusOK    Status = "ok"
	StatusDrift Status = "drift"
	StatusError Status = "error"
)

// Drift represents a balance discrepancy between ledger and provider.
type Drift struct {
	Asset          string `json:"asset"`
	LedgerBalance  string `json:"ledger_balance"`
	ProviderBalance string `json:"provider_balance"`
	Difference     string `json:"difference"`
}
