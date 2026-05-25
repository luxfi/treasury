package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/luxfi/treasury/pkg/types"
)

// Provider is the unified interface every treasury backend must implement.
// Not all providers support all methods — unsupported methods return ErrNotSupported.
type Provider interface {
	// Name returns the provider identifier (e.g. "currencycloud", "column", "plaid").
	Name() string

	// Bank Accounts
	CreateAccount(ctx context.Context, orgID string, req *CreateAccountRequest) (*types.BankAccount, error)
	GetAccount(ctx context.Context, accountID string) (*types.BankAccount, error)
	ListAccounts(ctx context.Context, orgID string) ([]*types.BankAccount, error)
	GetBalance(ctx context.Context, accountID string) (*types.Balance, error)

	// Payments
	CreatePayment(ctx context.Context, req *types.CreatePaymentRequest) (*types.Payment, error)
	GetPayment(ctx context.Context, paymentID string) (*types.Payment, error)
	ListPayments(ctx context.Context, accountID string) ([]*types.Payment, error)
	CancelPayment(ctx context.Context, paymentID string) error

	// FX
	GetFXQuote(ctx context.Context, sellCcy, buyCcy, amount, fixedSide string) (*types.FXQuote, error)
	CreateFXConversion(ctx context.Context, req *types.CreateFXConversionRequest) (*types.FXConversion, error)
	ListFXConversions(ctx context.Context) ([]*types.FXConversion, error)

	// Counterparties
	CreateCounterparty(ctx context.Context, cp *types.Counterparty) (*types.Counterparty, error)
	ListCounterparties(ctx context.Context) ([]*types.Counterparty, error)

	// Capabilities
	Capabilities() *types.ProviderCapability
}

// CreateAccountRequest for opening bank accounts.
type CreateAccountRequest struct {
	AccountName string `json:"account_name"`
	Currency    string `json:"currency"`
	AccountType string `json:"account_type"` // checking, savings, virtual
	Country     string `json:"country,omitempty"`
}

// ErrNotSupported is returned when a provider doesn't support an operation.
var ErrNotSupported = fmt.Errorf("operation not supported by this provider")

// DeriveIdempotencyKey returns a stable SHA-256 hex digest of
// (orgID || 0x00 || endpoint || 0x00 || canonicalRequestBody) for use
// when the caller did not supply an explicit IdempotencyKey. The output
// is purely a function of its inputs — a retry with the same orgID,
// endpoint, and body produces the same key, so the upstream provider
// can collapse duplicates. It is NEVER derived from a wall-clock value.
//
// Adapters MUST prefer the caller-supplied req.IdempotencyKey when set,
// and fall back to this helper only when it is empty.
func DeriveIdempotencyKey(orgID, endpoint string, canonicalBody []byte) string {
	h := sha256.New()
	h.Write([]byte(orgID))
	h.Write([]byte{0})
	h.Write([]byte(endpoint))
	h.Write([]byte{0})
	h.Write(canonicalBody)
	return hex.EncodeToString(h.Sum(nil))
}

// Registry holds all registered treasury providers.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
}

func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("treasury provider %q not registered", name)
	}
	return p, nil
}

func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for n := range r.providers {
		names = append(names, n)
	}
	return names
}

func (r *Registry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		all = append(all, p)
	}
	return all
}
