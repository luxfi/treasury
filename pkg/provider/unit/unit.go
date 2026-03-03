package unit

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

// Unit — Banking-as-a-Service platform.
// White-label checking, savings, cards, ACH, wire.
const (
	ProdURL    = "https://api.s.unit.sh"
	SandboxURL = "https://api.s.unit.sh"
)

type Config struct {
	BaseURL string
	Token   string // Bearer token
}

type Provider struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) provider.Provider {
	return &Provider{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (p *Provider) Name() string { return "unit" }

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
	req.Header.Set("Authorization", "Bearer "+p.cfg.Token)
	req.Header.Set("Content-Type", "application/vnd.api+json")

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
		return nil, fmt.Errorf("unit API error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func (p *Provider) CreateAccount(ctx context.Context, orgID string, req *provider.CreateAccountRequest) (*types.BankAccount, error) {
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "depositAccount",
			"attributes": map[string]interface{}{
				"depositProduct": req.AccountType,
			},
		},
	}

	data, err := p.doRequest(ctx, "POST", "/accounts", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			ID         string `json:"id"`
			Attributes struct {
				Name          string `json:"name"`
				RoutingNumber string `json:"routingNumber"`
				AccountNumber string `json:"accountNumber"`
				Balance       int64  `json:"balance"`
				Status        string `json:"status"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.BankAccount{
		ID:            resp.Data.ID,
		Provider:      "unit",
		ProviderID:    resp.Data.ID,
		OrgID:         orgID,
		AccountName:   resp.Data.Attributes.Name,
		AccountNumber: resp.Data.Attributes.AccountNumber,
		RoutingNumber: resp.Data.Attributes.RoutingNumber,
		Currency:      "USD",
		AccountType:   "checking",
		Status:        resp.Data.Attributes.Status,
		Balance: &types.Balance{
			Available: fmt.Sprintf("%d", resp.Data.Attributes.Balance),
			Currency:  "USD",
			UpdatedAt: time.Now(),
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (p *Provider) GetAccount(ctx context.Context, accountID string) (*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/accounts/"+accountID, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			ID         string `json:"id"`
			Attributes struct {
				Name    string `json:"name"`
				Balance int64  `json:"balance"`
				Status  string `json:"status"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.BankAccount{
		ID:          resp.Data.ID,
		Provider:    "unit",
		ProviderID:  resp.Data.ID,
		AccountName: resp.Data.Attributes.Name,
		Currency:    "USD",
		Status:      resp.Data.Attributes.Status,
		Balance: &types.Balance{
			Available: fmt.Sprintf("%d", resp.Data.Attributes.Balance),
			Currency:  "USD",
		},
	}, nil
}

func (p *Provider) ListAccounts(ctx context.Context, orgID string) ([]*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/accounts", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				Name    string `json:"name"`
				Balance int64  `json:"balance"`
				Status  string `json:"status"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	accounts := make([]*types.BankAccount, 0, len(resp.Data))
	for _, item := range resp.Data {
		accounts = append(accounts, &types.BankAccount{
			ID:          item.ID,
			Provider:    "unit",
			ProviderID:  item.ID,
			OrgID:       orgID,
			AccountName: item.Attributes.Name,
			Currency:    "USD",
			Status:      item.Attributes.Status,
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
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "achPayment",
			"attributes": map[string]interface{}{
				"amount":      req.Amount,
				"direction":   req.Direction,
				"description": req.Description,
			},
			"relationships": map[string]interface{}{
				"account": map[string]interface{}{
					"data": map[string]string{"type": "depositAccount", "id": req.SourceAccountID},
				},
			},
		},
	}

	data, err := p.doRequest(ctx, "POST", "/payments", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			ID         string `json:"id"`
			Attributes struct {
				Amount    int64  `json:"amount"`
				Direction string `json:"direction"`
				Status    string `json:"status"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Payment{
		ID:         resp.Data.ID,
		Provider:   "unit",
		ProviderID: resp.Data.ID,
		Type:       types.PaymentACH,
		Direction:  resp.Data.Attributes.Direction,
		Amount:     fmt.Sprintf("%d", resp.Data.Attributes.Amount),
		Currency:   "USD",
		Status:     types.PaymentStatus(resp.Data.Attributes.Status),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}, nil
}

func (p *Provider) GetPayment(ctx context.Context, paymentID string) (*types.Payment, error) {
	data, err := p.doRequest(ctx, "GET", "/payments/"+paymentID, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			ID         string `json:"id"`
			Attributes struct {
				Amount int64  `json:"amount"`
				Status string `json:"status"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Payment{
		ID:         resp.Data.ID,
		Provider:   "unit",
		ProviderID: resp.Data.ID,
		Amount:     fmt.Sprintf("%d", resp.Data.Attributes.Amount),
		Status:     types.PaymentStatus(resp.Data.Attributes.Status),
	}, nil
}

func (p *Provider) ListPayments(ctx context.Context, accountID string) ([]*types.Payment, error) {
	path := "/payments"
	if accountID != "" {
		path += "?filter[accountId]=" + accountID
	}
	data, err := p.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				Amount int64  `json:"amount"`
				Status string `json:"status"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	payments := make([]*types.Payment, 0, len(resp.Data))
	for _, item := range resp.Data {
		payments = append(payments, &types.Payment{
			ID:         item.ID,
			Provider:   "unit",
			ProviderID: item.ID,
			Amount:     fmt.Sprintf("%d", item.Attributes.Amount),
			Status:     types.PaymentStatus(item.Attributes.Status),
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

func (p *Provider) Capabilities() *types.ProviderCapability {
	return &types.ProviderCapability{
		Name:            "unit",
		PaymentTypes:    []string{"ach", "wire", "book", "card"},
		Currencies:      []string{"USD"},
		Features:        []string{"baas", "checking", "savings", "cards", "white_label", "kyc", "lending"},
		Countries:       []string{"US"},
		SettlementSpeed: "same_day",
		Status:          "active",
	}
}
