package stripe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/luxfi/treasury/pkg/provider"
	"github.com/luxfi/treasury/pkg/types"
)

const ProdURL = "https://api.stripe.com"

type Config struct {
	BaseURL   string
	SecretKey string
}

type Provider struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) provider.Provider {
	return &Provider{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (p *Provider) Name() string { return "stripe" }

func (p *Provider) doRequest(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, p.cfg.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.SecretKey)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

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
		return nil, fmt.Errorf("stripe API error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func (p *Provider) CreateAccount(ctx context.Context, orgID string, req *provider.CreateAccountRequest) (*types.BankAccount, error) {
	form := url.Values{}
	form.Set("supported_currencies[]", req.Currency)
	form.Set("features[]", "financial_addresses.aba")

	data, err := p.doRequest(ctx, "POST", "/v1/treasury/financial_accounts", form)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		Currency string `json:"supported_currencies"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.BankAccount{
		ID:          resp.ID,
		Provider:    "stripe",
		ProviderID:  resp.ID,
		OrgID:       orgID,
		AccountName: req.AccountName,
		Currency:    req.Currency,
		AccountType: "financial_account",
		Status:      resp.Status,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}, nil
}

func (p *Provider) GetAccount(ctx context.Context, accountID string) (*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/v1/treasury/financial_accounts/"+accountID, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Balance struct {
			Cash map[string]int64 `json:"cash"`
		} `json:"balance"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.BankAccount{
		ID:         resp.ID,
		Provider:   "stripe",
		ProviderID: resp.ID,
		Status:     resp.Status,
	}, nil
}

func (p *Provider) ListAccounts(ctx context.Context, orgID string) ([]*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/v1/treasury/financial_accounts", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	accounts := make([]*types.BankAccount, 0, len(resp.Data))
	for _, item := range resp.Data {
		accounts = append(accounts, &types.BankAccount{
			ID:         item.ID,
			Provider:   "stripe",
			ProviderID: item.ID,
			OrgID:      orgID,
			Status:     item.Status,
		})
	}
	return accounts, nil
}

func (p *Provider) GetBalance(ctx context.Context, accountID string) (*types.Balance, error) {
	acct, err := p.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if acct.Balance != nil {
		return acct.Balance, nil
	}
	return &types.Balance{Currency: "USD", UpdatedAt: time.Now()}, nil
}

func (p *Provider) CreatePayment(ctx context.Context, req *types.CreatePaymentRequest) (*types.Payment, error) {
	form := url.Values{}
	form.Set("financial_account", req.SourceAccountID)
	form.Set("amount", req.Amount)
	form.Set("currency", req.Currency)
	form.Set("description", req.Description)
	if req.Counterparty != nil {
		form.Set("destination_payment_method", req.Counterparty.ID)
	}

	data, err := p.doRequest(ctx, "POST", "/v1/treasury/outbound_payments", form)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID       string `json:"id"`
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Payment{
		ID:         resp.ID,
		Provider:   "stripe",
		ProviderID: resp.ID,
		Type:       types.PaymentACH,
		Direction:  "credit",
		Amount:     fmt.Sprintf("%d", resp.Amount),
		Currency:   resp.Currency,
		Status:     types.PaymentStatus(resp.Status),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}, nil
}

func (p *Provider) GetPayment(ctx context.Context, paymentID string) (*types.Payment, error) {
	data, err := p.doRequest(ctx, "GET", "/v1/treasury/outbound_payments/"+paymentID, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID       string `json:"id"`
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Payment{
		ID:         resp.ID,
		Provider:   "stripe",
		ProviderID: resp.ID,
		Amount:     fmt.Sprintf("%d", resp.Amount),
		Currency:   resp.Currency,
		Status:     types.PaymentStatus(resp.Status),
	}, nil
}

func (p *Provider) ListPayments(ctx context.Context, accountID string) ([]*types.Payment, error) {
	path := "/v1/treasury/outbound_payments"
	if accountID != "" {
		path += "?financial_account=" + accountID
	}
	data, err := p.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data []struct {
			ID       string `json:"id"`
			Amount   int64  `json:"amount"`
			Currency string `json:"currency"`
			Status   string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	payments := make([]*types.Payment, 0, len(resp.Data))
	for _, item := range resp.Data {
		payments = append(payments, &types.Payment{
			ID:         item.ID,
			Provider:   "stripe",
			ProviderID: item.ID,
			Amount:     fmt.Sprintf("%d", item.Amount),
			Currency:   item.Currency,
			Status:     types.PaymentStatus(item.Status),
		})
	}
	return payments, nil
}

func (p *Provider) CancelPayment(ctx context.Context, paymentID string) error {
	form := url.Values{}
	_, err := p.doRequest(ctx, "POST", "/v1/treasury/outbound_payments/"+paymentID+"/cancel", form)
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
		Name:            "stripe",
		PaymentTypes:    []string{"ach", "wire"},
		Currencies:      []string{"USD"},
		Features:        []string{"baas", "cards", "financial_accounts", "treasury"},
		Countries:       []string{"US"},
		SettlementSpeed: "t+1",
		Status:          "active",
	}
}
