package marqeta

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/luxfi/treasury/pkg/provider"
	"github.com/luxfi/treasury/pkg/types"
)

const (
	ProdURL    = "https://api.marqeta.com"
	SandboxURL = "https://sandbox-api.marqeta.com"
)

type Config struct {
	BaseURL     string
	AppToken    string
	AccessToken string
}

type Provider struct {
	cfg    Config
	client *http.Client
}

// New constructs a placeholder Marqeta provider. It is not registered by
// cmd/treasuryd/main.go and every Provider method returns ErrNotSupported.
// See doc.go.
func New(cfg Config) provider.Provider {
	return &Provider{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (p *Provider) Name() string { return "marqeta" }

func notImpl(method string) error {
	return fmt.Errorf("marqeta adapter not implemented (%s) — see doc.go: %w", method, provider.ErrNotSupported)
}

func (p *Provider) CreateAccount(_ context.Context, _ string, _ *provider.CreateAccountRequest) (*types.BankAccount, error) {
	return nil, notImpl("CreateAccount")
}
func (p *Provider) GetAccount(_ context.Context, _ string) (*types.BankAccount, error) {
	return nil, notImpl("GetAccount")
}
func (p *Provider) ListAccounts(_ context.Context, _ string) ([]*types.BankAccount, error) {
	return nil, notImpl("ListAccounts")
}
func (p *Provider) GetBalance(_ context.Context, _ string) (*types.Balance, error) {
	return nil, notImpl("GetBalance")
}

func (p *Provider) CreatePayment(_ context.Context, _ *types.CreatePaymentRequest) (*types.Payment, error) {
	return nil, notImpl("CreatePayment")
}
func (p *Provider) GetPayment(_ context.Context, _ string) (*types.Payment, error) {
	return nil, notImpl("GetPayment")
}
func (p *Provider) ListPayments(_ context.Context, _ string) ([]*types.Payment, error) {
	return nil, notImpl("ListPayments")
}
func (p *Provider) CancelPayment(_ context.Context, _ string) error {
	return notImpl("CancelPayment")
}

func (p *Provider) GetFXQuote(_ context.Context, _, _, _, _ string) (*types.FXQuote, error) {
	return nil, notImpl("GetFXQuote")
}
func (p *Provider) CreateFXConversion(_ context.Context, _ *types.CreateFXConversionRequest) (*types.FXConversion, error) {
	return nil, notImpl("CreateFXConversion")
}
func (p *Provider) ListFXConversions(_ context.Context) ([]*types.FXConversion, error) {
	return nil, notImpl("ListFXConversions")
}

func (p *Provider) CreateCounterparty(_ context.Context, _ *types.Counterparty) (*types.Counterparty, error) {
	return nil, notImpl("CreateCounterparty")
}
func (p *Provider) ListCounterparties(_ context.Context) ([]*types.Counterparty, error) {
	return nil, notImpl("ListCounterparties")
}

// Capabilities returns a fully-empty capability set. The Marqeta adapter
// has zero real capabilities until a real implementation lands; routing
// logic MUST treat this provider as supporting nothing.
func (p *Provider) Capabilities() *types.ProviderCapability {
	return &types.ProviderCapability{
		Name:         "marqeta",
		PaymentTypes: []string{},
		Currencies:   []string{},
		Features:     []string{},
		Countries:    []string{},
		Status:       "placeholder",
	}
}
