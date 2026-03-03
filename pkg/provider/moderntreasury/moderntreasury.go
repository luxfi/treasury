package moderntreasury

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

const ProdURL = "https://app.moderntreasury.com"

type Config struct {
	BaseURL string
	OrgID   string
	APIKey  string
}

type Provider struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) provider.Provider {
	return &Provider{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (p *Provider) Name() string { return "moderntreasury" }

func (p *Provider) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, p.cfg.BaseURL+"/api"+path, reqBody)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(p.cfg.OrgID, p.cfg.APIKey)
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
		return nil, fmt.Errorf("modern treasury API error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func (p *Provider) CreateAccount(ctx context.Context, orgID string, req *provider.CreateAccountRequest) (*types.BankAccount, error) {
	body := map[string]interface{}{
		"name":         req.AccountName,
		"party_name":   req.AccountName,
		"currency":     req.Currency,
		"account_type": "internal",
	}

	data, err := p.doRequest(ctx, "POST", "/internal_accounts", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Currency      string `json:"currency"`
		AccountNumber string `json:"account_number"`
		RoutingNumber string `json:"routing_number"`
		Status        string `json:"status"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.BankAccount{
		ID:            resp.ID,
		Provider:      "moderntreasury",
		ProviderID:    resp.ID,
		OrgID:         orgID,
		AccountName:   resp.Name,
		AccountNumber: resp.AccountNumber,
		RoutingNumber: resp.RoutingNumber,
		Currency:      resp.Currency,
		AccountType:   "checking",
		Status:        "active",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}, nil
}

func (p *Provider) GetAccount(ctx context.Context, accountID string) (*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/internal_accounts/"+accountID, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Currency string `json:"currency"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.BankAccount{
		ID:          resp.ID,
		Provider:    "moderntreasury",
		ProviderID:  resp.ID,
		AccountName: resp.Name,
		Currency:    resp.Currency,
		Status:      "active",
	}, nil
}

func (p *Provider) ListAccounts(ctx context.Context, orgID string) ([]*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/internal_accounts", nil)
	if err != nil {
		return nil, err
	}

	var items []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Currency string `json:"currency"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}

	accounts := make([]*types.BankAccount, 0, len(items))
	for _, item := range items {
		accounts = append(accounts, &types.BankAccount{
			ID:          item.ID,
			Provider:    "moderntreasury",
			ProviderID:  item.ID,
			OrgID:       orgID,
			AccountName: item.Name,
			Currency:    item.Currency,
			Status:      "active",
		})
	}
	return accounts, nil
}

func (p *Provider) GetBalance(ctx context.Context, accountID string) (*types.Balance, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) CreatePayment(ctx context.Context, req *types.CreatePaymentRequest) (*types.Payment, error) {
	body := map[string]interface{}{
		"type":                string(req.Type),
		"direction":           req.Direction,
		"amount":              req.Amount,
		"currency":            req.Currency,
		"originating_account_id": req.SourceAccountID,
		"description":         req.Description,
	}
	if req.Counterparty != nil {
		body["receiving_account_id"] = req.Counterparty.ID
	}

	data, err := p.doRequest(ctx, "POST", "/payment_orders", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Direction string `json:"direction"`
		Amount    int64  `json:"amount"`
		Currency  string `json:"currency"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Payment{
		ID:         resp.ID,
		Provider:   "moderntreasury",
		ProviderID: resp.ID,
		Type:       types.PaymentType(resp.Type),
		Direction:  resp.Direction,
		Amount:     fmt.Sprintf("%d", resp.Amount),
		Currency:   resp.Currency,
		Status:     types.PaymentStatus(resp.Status),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}, nil
}

func (p *Provider) GetPayment(ctx context.Context, paymentID string) (*types.Payment, error) {
	data, err := p.doRequest(ctx, "GET", "/payment_orders/"+paymentID, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Payment{
		ID:         resp.ID,
		Provider:   "moderntreasury",
		ProviderID: resp.ID,
		Type:       types.PaymentType(resp.Type),
		Amount:     fmt.Sprintf("%d", resp.Amount),
		Currency:   resp.Currency,
		Status:     types.PaymentStatus(resp.Status),
	}, nil
}

func (p *Provider) ListPayments(ctx context.Context, accountID string) ([]*types.Payment, error) {
	data, err := p.doRequest(ctx, "GET", "/payment_orders", nil)
	if err != nil {
		return nil, err
	}

	var items []struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}

	payments := make([]*types.Payment, 0, len(items))
	for _, item := range items {
		payments = append(payments, &types.Payment{
			ID:         item.ID,
			Provider:   "moderntreasury",
			ProviderID: item.ID,
			Type:       types.PaymentType(item.Type),
			Amount:     fmt.Sprintf("%d", item.Amount),
			Currency:   item.Currency,
			Status:     types.PaymentStatus(item.Status),
		})
	}
	return payments, nil
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

func (p *Provider) CreateCounterparty(ctx context.Context, cp *types.Counterparty) (*types.Counterparty, error) {
	body := map[string]interface{}{
		"name": cp.Name,
	}
	if cp.Email != "" {
		body["email"] = cp.Email
	}

	data, err := p.doRequest(ctx, "POST", "/counterparties", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	cp.ID = resp.ID
	return cp, nil
}

func (p *Provider) ListCounterparties(ctx context.Context) ([]*types.Counterparty, error) {
	data, err := p.doRequest(ctx, "GET", "/counterparties", nil)
	if err != nil {
		return nil, err
	}

	var items []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}

	cps := make([]*types.Counterparty, 0, len(items))
	for _, item := range items {
		cps = append(cps, &types.Counterparty{ID: item.ID, Name: item.Name})
	}
	return cps, nil
}

func (p *Provider) Capabilities() *types.ProviderCapability {
	return &types.ProviderCapability{
		Name:            "moderntreasury",
		PaymentTypes:    []string{"ach", "wire", "rtp", "sepa", "check"},
		Currencies:      []string{"USD", "EUR", "GBP", "CAD"},
		Features:        []string{"ledger", "payments", "counterparties", "compliance", "virtual_accounts"},
		Countries:       []string{"US", "GB", "EU"},
		SettlementSpeed: "same_day",
		Status:          "active",
	}
}
