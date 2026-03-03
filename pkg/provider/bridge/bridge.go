package bridge

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

// Bridge (by Stripe) — stablecoin orchestration API.
// Enables USDC <> fiat conversions, cross-border stablecoin payments.
const ProdURL = "https://api.bridge.xyz"

type Config struct {
	BaseURL string
	APIKey  string
}

type Provider struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) provider.Provider {
	return &Provider{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (p *Provider) Name() string { return "bridge" }

func (p *Provider) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, p.cfg.BaseURL+path, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Api-Key", p.cfg.APIKey)
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
		return nil, fmt.Errorf("bridge API error %d: %s", resp.StatusCode, string(data))
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

func (p *Provider) CreatePayment(ctx context.Context, req *types.CreatePaymentRequest) (*types.Payment, error) {
	body := map[string]interface{}{
		"amount":          req.Amount,
		"source_currency": req.Currency,
		"destination_currency": "usdc",
		"on_behalf_of":    req.SourceAccountID,
	}

	data, err := p.doRequest(ctx, "POST", "/v0/transfers", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID     string `json:"id"`
		State  string `json:"state"`
		Amount string `json:"amount"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Payment{
		ID:         resp.ID,
		Provider:   "bridge",
		ProviderID: resp.ID,
		Type:       types.PaymentType("stablecoin"),
		Direction:  req.Direction,
		Amount:     resp.Amount,
		Currency:   req.Currency,
		Status:     types.PaymentStatus(resp.State),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}, nil
}

func (p *Provider) GetPayment(ctx context.Context, paymentID string) (*types.Payment, error) {
	data, err := p.doRequest(ctx, "GET", "/v0/transfers/"+paymentID, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Payment{
		ID:         resp.ID,
		Provider:   "bridge",
		ProviderID: resp.ID,
		Status:     types.PaymentStatus(resp.State),
	}, nil
}

func (p *Provider) ListPayments(ctx context.Context, accountID string) ([]*types.Payment, error) {
	data, err := p.doRequest(ctx, "GET", "/v0/transfers", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	payments := make([]*types.Payment, 0, len(resp.Data))
	for _, item := range resp.Data {
		payments = append(payments, &types.Payment{
			ID:         item.ID,
			Provider:   "bridge",
			ProviderID: item.ID,
			Status:     types.PaymentStatus(item.State),
		})
	}
	return payments, nil
}

func (p *Provider) CancelPayment(_ context.Context, _ string) error {
	return provider.ErrNotSupported
}

func (p *Provider) GetFXQuote(ctx context.Context, sellCcy, buyCcy, amount, fixedSide string) (*types.FXQuote, error) {
	body := map[string]interface{}{
		"source_currency":      sellCcy,
		"destination_currency": buyCcy,
		"amount":               amount,
	}

	data, err := p.doRequest(ctx, "POST", "/v0/quotes", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Rate       string `json:"rate"`
		SrcAmount  string `json:"source_amount"`
		DestAmount string `json:"destination_amount"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.FXQuote{
		Provider:     "bridge",
		SellCurrency: sellCcy,
		BuyCurrency:  buyCcy,
		SellAmount:   resp.SrcAmount,
		BuyAmount:    resp.DestAmount,
		Rate:         resp.Rate,
		FixedSide:    fixedSide,
		ExpiresAt:    time.Now().Add(30 * time.Second),
		CreatedAt:    time.Now(),
	}, nil
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
		Name:            "bridge",
		PaymentTypes:    []string{"stablecoin", "wire"},
		Currencies:      []string{"USD", "EUR", "USDC", "USDT"},
		Features:        []string{"stablecoin_orchestration", "fiat_to_crypto", "crypto_to_fiat", "cross_border"},
		Countries:       []string{"US", "EU", "GB", "BR", "MX", "CO", "AR"},
		SettlementSpeed: "instant",
		Status:          "active",
	}
}
