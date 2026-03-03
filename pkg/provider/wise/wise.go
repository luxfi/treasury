package wise

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
	ProdURL    = "https://api.transferwise.com"
	SandboxURL = "https://api.sandbox.transferwise.tech"
)

type Config struct {
	BaseURL   string
	APIToken  string
	ProfileID string
}

type Provider struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) provider.Provider {
	return &Provider{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (p *Provider) Name() string { return "wise" }

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
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIToken)
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
		return nil, fmt.Errorf("wise API error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func (p *Provider) CreateAccount(_ context.Context, _ string, _ *provider.CreateAccountRequest) (*types.BankAccount, error) {
	return nil, fmt.Errorf("wise: accounts are created through the Wise dashboard")
}

func (p *Provider) GetAccount(ctx context.Context, accountID string) (*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/v4/profiles/"+p.cfg.ProfileID+"/balances?types=STANDARD", nil)
	if err != nil {
		return nil, err
	}

	var balances []struct {
		ID       int64 `json:"id"`
		Currency string `json:"currency"`
		Amount   struct {
			Value    float64 `json:"value"`
			Currency string  `json:"currency"`
		} `json:"amount"`
	}
	if err := json.Unmarshal(data, &balances); err != nil {
		return nil, err
	}

	for _, b := range balances {
		if fmt.Sprintf("%d", b.ID) == accountID {
			return &types.BankAccount{
				ID:          fmt.Sprintf("%d", b.ID),
				Provider:    "wise",
				ProviderID:  fmt.Sprintf("%d", b.ID),
				AccountName: b.Currency + " Balance",
				Currency:    b.Currency,
				AccountType: "multi_currency",
				Status:      "active",
				Balance: &types.Balance{
					Available: fmt.Sprintf("%.2f", b.Amount.Value),
					Current:   fmt.Sprintf("%.2f", b.Amount.Value),
					Currency:  b.Currency,
					UpdatedAt: time.Now(),
				},
			}, nil
		}
	}
	return nil, fmt.Errorf("balance %s not found", accountID)
}

func (p *Provider) ListAccounts(ctx context.Context, orgID string) ([]*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/v4/profiles/"+p.cfg.ProfileID+"/balances?types=STANDARD", nil)
	if err != nil {
		return nil, err
	}

	var balances []struct {
		ID       int64  `json:"id"`
		Currency string `json:"currency"`
		Amount   struct {
			Value    float64 `json:"value"`
			Currency string  `json:"currency"`
		} `json:"amount"`
	}
	if err := json.Unmarshal(data, &balances); err != nil {
		return nil, err
	}

	accounts := make([]*types.BankAccount, 0, len(balances))
	for _, b := range balances {
		accounts = append(accounts, &types.BankAccount{
			ID:          fmt.Sprintf("%d", b.ID),
			Provider:    "wise",
			ProviderID:  fmt.Sprintf("%d", b.ID),
			OrgID:       orgID,
			AccountName: b.Currency + " Balance",
			Currency:    b.Currency,
			AccountType: "multi_currency",
			Status:      "active",
			Balance: &types.Balance{
				Available: fmt.Sprintf("%.2f", b.Amount.Value),
				Current:   fmt.Sprintf("%.2f", b.Amount.Value),
				Currency:  b.Currency,
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

func (p *Provider) CreatePayment(ctx context.Context, req *types.CreatePaymentRequest) (*types.Payment, error) {
	// Step 1: Create quote
	quoteBody := map[string]interface{}{
		"sourceCurrency": req.Currency,
		"targetCurrency": req.Currency,
		"sourceAmount":   req.Amount,
		"profile":        p.cfg.ProfileID,
	}

	quoteData, err := p.doRequest(ctx, "POST", "/v3/profiles/"+p.cfg.ProfileID+"/quotes", quoteBody)
	if err != nil {
		return nil, fmt.Errorf("quote creation failed: %w", err)
	}

	var quote struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(quoteData, &quote); err != nil {
		return nil, err
	}

	// Step 2: Create transfer
	transferBody := map[string]interface{}{
		"targetAccount": req.DestAccountID,
		"quoteUuid":     quote.ID,
		"details": map[string]string{
			"reference": req.Reference,
		},
	}

	data, err := p.doRequest(ctx, "POST", "/v1/transfers", transferBody)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID              int64  `json:"id"`
		Status          string `json:"status"`
		SourceCurrency  string `json:"sourceCurrency"`
		SourceValue     float64 `json:"sourceValue"`
		TargetCurrency  string `json:"targetCurrency"`
		TargetValue     float64 `json:"targetValue"`
		Rate            float64 `json:"rate"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Payment{
		ID:         fmt.Sprintf("%d", resp.ID),
		Provider:   "wise",
		ProviderID: fmt.Sprintf("%d", resp.ID),
		Type:       types.PaymentSWIFT,
		Direction:  req.Direction,
		Amount:     fmt.Sprintf("%.2f", resp.SourceValue),
		Currency:   resp.SourceCurrency,
		Status:     types.PaymentStatus(resp.Status),
		ExchangeRate: fmt.Sprintf("%.6f", resp.Rate),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}, nil
}

func (p *Provider) GetPayment(ctx context.Context, paymentID string) (*types.Payment, error) {
	data, err := p.doRequest(ctx, "GET", "/v1/transfers/"+paymentID, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Payment{
		ID:         fmt.Sprintf("%d", resp.ID),
		Provider:   "wise",
		ProviderID: fmt.Sprintf("%d", resp.ID),
		Status:     types.PaymentStatus(resp.Status),
	}, nil
}

func (p *Provider) ListPayments(ctx context.Context, accountID string) ([]*types.Payment, error) {
	data, err := p.doRequest(ctx, "GET", "/v1/transfers?profile="+p.cfg.ProfileID, nil)
	if err != nil {
		return nil, err
	}

	var items []struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
		Source string `json:"sourceCurrency"`
		Amount float64 `json:"sourceValue"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}

	payments := make([]*types.Payment, 0, len(items))
	for _, item := range items {
		payments = append(payments, &types.Payment{
			ID:         fmt.Sprintf("%d", item.ID),
			Provider:   "wise",
			ProviderID: fmt.Sprintf("%d", item.ID),
			Amount:     fmt.Sprintf("%.2f", item.Amount),
			Currency:   item.Source,
			Status:     types.PaymentStatus(item.Status),
		})
	}
	return payments, nil
}

func (p *Provider) CancelPayment(ctx context.Context, paymentID string) error {
	_, err := p.doRequest(ctx, "PUT", "/v1/transfers/"+paymentID+"/cancel", nil)
	return err
}

func (p *Provider) GetFXQuote(ctx context.Context, sellCcy, buyCcy, amount, fixedSide string) (*types.FXQuote, error) {
	body := map[string]interface{}{
		"sourceCurrency": sellCcy,
		"targetCurrency": buyCcy,
		"sourceAmount":   amount,
		"profile":        p.cfg.ProfileID,
	}

	data, err := p.doRequest(ctx, "POST", "/v3/profiles/"+p.cfg.ProfileID+"/quotes", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID           string  `json:"id"`
		Rate         float64 `json:"rate"`
		SourceAmount float64 `json:"sourceAmount"`
		TargetAmount float64 `json:"targetAmount"`
		ExpirationTime string `json:"expirationTime"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	exp, _ := time.Parse(time.RFC3339, resp.ExpirationTime)
	return &types.FXQuote{
		ID:           resp.ID,
		Provider:     "wise",
		SellCurrency: sellCcy,
		BuyCurrency:  buyCcy,
		SellAmount:   fmt.Sprintf("%.2f", resp.SourceAmount),
		BuyAmount:    fmt.Sprintf("%.2f", resp.TargetAmount),
		Rate:         fmt.Sprintf("%.6f", resp.Rate),
		FixedSide:    fixedSide,
		ExpiresAt:    exp,
		CreatedAt:    time.Now(),
	}, nil
}

func (p *Provider) CreateFXConversion(ctx context.Context, req *types.CreateFXConversionRequest) (*types.FXConversion, error) {
	quote, err := p.GetFXQuote(ctx, req.SellCurrency, req.BuyCurrency, req.Amount, req.FixedSide)
	if err != nil {
		return nil, err
	}

	return &types.FXConversion{
		Provider:     "wise",
		QuoteID:      quote.ID,
		SellCurrency: req.SellCurrency,
		BuyCurrency:  req.BuyCurrency,
		SellAmount:   quote.SellAmount,
		BuyAmount:    quote.BuyAmount,
		Rate:         quote.Rate,
		Status:       "quoted",
		CreatedAt:    time.Now(),
	}, nil
}

func (p *Provider) ListFXConversions(_ context.Context) ([]*types.FXConversion, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) CreateCounterparty(ctx context.Context, cp *types.Counterparty) (*types.Counterparty, error) {
	body := map[string]interface{}{
		"profile":     p.cfg.ProfileID,
		"accountHolderName": cp.Name,
		"currency":    "USD",
		"type":        "sort_code",
	}
	if cp.IBAN != "" {
		body["type"] = "iban"
		body["details"] = map[string]string{"IBAN": cp.IBAN}
	} else if cp.AccountNumber != "" {
		body["type"] = "aba"
		body["details"] = map[string]string{
			"abartn":        cp.RoutingNumber,
			"accountNumber": cp.AccountNumber,
			"accountType":   "CHECKING",
		}
	}

	data, err := p.doRequest(ctx, "POST", "/v1/accounts", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	cp.ID = fmt.Sprintf("%d", resp.ID)
	return cp, nil
}

func (p *Provider) ListCounterparties(ctx context.Context) ([]*types.Counterparty, error) {
	data, err := p.doRequest(ctx, "GET", "/v1/accounts?profile="+p.cfg.ProfileID, nil)
	if err != nil {
		return nil, err
	}

	var items []struct {
		ID                int64  `json:"id"`
		AccountHolderName string `json:"accountHolderName"`
		Currency          string `json:"currency"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}

	cps := make([]*types.Counterparty, 0, len(items))
	for _, item := range items {
		cps = append(cps, &types.Counterparty{
			ID:   fmt.Sprintf("%d", item.ID),
			Name: item.AccountHolderName,
		})
	}
	return cps, nil
}

func (p *Provider) Capabilities() *types.ProviderCapability {
	return &types.ProviderCapability{
		Name:            "wise",
		PaymentTypes:    []string{"swift", "sepa", "wire"},
		Currencies:      []string{"USD", "EUR", "GBP", "JPY", "CHF", "AUD", "CAD", "NZD", "HKD", "SGD", "SEK", "NOK", "DKK", "PLN", "CZK", "HUF", "ZAR", "MXN", "BRL", "INR", "CNY", "THB", "KRW", "TWD", "ILS", "TRY", "AED", "SAR", "PHP", "IDR", "MYR"},
		Features:        []string{"fx", "multi_currency", "international", "60+_currencies", "cheap_transfers"},
		Countries:       []string{"US", "GB", "EU", "AU", "CA", "JP", "SG", "HK", "BR", "IN"},
		SettlementSpeed: "same_day",
		Status:          "active",
	}
}
