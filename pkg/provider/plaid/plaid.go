package plaid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/luxfi/treasury/pkg/provider"
	"github.com/luxfi/treasury/pkg/types"
)

const (
	ProdURL    = "https://production.plaid.com"
	SandboxURL = "https://sandbox.plaid.com"
)

type Config struct {
	BaseURL  string
	ClientID string
	Secret   string
}

type Provider struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) provider.Provider {
	return &Provider{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *Provider) Name() string { return "plaid" }

func (p *Provider) doRequest(ctx context.Context, path string, body interface{}) ([]byte, error) {
	// Plaid requires client_id and secret in every request body
	payload := map[string]interface{}{
		"client_id": p.cfg.ClientID,
		"secret":    p.cfg.Secret,
	}
	if body != nil {
		if m, ok := body.(map[string]interface{}); ok {
			for k, v := range m {
				payload[k] = v
			}
		}
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("plaid API error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

// --- Accounts (requires access_token from Link flow) ---

func (p *Provider) CreateAccount(_ context.Context, _ string, _ *provider.CreateAccountRequest) (*types.BankAccount, error) {
	return nil, fmt.Errorf("plaid: use Link flow to connect bank accounts, not direct creation")
}

func (p *Provider) GetAccount(ctx context.Context, accountID string) (*types.BankAccount, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) ListAccounts(ctx context.Context, orgID string) ([]*types.BankAccount, error) {
	// This would require an access_token per linked institution
	return nil, provider.ErrNotSupported
}

func (p *Provider) GetBalance(ctx context.Context, accountID string) (*types.Balance, error) {
	return nil, provider.ErrNotSupported
}

// --- Payments (Plaid is read-only for transactions) ---

func (p *Provider) CreatePayment(_ context.Context, _ *types.CreatePaymentRequest) (*types.Payment, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) GetPayment(_ context.Context, _ string) (*types.Payment, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) ListPayments(_ context.Context, _ string) ([]*types.Payment, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) CancelPayment(_ context.Context, _ string) error {
	return provider.ErrNotSupported
}

// --- FX (not supported) ---

func (p *Provider) GetFXQuote(_ context.Context, _, _, _, _ string) (*types.FXQuote, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) CreateFXConversion(_ context.Context, _ *types.CreateFXConversionRequest) (*types.FXConversion, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) ListFXConversions(_ context.Context) ([]*types.FXConversion, error) {
	return nil, provider.ErrNotSupported
}

// --- Counterparties (not applicable) ---

func (p *Provider) CreateCounterparty(_ context.Context, _ *types.Counterparty) (*types.Counterparty, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) ListCounterparties(_ context.Context) ([]*types.Counterparty, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) Capabilities() *types.ProviderCapability {
	return &types.ProviderCapability{
		Name:         "plaid",
		PaymentTypes: []string{},
		Currencies:   []string{"USD"},
		Features:     []string{"bank_linking", "identity", "transactions", "balance", "kyc", "auth"},
		Countries:    []string{"US", "CA", "GB"},
		Status:       "active",
	}
}
