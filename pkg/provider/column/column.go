package column

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
	ProdURL    = "https://api.column.com"
	SandboxURL = "https://api-sandbox.column.com"
)

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

func (p *Provider) Name() string { return "column" }

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
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
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
		return nil, fmt.Errorf("column API error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func (p *Provider) CreateAccount(ctx context.Context, orgID string, req *provider.CreateAccountRequest) (*types.BankAccount, error) {
	body := map[string]interface{}{
		"description": req.AccountName,
	}

	data, err := p.doRequest(ctx, "POST", "/bank-accounts", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID            string `json:"id"`
		Description   string `json:"description"`
		AccountNumber string `json:"account_number"`
		RoutingNumber string `json:"routing_number"`
		Status        string `json:"status"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.BankAccount{
		ID:            resp.ID,
		Provider:      "column",
		ProviderID:    resp.ID,
		OrgID:         orgID,
		AccountName:   resp.Description,
		AccountNumber: resp.AccountNumber,
		RoutingNumber: resp.RoutingNumber,
		Currency:      "USD",
		AccountType:   "checking",
		Status:        resp.Status,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}, nil
}

func (p *Provider) GetAccount(ctx context.Context, accountID string) (*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/bank-accounts/"+accountID, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID            string `json:"id"`
		Description   string `json:"description"`
		AccountNumber string `json:"account_number"`
		RoutingNumber string `json:"routing_number"`
		Status        string `json:"status"`
		Balance       int64  `json:"available_balance"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.BankAccount{
		ID:            resp.ID,
		Provider:      "column",
		ProviderID:    resp.ID,
		AccountName:   resp.Description,
		AccountNumber: resp.AccountNumber,
		RoutingNumber: resp.RoutingNumber,
		Currency:      "USD",
		Status:        resp.Status,
		Balance: &types.Balance{
			Available: fmt.Sprintf("%d", resp.Balance),
			Current:   fmt.Sprintf("%d", resp.Balance),
			Currency:  "USD",
			UpdatedAt: time.Now(),
		},
	}, nil
}

func (p *Provider) ListAccounts(ctx context.Context, orgID string) ([]*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/bank-accounts", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		BankAccounts []struct {
			ID            string `json:"id"`
			Description   string `json:"description"`
			AccountNumber string `json:"account_number"`
			RoutingNumber string `json:"routing_number"`
			Status        string `json:"status"`
		} `json:"bank_accounts"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	accounts := make([]*types.BankAccount, 0, len(resp.BankAccounts))
	for _, item := range resp.BankAccounts {
		accounts = append(accounts, &types.BankAccount{
			ID:            item.ID,
			Provider:      "column",
			ProviderID:    item.ID,
			OrgID:         orgID,
			AccountName:   item.Description,
			AccountNumber: item.AccountNumber,
			RoutingNumber: item.RoutingNumber,
			Currency:      "USD",
			Status:        item.Status,
		})
	}
	return accounts, nil
}

func (p *Provider) GetBalance(ctx context.Context, accountID string) (*types.Balance, error) {
	acct, err := p.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return acct.Balance, nil
}

func (p *Provider) CreatePayment(ctx context.Context, req *types.CreatePaymentRequest) (*types.Payment, error) {
	endpoint := "/transfers/ach"
	if req.Type == types.PaymentWire {
		endpoint = "/transfers/wire"
	} else if req.Type == types.PaymentBook {
		endpoint = "/transfers/book"
	}

	body := map[string]interface{}{
		"amount":                req.Amount,
		"currency_code":        req.Currency,
		"bank_account_id":      req.SourceAccountID,
		"description":          req.Description,
	}
	if req.Counterparty != nil {
		body["counterparty_id"] = req.Counterparty.ID
	}

	data, err := p.doRequest(ctx, "POST", endpoint, body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID       string `json:"id"`
		Amount   int64  `json:"amount"`
		Currency string `json:"currency_code"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Payment{
		ID:              resp.ID,
		Provider:        "column",
		ProviderID:      resp.ID,
		Type:            req.Type,
		Direction:       req.Direction,
		Amount:          fmt.Sprintf("%d", resp.Amount),
		Currency:        resp.Currency,
		Status:          types.PaymentStatus(resp.Status),
		SourceAccountID: req.SourceAccountID,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}, nil
}

func (p *Provider) GetPayment(ctx context.Context, paymentID string) (*types.Payment, error) {
	// Try ACH first, then wire
	data, err := p.doRequest(ctx, "GET", "/transfers/ach/"+paymentID, nil)
	if err != nil {
		data, err = p.doRequest(ctx, "GET", "/transfers/wire/"+paymentID, nil)
		if err != nil {
			return nil, err
		}
	}

	var resp struct {
		ID     string `json:"id"`
		Amount int64  `json:"amount"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Payment{
		ID:         resp.ID,
		Provider:   "column",
		ProviderID: resp.ID,
		Amount:     fmt.Sprintf("%d", resp.Amount),
		Status:     types.PaymentStatus(resp.Status),
	}, nil
}

func (p *Provider) ListPayments(ctx context.Context, accountID string) ([]*types.Payment, error) {
	path := "/transfers/ach"
	if accountID != "" {
		path += "?bank_account_id=" + accountID
	}
	data, err := p.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Transfers []struct {
			ID     string `json:"id"`
			Amount int64  `json:"amount"`
			Status string `json:"status"`
		} `json:"ach_transfers"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	payments := make([]*types.Payment, 0, len(resp.Transfers))
	for _, item := range resp.Transfers {
		payments = append(payments, &types.Payment{
			ID:         item.ID,
			Provider:   "column",
			ProviderID: item.ID,
			Type:       types.PaymentACH,
			Amount:     fmt.Sprintf("%d", item.Amount),
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
		"name":           cp.Name,
		"account_number": cp.AccountNumber,
		"routing_number": cp.RoutingNumber,
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

	var resp struct {
		Counterparties []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"counterparties"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	cps := make([]*types.Counterparty, 0, len(resp.Counterparties))
	for _, item := range resp.Counterparties {
		cps = append(cps, &types.Counterparty{ID: item.ID, Name: item.Name})
	}
	return cps, nil
}

func (p *Provider) Capabilities() *types.ProviderCapability {
	return &types.ProviderCapability{
		Name:            "column",
		PaymentTypes:    []string{"ach", "wire", "book", "rtp", "fednow", "check"},
		Currencies:      []string{"USD"},
		Features:        []string{"direct_bank", "fed_access", "same_day_ach", "fednow", "check_issuing"},
		Countries:       []string{"US"},
		SettlementSpeed: "same_day",
		Status:          "active",
	}
}
