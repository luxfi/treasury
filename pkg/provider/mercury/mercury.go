package mercury

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

const ProdURL = "https://api.mercury.com"

type Config struct {
	BaseURL string
	APIKey  string // Bearer token
}

type Provider struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) provider.Provider {
	return &Provider{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (p *Provider) Name() string { return "mercury" }

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
		return nil, fmt.Errorf("mercury API error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func (p *Provider) CreateAccount(_ context.Context, _ string, _ *provider.CreateAccountRequest) (*types.BankAccount, error) {
	return nil, fmt.Errorf("mercury: accounts are created through the Mercury dashboard")
}

func (p *Provider) GetAccount(ctx context.Context, accountID string) (*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/api/v1/account/"+accountID, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		AccountNumber string `json:"accountNumber"`
		RoutingNumber string `json:"routingNumber"`
		Name          string `json:"name"`
		Status        string `json:"status"`
		Type          string `json:"type"`
		CurrentBalance float64 `json:"currentBalance"`
		AvailableBalance float64 `json:"availableBalance"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.BankAccount{
		ID:            accountID,
		Provider:      "mercury",
		ProviderID:    accountID,
		AccountName:   resp.Name,
		AccountNumber: resp.AccountNumber,
		RoutingNumber: resp.RoutingNumber,
		Currency:      "USD",
		AccountType:   resp.Type,
		Status:        resp.Status,
		Balance: &types.Balance{
			Available: fmt.Sprintf("%.2f", resp.AvailableBalance),
			Current:   fmt.Sprintf("%.2f", resp.CurrentBalance),
			Currency:  "USD",
			UpdatedAt: time.Now(),
		},
	}, nil
}

func (p *Provider) ListAccounts(ctx context.Context, orgID string) ([]*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/api/v1/accounts", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Accounts []struct {
			AccountNumber    string  `json:"accountNumber"`
			RoutingNumber    string  `json:"routingNumber"`
			Name             string  `json:"name"`
			Status           string  `json:"status"`
			Type             string  `json:"type"`
			CurrentBalance   float64 `json:"currentBalance"`
			AvailableBalance float64 `json:"availableBalance"`
			ID               string  `json:"id"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	accounts := make([]*types.BankAccount, 0, len(resp.Accounts))
	for _, a := range resp.Accounts {
		accounts = append(accounts, &types.BankAccount{
			ID:            a.ID,
			Provider:      "mercury",
			ProviderID:    a.ID,
			OrgID:         orgID,
			AccountName:   a.Name,
			AccountNumber: a.AccountNumber,
			RoutingNumber: a.RoutingNumber,
			Currency:      "USD",
			AccountType:   a.Type,
			Status:        a.Status,
			Balance: &types.Balance{
				Available: fmt.Sprintf("%.2f", a.AvailableBalance),
				Current:   fmt.Sprintf("%.2f", a.CurrentBalance),
				Currency:  "USD",
			},
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

func (p *Provider) CreatePayment(_ context.Context, _ *types.CreatePaymentRequest) (*types.Payment, error) {
	return nil, provider.ErrNotSupported // Mercury uses dashboard for transfers
}

func (p *Provider) GetPayment(ctx context.Context, paymentID string) (*types.Payment, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) ListPayments(ctx context.Context, accountID string) ([]*types.Payment, error) {
	path := "/api/v1/account/" + accountID + "/transactions"
	data, err := p.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Transactions []struct {
			ID          string  `json:"id"`
			Amount      float64 `json:"amount"`
			Status      string  `json:"status"`
			Kind        string  `json:"kind"`
			Description string  `json:"note"`
			CreatedAt   string  `json:"createdAt"`
		} `json:"transactions"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	payments := make([]*types.Payment, 0, len(resp.Transactions))
	for _, tx := range resp.Transactions {
		direction := "credit"
		if tx.Amount < 0 {
			direction = "debit"
		}
		payments = append(payments, &types.Payment{
			ID:          tx.ID,
			Provider:    "mercury",
			ProviderID:  tx.ID,
			Type:        types.PaymentACH,
			Direction:   direction,
			Amount:      fmt.Sprintf("%.2f", tx.Amount),
			Currency:    "USD",
			Status:      types.PaymentStatus(tx.Status),
			Description: tx.Description,
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
		Name:            "mercury",
		PaymentTypes:    []string{"ach", "wire"},
		Currencies:      []string{"USD"},
		Features:        []string{"checking", "savings", "treasury", "api_banking", "startup_banking"},
		Countries:       []string{"US"},
		SettlementSpeed: "same_day",
		Status:          "active",
	}
}
