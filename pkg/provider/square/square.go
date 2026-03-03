package square

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
	ProdURL    = "https://connect.squareup.com"
	SandboxURL = "https://connect.squareupsandbox.com"
)

type Config struct {
	BaseURL     string
	AccessToken string
}

type Provider struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) provider.Provider {
	return &Provider{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (p *Provider) Name() string { return "square" }

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
	req.Header.Set("Authorization", "Bearer "+p.cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Square-Version", "2024-12-18")

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
		return nil, fmt.Errorf("square API error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func (p *Provider) CreateAccount(_ context.Context, _ string, _ *provider.CreateAccountRequest) (*types.BankAccount, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) GetAccount(_ context.Context, _ string) (*types.BankAccount, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) ListAccounts(ctx context.Context, orgID string) ([]*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/v2/bank-accounts", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		BankAccounts []struct {
			ID            string `json:"id"`
			AccountNumber string `json:"account_number_suffix"`
			BankName      string `json:"bank_name"`
			Status        string `json:"status"`
			Currency      string `json:"currency"`
			Balance       int64  `json:"balance"`
		} `json:"bank_accounts"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	accounts := make([]*types.BankAccount, 0, len(resp.BankAccounts))
	for _, a := range resp.BankAccounts {
		accounts = append(accounts, &types.BankAccount{
			ID:          a.ID,
			Provider:    "square",
			ProviderID:  a.ID,
			OrgID:       orgID,
			AccountName: a.BankName,
			Currency:    a.Currency,
			Status:      a.Status,
			Balance: &types.Balance{
				Available: fmt.Sprintf("%d", a.Balance),
				Current:   fmt.Sprintf("%d", a.Balance),
				Currency:  a.Currency,
			},
		})
	}
	return accounts, nil
}

func (p *Provider) GetBalance(_ context.Context, _ string) (*types.Balance, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) CreatePayment(ctx context.Context, req *types.CreatePaymentRequest) (*types.Payment, error) {
	body := map[string]interface{}{
		"source_id":      req.SourceAccountID,
		"amount_money": map[string]interface{}{
			"amount":   req.Amount,
			"currency": req.Currency,
		},
		"idempotency_key": fmt.Sprintf("lux_%d", time.Now().UnixNano()),
	}

	data, err := p.doRequest(ctx, "POST", "/v2/payments", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Payment struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			AmountMoney struct {
				Amount   int64  `json:"amount"`
				Currency string `json:"currency"`
			} `json:"amount_money"`
		} `json:"payment"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Payment{
		ID:         resp.Payment.ID,
		Provider:   "square",
		ProviderID: resp.Payment.ID,
		Type:       types.PaymentCard,
		Direction:  req.Direction,
		Amount:     fmt.Sprintf("%d", resp.Payment.AmountMoney.Amount),
		Currency:   resp.Payment.AmountMoney.Currency,
		Status:     types.PaymentStatus(resp.Payment.Status),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}, nil
}

func (p *Provider) GetPayment(ctx context.Context, paymentID string) (*types.Payment, error) {
	data, err := p.doRequest(ctx, "GET", "/v2/payments/"+paymentID, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Payment struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			AmountMoney struct {
				Amount   int64  `json:"amount"`
				Currency string `json:"currency"`
			} `json:"amount_money"`
		} `json:"payment"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Payment{
		ID:         resp.Payment.ID,
		Provider:   "square",
		ProviderID: resp.Payment.ID,
		Amount:     fmt.Sprintf("%d", resp.Payment.AmountMoney.Amount),
		Currency:   resp.Payment.AmountMoney.Currency,
		Status:     types.PaymentStatus(resp.Payment.Status),
	}, nil
}

func (p *Provider) ListPayments(ctx context.Context, accountID string) ([]*types.Payment, error) {
	body := map[string]interface{}{}
	data, err := p.doRequest(ctx, "POST", "/v2/payments/search", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Payments []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			AmountMoney struct {
				Amount   int64  `json:"amount"`
				Currency string `json:"currency"`
			} `json:"amount_money"`
		} `json:"payments"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	payments := make([]*types.Payment, 0, len(resp.Payments))
	for _, pm := range resp.Payments {
		payments = append(payments, &types.Payment{
			ID:         pm.ID,
			Provider:   "square",
			ProviderID: pm.ID,
			Type:       types.PaymentCard,
			Amount:     fmt.Sprintf("%d", pm.AmountMoney.Amount),
			Currency:   pm.AmountMoney.Currency,
			Status:     types.PaymentStatus(pm.Status),
		})
	}
	return payments, nil
}

func (p *Provider) CancelPayment(ctx context.Context, paymentID string) error {
	_, err := p.doRequest(ctx, "POST", "/v2/payments/"+paymentID+"/cancel", nil)
	return err
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
		Name:            "square",
		PaymentTypes:    []string{"card", "ach"},
		Currencies:      []string{"USD", "CAD", "GBP", "EUR", "AUD", "JPY"},
		Features:        []string{"card_payments", "invoicing", "pos", "online_payments", "subscriptions", "afterpay"},
		Countries:       []string{"US", "CA", "GB", "AU", "JP", "IE", "FR", "ES"},
		SettlementSpeed: "t+1",
		Status:          "active",
	}
}
