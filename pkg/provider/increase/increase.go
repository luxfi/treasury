package increase

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

// Increase — Banking infrastructure API.
// Direct Fed access: ACH, wire, real-time payments, check deposits.

const (
	ProdURL    = "https://api.increase.com"
	SandboxURL = "https://sandbox.increase.com"
)

type Config struct{ BaseURL, APIKey string }

type Provider struct{ cfg Config; client *http.Client }

func New(cfg Config) *Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = ProdURL
	}
	return &Provider{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (p *Provider) Name() string { return "increase" }

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
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("increase error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func (p *Provider) Capabilities() *types.ProviderCapability {
	return &types.ProviderCapability{
		Name:         "increase",
		PaymentTypes: []string{"ach", "wire", "rtp", "check"},
		Currencies:   []string{"USD"},
		Features:     []string{"ach", "wire", "rtp", "check_deposit", "check_transfer"},
		Status:       "active",
	}
}

func (p *Provider) CreateAccount(ctx context.Context, orgID string, req *provider.CreateAccountRequest) (*types.BankAccount, error) {
	body := map[string]interface{}{
		"name":       req.AccountName,
		"program_id": orgID,
	}
	data, err := p.doRequest(ctx, "POST", "/accounts", body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Status   string `json:"status"`
		Currency string `json:"currency"`
	}
	json.Unmarshal(data, &resp)
	return &types.BankAccount{
		ID: resp.ID, Provider: "increase", ProviderID: resp.ID,
		AccountName: resp.Name, Currency: resp.Currency, Status: resp.Status,
	}, nil
}

func (p *Provider) GetAccount(ctx context.Context, accountID string) (*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/accounts/"+accountID, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Status   string `json:"status"`
		Currency string `json:"currency"`
	}
	json.Unmarshal(data, &resp)
	return &types.BankAccount{
		ID: resp.ID, Provider: "increase", ProviderID: resp.ID,
		AccountName: resp.Name, Currency: resp.Currency, Status: resp.Status,
	}, nil
}

func (p *Provider) ListAccounts(ctx context.Context, _ string) ([]*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/accounts", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Status   string `json:"status"`
			Currency string `json:"currency"`
		} `json:"data"`
	}
	json.Unmarshal(data, &resp)
	accts := make([]*types.BankAccount, 0, len(resp.Data))
	for _, a := range resp.Data {
		accts = append(accts, &types.BankAccount{
			ID: a.ID, Provider: "increase", ProviderID: a.ID,
			AccountName: a.Name, Currency: a.Currency, Status: a.Status,
		})
	}
	return accts, nil
}

func (p *Provider) GetBalance(ctx context.Context, accountID string) (*types.Balance, error) {
	data, err := p.doRequest(ctx, "GET", "/accounts/"+accountID, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Balance  int64  `json:"balance"`
		Currency string `json:"currency"`
	}
	json.Unmarshal(data, &resp)
	amt := fmt.Sprintf("%.2f", float64(resp.Balance)/100)
	return &types.Balance{
		Currency:  resp.Currency,
		Available: amt,
		Current:   amt,
	}, nil
}

func (p *Provider) CreatePayment(ctx context.Context, req *types.CreatePaymentRequest) (*types.Payment, error) {
	switch req.Type {
	case types.PaymentACH:
		cp := req.Counterparty
		if cp == nil {
			return nil, fmt.Errorf("increase: counterparty required for ACH")
		}
		body := map[string]interface{}{
			"account_id":           req.SourceAccountID,
			"amount":               req.Amount,
			"routing_number":       cp.RoutingNumber,
			"account_number":       cp.AccountNumber,
			"statement_descriptor": req.Reference,
		}
		data, err := p.doRequest(ctx, "POST", "/ach_transfers", body)
		if err != nil {
			return nil, err
		}
		var resp struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		json.Unmarshal(data, &resp)
		return &types.Payment{
			ID: resp.ID, Provider: "increase", ProviderID: resp.ID,
			Type: types.PaymentACH, Status: PaymentStatus(resp.Status),
			Amount: req.Amount, Currency: req.Currency,
		}, nil
	case types.PaymentWire:
		cp := req.Counterparty
		if cp == nil {
			return nil, fmt.Errorf("increase: counterparty required for wire")
		}
		body := map[string]interface{}{
			"account_id":            req.SourceAccountID,
			"amount":                req.Amount,
			"routing_number":        cp.RoutingNumber,
			"account_number":        cp.AccountNumber,
			"message_to_recipient":  req.Reference,
		}
		data, err := p.doRequest(ctx, "POST", "/wire_transfers", body)
		if err != nil {
			return nil, err
		}
		var resp struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		json.Unmarshal(data, &resp)
		return &types.Payment{
			ID: resp.ID, Provider: "increase", ProviderID: resp.ID,
			Type: types.PaymentWire, Status: PaymentStatus(resp.Status),
			Amount: req.Amount, Currency: req.Currency,
		}, nil
	default:
		return nil, fmt.Errorf("increase: unsupported payment type %s", req.Type)
	}
}

func PaymentStatus(s string) types.PaymentStatus {
	switch s {
	case "pending_approval", "pending_reviewing":
		return types.PaymentPending
	case "submitted", "pending_submission":
		return types.PaymentProcessing
	case "complete":
		return types.PaymentCompleted
	case "canceled":
		return types.PaymentCancelled
	case "returned":
		return types.PaymentReturned
	default:
		return types.PaymentStatus(s)
	}
}

func (p *Provider) GetPayment(ctx context.Context, paymentID string) (*types.Payment, error) {
	data, err := p.doRequest(ctx, "GET", "/ach_transfers/"+paymentID, nil)
	if err != nil {
		data, err = p.doRequest(ctx, "GET", "/wire_transfers/"+paymentID, nil)
		if err != nil {
			return nil, err
		}
	}
	var resp struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Amount int64  `json:"amount"`
	}
	json.Unmarshal(data, &resp)
	return &types.Payment{
		ID: resp.ID, Provider: "increase", ProviderID: resp.ID,
		Status: PaymentStatus(resp.Status), Amount: fmt.Sprintf("%d", resp.Amount),
	}, nil
}

func (p *Provider) ListPayments(ctx context.Context, accountID string) ([]*types.Payment, error) {
	path := "/ach_transfers"
	if accountID != "" {
		path += "?account_id=" + accountID
	}
	data, err := p.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Amount int64  `json:"amount"`
		} `json:"data"`
	}
	json.Unmarshal(data, &resp)
	payments := make([]*types.Payment, 0, len(resp.Data))
	for _, t := range resp.Data {
		payments = append(payments, &types.Payment{
			ID: t.ID, Provider: "increase", ProviderID: t.ID,
			Type: types.PaymentACH, Status: PaymentStatus(t.Status),
			Amount: fmt.Sprintf("%d", t.Amount),
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

func (p *Provider) CreateCounterparty(_ context.Context, _ *types.Counterparty) (*types.Counterparty, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) ListCounterparties(_ context.Context) ([]*types.Counterparty, error) {
	return nil, provider.ErrNotSupported
}
