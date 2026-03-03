package marqeta

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

func New(cfg Config) provider.Provider {
	return &Provider{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (p *Provider) Name() string { return "marqeta" }

func (p *Provider) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, p.cfg.BaseURL+"/v3"+path, reqBody)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(p.cfg.AppToken, p.cfg.AccessToken)
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
		return nil, fmt.Errorf("marqeta API error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func (p *Provider) CreateAccount(_ context.Context, _ string, _ *provider.CreateAccountRequest) (*types.BankAccount, error) {
	return nil, provider.ErrNotSupported
}
func (p *Provider) GetAccount(_ context.Context, _ string) (*types.BankAccount, error) {
	return nil, provider.ErrNotSupported
}
func (p *Provider) ListAccounts(_ context.Context, _ string) ([]*types.BankAccount, error) {
	return nil, provider.ErrNotSupported
}
func (p *Provider) GetBalance(_ context.Context, _ string) (*types.Balance, error) {
	return nil, provider.ErrNotSupported
}

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

func (p *Provider) GetFXQuote(_ context.Context, _, _, _, _ string) (*types.FXQuote, error) {
	return nil, provider.ErrNotSupported
}
func (p *Provider) CreateFXConversion(_ context.Context, _ *types.CreateFXConversionRequest) (*types.FXConversion, error) {
	return nil, provider.ErrNotSupported
}
func (p *Provider) ListFXConversions(_ context.Context) ([]*types.FXConversion, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) CreateCounterparty(_ context.Context, _ *types.Counterparty) (*types.Counterparty, error) {
	return nil, provider.ErrNotSupported
}
func (p *Provider) ListCounterparties(_ context.Context) ([]*types.Counterparty, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) Capabilities() *types.ProviderCapability {
	return &types.ProviderCapability{
		Name:         "marqeta",
		PaymentTypes: []string{"card"},
		Currencies:   []string{"USD"},
		Features:     []string{"card_issuing", "virtual_cards", "physical_cards", "spend_controls", "tokenization"},
		Countries:    []string{"US"},
		Status:       "active",
	}
}
