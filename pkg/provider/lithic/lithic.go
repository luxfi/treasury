package lithic

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

// Lithic — Modern card issuing platform.
// Virtual + physical cards, spend controls, real-time auth.

const (
	ProdURL    = "https://api.lithic.com/v1"
	SandboxURL = "https://sandbox.lithic.com/v1"
)

type Config struct{ BaseURL, APIKey string }

type Provider struct{ cfg Config; client *http.Client }

func New(cfg Config) *Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = ProdURL
	}
	return &Provider{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (p *Provider) Name() string { return "lithic" }

func (p *Provider) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.cfg.BaseURL+path, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "api-key "+p.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("lithic error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func (p *Provider) Capabilities() *types.ProviderCapability {
	return &types.ProviderCapability{
		Name:         "lithic",
		PaymentTypes: []string{"card"},
		Currencies:   []string{"USD"},
		Features:     []string{"virtual_cards", "physical_cards", "spend_controls", "real_time_auth", "tokenization"},
		Status:       "active",
	}
}

func (p *Provider) CreateAccount(ctx context.Context, _ string, _ *provider.CreateAccountRequest) (*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "POST", "/financial_accounts", map[string]interface{}{"type": "ISSUING"})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Token    string `json:"token"`
		Nickname string `json:"nickname"`
	}
	json.Unmarshal(data, &resp)
	return &types.BankAccount{
		ID: resp.Token, Provider: "lithic", ProviderID: resp.Token,
		AccountName: resp.Nickname, Status: "active", Currency: "USD", AccountType: "card_issuing",
	}, nil
}

func (p *Provider) GetAccount(ctx context.Context, accountID string) (*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/financial_accounts/"+accountID, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Token    string `json:"token"`
		Nickname string `json:"nickname"`
	}
	json.Unmarshal(data, &resp)
	return &types.BankAccount{
		ID: resp.Token, Provider: "lithic", ProviderID: resp.Token,
		AccountName: resp.Nickname, Status: "active", Currency: "USD", AccountType: "card_issuing",
	}, nil
}

func (p *Provider) ListAccounts(ctx context.Context, _ string) ([]*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/financial_accounts", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []struct {
			Token    string `json:"token"`
			Nickname string `json:"nickname"`
		} `json:"data"`
	}
	json.Unmarshal(data, &resp)
	accts := make([]*types.BankAccount, 0, len(resp.Data))
	for _, a := range resp.Data {
		accts = append(accts, &types.BankAccount{
			ID: a.Token, Provider: "lithic", ProviderID: a.Token,
			AccountName: a.Nickname, Status: "active", Currency: "USD", AccountType: "card_issuing",
		})
	}
	return accts, nil
}

func (p *Provider) GetBalance(ctx context.Context, accountID string) (*types.Balance, error) {
	data, err := p.doRequest(ctx, "GET", "/financial_accounts/"+accountID+"/balances", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []struct {
			Currency        string `json:"currency"`
			AvailableAmount int64  `json:"available_amount"`
			PendingAmount   int64  `json:"pending_amount"`
			TotalAmount     int64  `json:"total_amount"`
		} `json:"data"`
	}
	json.Unmarshal(data, &resp)
	if len(resp.Data) == 0 {
		return &types.Balance{Currency: "USD"}, nil
	}
	b := resp.Data[0]
	return &types.Balance{
		Currency:  b.Currency,
		Available: fmt.Sprintf("%.2f", float64(b.AvailableAmount)/100),
		Current:   fmt.Sprintf("%.2f", float64(b.TotalAmount)/100),
		Pending:   fmt.Sprintf("%.2f", float64(b.PendingAmount)/100),
	}, nil
}

func (p *Provider) CreatePayment(ctx context.Context, req *types.CreatePaymentRequest) (*types.Payment, error) {
	body := map[string]interface{}{
		"amount":                  req.Amount,
		"descriptor":              req.Reference,
		"financial_account_token": req.SourceAccountID,
	}
	data, err := p.doRequest(ctx, "POST", "/simulate/authorize", body)
	if err != nil {
		return nil, err
	}
	var resp struct{ Token string `json:"token"` }
	json.Unmarshal(data, &resp)
	return &types.Payment{
		ID: resp.Token, Provider: "lithic", ProviderID: resp.Token,
		Type: types.PaymentCard, Status: types.PaymentProcessing,
		Amount: req.Amount, Currency: "USD",
	}, nil
}

func (p *Provider) GetPayment(ctx context.Context, paymentID string) (*types.Payment, error) {
	data, err := p.doRequest(ctx, "GET", "/transactions/"+paymentID, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Token  string `json:"token"`
		Status string `json:"status"`
		Amount int64  `json:"amount"`
	}
	json.Unmarshal(data, &resp)
	return &types.Payment{
		ID: resp.Token, Provider: "lithic", ProviderID: resp.Token,
		Type: types.PaymentCard, Status: types.PaymentStatus(resp.Status),
		Amount: fmt.Sprintf("%d", resp.Amount), Currency: "USD",
	}, nil
}

func (p *Provider) ListPayments(ctx context.Context, accountID string) ([]*types.Payment, error) {
	path := "/transactions"
	if accountID != "" {
		path += "?financial_account_token=" + accountID
	}
	data, err := p.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []struct {
			Token  string `json:"token"`
			Status string `json:"status"`
			Amount int64  `json:"amount"`
		} `json:"data"`
	}
	json.Unmarshal(data, &resp)
	payments := make([]*types.Payment, 0, len(resp.Data))
	for _, t := range resp.Data {
		payments = append(payments, &types.Payment{
			ID: t.Token, Provider: "lithic", ProviderID: t.Token,
			Type: types.PaymentCard, Status: types.PaymentStatus(t.Status),
			Amount: fmt.Sprintf("%d", t.Amount), Currency: "USD",
		})
	}
	return payments, nil
}

func (p *Provider) CancelPayment(ctx context.Context, paymentID string) error {
	_, err := p.doRequest(ctx, "POST", "/transactions/"+paymentID+"/void", nil)
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
