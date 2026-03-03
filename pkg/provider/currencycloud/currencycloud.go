package currencycloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/luxfi/treasury/pkg/provider"
	"github.com/luxfi/treasury/pkg/types"
)

const (
	ProdURL = "https://api.currencycloud.com"
	DemoURL = "https://devapi.currencycloud.com"
)

type Config struct {
	BaseURL    string
	LoginID    string
	APIKey     string
	OnBehalfOf string // optional sub-account UUID
}

type Provider struct {
	cfg       Config
	client    *http.Client
	authToken string
	tokenExp  time.Time
	mu        sync.Mutex
}

func New(cfg Config) provider.Provider {
	return &Provider{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *Provider) Name() string { return "currencycloud" }

func (p *Provider) authenticate() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.authToken != "" && time.Now().Before(p.tokenExp) {
		return nil
	}

	form := url.Values{}
	form.Set("login_id", p.cfg.LoginID)
	form.Set("api_key", p.cfg.APIKey)

	resp, err := p.client.PostForm(p.cfg.BaseURL+"/v2/authenticate/api", form)
	if err != nil {
		return fmt.Errorf("auth failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		AuthToken string `json:"auth_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("auth decode failed: %w", err)
	}
	p.authToken = result.AuthToken
	p.tokenExp = time.Now().Add(25 * time.Minute) // tokens expire in 30 min
	return nil
}

func (p *Provider) doRequest(ctx context.Context, method, path string, body url.Values) ([]byte, error) {
	if err := p.authenticate(); err != nil {
		return nil, err
	}

	var reqBody io.Reader
	if body != nil {
		reqBody = strings.NewReader(body.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, p.cfg.BaseURL+path, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", p.authToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if p.cfg.OnBehalfOf != "" {
		req.Header.Set("X-On-Behalf-Of", p.cfg.OnBehalfOf)
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
		return nil, fmt.Errorf("currencycloud API error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

// --- Accounts ---

func (p *Provider) CreateAccount(ctx context.Context, orgID string, req *provider.CreateAccountRequest) (*types.BankAccount, error) {
	form := url.Values{}
	form.Set("account_name", req.AccountName)
	form.Set("legal_entity_type", "company")

	data, err := p.doRequest(ctx, "POST", "/v2/accounts/create", form)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID            string `json:"id"`
		AccountName   string `json:"account_name"`
		Status        string `json:"status"`
		CreatedAt     string `json:"created_at"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.BankAccount{
		ID:          resp.ID,
		Provider:    "currencycloud",
		ProviderID:  resp.ID,
		OrgID:       orgID,
		AccountName: resp.AccountName,
		Currency:    req.Currency,
		AccountType: "virtual",
		Status:      resp.Status,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}, nil
}

func (p *Provider) GetAccount(ctx context.Context, accountID string) (*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/v2/accounts/"+accountID, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID          string `json:"id"`
		AccountName string `json:"account_name"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.BankAccount{
		ID:          resp.ID,
		Provider:    "currencycloud",
		ProviderID:  resp.ID,
		AccountName: resp.AccountName,
		Status:      resp.Status,
	}, nil
}

func (p *Provider) ListAccounts(ctx context.Context, orgID string) ([]*types.BankAccount, error) {
	data, err := p.doRequest(ctx, "GET", "/v2/balances/find", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Balances []struct {
			ID       string `json:"id"`
			Currency string `json:"currency"`
			Amount   string `json:"amount"`
		} `json:"balances"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	accounts := make([]*types.BankAccount, 0, len(resp.Balances))
	for _, b := range resp.Balances {
		accounts = append(accounts, &types.BankAccount{
			ID:          b.ID,
			Provider:    "currencycloud",
			ProviderID:  b.ID,
			OrgID:       orgID,
			AccountName: b.Currency + " Balance",
			Currency:    b.Currency,
			AccountType: "virtual",
			Status:      "active",
			Balance:     &types.Balance{Available: b.Amount, Current: b.Amount, Currency: b.Currency},
		})
	}
	return accounts, nil
}

func (p *Provider) GetBalance(ctx context.Context, accountID string) (*types.Balance, error) {
	data, err := p.doRequest(ctx, "GET", "/v2/balances/"+accountID, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Currency string `json:"currency"`
		Amount   string `json:"amount"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Balance{
		Available: resp.Amount,
		Current:   resp.Amount,
		Currency:  resp.Currency,
		UpdatedAt: time.Now(),
	}, nil
}

// --- Payments ---

func (p *Provider) CreatePayment(ctx context.Context, req *types.CreatePaymentRequest) (*types.Payment, error) {
	form := url.Values{}
	form.Set("currency", req.Currency)
	form.Set("amount", req.Amount)
	form.Set("reason", req.Description)
	form.Set("reference", req.Reference)
	if req.Counterparty != nil {
		form.Set("beneficiary_id", req.Counterparty.ID)
	}
	form.Set("payment_type", "regular")

	data, err := p.doRequest(ctx, "POST", "/v2/payments/create", form)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID        string `json:"id"`
		Amount    string `json:"amount"`
		Currency  string `json:"currency"`
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Payment{
		ID:         resp.ID,
		Provider:   "currencycloud",
		ProviderID: resp.ID,
		Type:       types.PaymentSWIFT,
		Direction:  req.Direction,
		Amount:     resp.Amount,
		Currency:   resp.Currency,
		Status:     types.PaymentStatus(resp.Status),
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
		ID       string `json:"id"`
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.Payment{
		ID:         resp.ID,
		Provider:   "currencycloud",
		ProviderID: resp.ID,
		Amount:     resp.Amount,
		Currency:   resp.Currency,
		Status:     types.PaymentStatus(resp.Status),
	}, nil
}

func (p *Provider) ListPayments(ctx context.Context, accountID string) ([]*types.Payment, error) {
	data, err := p.doRequest(ctx, "GET", "/v2/payments/find", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Payments []struct {
			ID       string `json:"id"`
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
			Status   string `json:"status"`
		} `json:"payments"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	payments := make([]*types.Payment, 0, len(resp.Payments))
	for _, pm := range resp.Payments {
		payments = append(payments, &types.Payment{
			ID:         pm.ID,
			Provider:   "currencycloud",
			ProviderID: pm.ID,
			Amount:     pm.Amount,
			Currency:   pm.Currency,
			Status:     types.PaymentStatus(pm.Status),
		})
	}
	return payments, nil
}

func (p *Provider) CancelPayment(ctx context.Context, paymentID string) error {
	_, err := p.doRequest(ctx, "POST", "/v2/payments/"+paymentID+"/delete", nil)
	return err
}

// --- FX ---

func (p *Provider) GetFXQuote(ctx context.Context, sellCcy, buyCcy, amount, fixedSide string) (*types.FXQuote, error) {
	params := url.Values{}
	params.Set("sell_currency", sellCcy)
	params.Set("buy_currency", buyCcy)
	params.Set("amount", amount)
	params.Set("fixed_side", fixedSide)

	data, err := p.doRequest(ctx, "GET", "/v2/rates/detailed?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		SellCurrency   string `json:"settlement_cut_off_time"`
		BuyCurrency    string `json:"buy_currency"`
		ClientSellAmt  string `json:"client_sell_amount"`
		ClientBuyAmt   string `json:"client_buy_amount"`
		ClientRate     string `json:"client_rate"`
		CoreRate       string `json:"core_rate"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.FXQuote{
		Provider:     "currencycloud",
		SellCurrency: sellCcy,
		BuyCurrency:  buyCcy,
		SellAmount:   resp.ClientSellAmt,
		BuyAmount:    resp.ClientBuyAmt,
		Rate:         resp.ClientRate,
		FixedSide:    fixedSide,
		ExpiresAt:    time.Now().Add(30 * time.Second),
		CreatedAt:    time.Now(),
	}, nil
}

func (p *Provider) CreateFXConversion(ctx context.Context, req *types.CreateFXConversionRequest) (*types.FXConversion, error) {
	form := url.Values{}
	form.Set("sell_currency", req.SellCurrency)
	form.Set("buy_currency", req.BuyCurrency)
	form.Set("amount", req.Amount)
	form.Set("fixed_side", req.FixedSide)
	form.Set("term_agreement", "true")

	data, err := p.doRequest(ctx, "POST", "/v2/conversions/create", form)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ID             string `json:"id"`
		SellCurrency   string `json:"sell_currency"`
		BuyCurrency    string `json:"buy_currency"`
		SellAmount     string `json:"client_sell_amount"`
		BuyAmount      string `json:"client_buy_amount"`
		Rate           string `json:"client_rate"`
		SettlementDate string `json:"settlement_date"`
		Status         string `json:"status"`
		CreatedAt      string `json:"created_at"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &types.FXConversion{
		ID:             resp.ID,
		Provider:       "currencycloud",
		ProviderID:     resp.ID,
		SellCurrency:   resp.SellCurrency,
		BuyCurrency:    resp.BuyCurrency,
		SellAmount:     resp.SellAmount,
		BuyAmount:      resp.BuyAmount,
		Rate:           resp.Rate,
		SettlementDate: resp.SettlementDate,
		Status:         resp.Status,
		CreatedAt:      time.Now(),
	}, nil
}

func (p *Provider) ListFXConversions(ctx context.Context) ([]*types.FXConversion, error) {
	data, err := p.doRequest(ctx, "GET", "/v2/conversions/find", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Conversions []struct {
			ID           string `json:"id"`
			SellCurrency string `json:"sell_currency"`
			BuyCurrency  string `json:"buy_currency"`
			SellAmount   string `json:"client_sell_amount"`
			BuyAmount    string `json:"client_buy_amount"`
			Rate         string `json:"client_rate"`
			Status       string `json:"status"`
		} `json:"conversions"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	conversions := make([]*types.FXConversion, 0, len(resp.Conversions))
	for _, c := range resp.Conversions {
		conversions = append(conversions, &types.FXConversion{
			ID:           c.ID,
			Provider:     "currencycloud",
			ProviderID:   c.ID,
			SellCurrency: c.SellCurrency,
			BuyCurrency:  c.BuyCurrency,
			SellAmount:   c.SellAmount,
			BuyAmount:    c.BuyAmount,
			Rate:         c.Rate,
			Status:       c.Status,
		})
	}
	return conversions, nil
}

// --- Counterparties ---

func (p *Provider) CreateCounterparty(ctx context.Context, cp *types.Counterparty) (*types.Counterparty, error) {
	form := url.Values{}
	form.Set("name", cp.Name)
	form.Set("bank_account_holder_name", cp.Name)
	if cp.IBAN != "" {
		form.Set("iban", cp.IBAN)
	}
	if cp.AccountNumber != "" {
		form.Set("account_number", cp.AccountNumber)
	}
	if cp.RoutingNumber != "" {
		form.Set("routing_code_value_1", cp.RoutingNumber)
		form.Set("routing_code_type_1", "aba")
	}
	if cp.BankCountry != "" {
		form.Set("bank_country", cp.BankCountry)
	}
	form.Set("currency", "USD")

	data, err := p.doRequest(ctx, "POST", "/v2/beneficiaries/create", form)
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
	data, err := p.doRequest(ctx, "GET", "/v2/beneficiaries/find", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Beneficiaries []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"beneficiaries"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	cps := make([]*types.Counterparty, 0, len(resp.Beneficiaries))
	for _, b := range resp.Beneficiaries {
		cps = append(cps, &types.Counterparty{ID: b.ID, Name: b.Name})
	}
	return cps, nil
}

func (p *Provider) Capabilities() *types.ProviderCapability {
	return &types.ProviderCapability{
		Name:            "currencycloud",
		PaymentTypes:    []string{"swift", "sepa", "wire"},
		Currencies:      []string{"USD", "EUR", "GBP", "JPY", "CHF", "AUD", "CAD", "NZD", "HKD", "SGD", "SEK", "NOK", "DKK", "PLN", "CZK", "HUF", "ZAR", "MXN", "BRL", "INR", "CNY", "THB", "KRW", "TWD", "ILS", "TRY", "AED", "SAR", "PHP", "IDR", "MYR", "KES", "NGN"},
		Features:        []string{"fx", "multi_currency", "payments", "beneficiaries", "35+_currencies"},
		Countries:       []string{"US", "GB", "EU", "AU", "CA", "JP", "SG", "HK"},
		SettlementSpeed: "t+1",
		Status:          "active",
	}
}
