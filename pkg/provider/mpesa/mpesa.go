package mpesa

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/luxfi/treasury/pkg/provider"
	"github.com/luxfi/treasury/pkg/types"
)

const (
	ProdURL    = "https://api.safaricom.co.ke"
	SandboxURL = "https://sandbox.safaricom.co.ke"
)

type Config struct {
	BaseURL           string
	ConsumerKey       string
	ConsumerSecret    string
	Shortcode         string
	InitiatorName     string
	InitiatorPassword string
	Passkey           string
	CallbackURL       string
	QueueTimeoutURL   string
	ResultURL         string
	Environment       string // "production" or "sandbox"
	CertPEM           []byte // optional; if nil, loaded from MPESA_CERT_PATH or embedded default
}

type Provider struct {
	cfg    Config
	client *http.Client

	tokenMu  sync.Mutex
	token    string
	tokenExp time.Time

	certMu sync.Mutex
	cert   *rsa.PublicKey
}

func New(cfg Config) provider.Provider {
	return &Provider{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (p *Provider) Name() string { return "mpesa" }

// --- OAuth ---

func (p *Provider) accessToken(ctx context.Context) (string, error) {
	p.tokenMu.Lock()
	defer p.tokenMu.Unlock()

	if p.token != "" && time.Now().Before(p.tokenExp.Add(-60*time.Second)) {
		return p.token, nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", p.cfg.BaseURL+"/oauth/v1/generate?grant_type=client_credentials", nil)
	if err != nil {
		return "", err
	}
	basic := base64.StdEncoding.EncodeToString([]byte(p.cfg.ConsumerKey + ":" + p.cfg.ConsumerSecret))
	req.Header.Set("Authorization", "Basic "+basic)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("mpesa oauth error %d: %s", resp.StatusCode, string(data))
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   string `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &tok); err != nil {
		return "", err
	}
	secs, _ := strconv.Atoi(tok.ExpiresIn)
	if secs == 0 {
		secs = 3599
	}
	p.token = tok.AccessToken
	p.tokenExp = time.Now().Add(time.Duration(secs) * time.Second)
	return p.token, nil
}

func (p *Provider) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	tok, err := p.accessToken(ctx)
	if err != nil {
		return nil, err
	}

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
	req.Header.Set("Authorization", "Bearer "+tok)
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
		return nil, fmt.Errorf("mpesa API error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

// --- Security credential (initiator password RSA-OAEP under Daraja cert) ---

func (p *Provider) publicKey() (*rsa.PublicKey, error) {
	p.certMu.Lock()
	defer p.certMu.Unlock()
	if p.cert != nil {
		return p.cert, nil
	}

	pemBytes := p.cfg.CertPEM
	if len(pemBytes) == 0 {
		if path := os.Getenv("MPESA_CERT_PATH"); path != "" {
			b, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("mpesa cert read: %w", err)
			}
			pemBytes = b
		}
	}
	if len(pemBytes) == 0 {
		return nil, fmt.Errorf("mpesa: no certificate configured (set Config.CertPEM or MPESA_CERT_PATH)")
	}

	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("mpesa: invalid PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("mpesa cert parse: %w", err)
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("mpesa: certificate public key is not RSA")
	}
	p.cert = pub
	return pub, nil
}

func (p *Provider) securityCredential() (string, error) {
	pub, err := p.publicKey()
	if err != nil {
		return "", err
	}
	enc, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, pub, []byte(p.cfg.InitiatorPassword), nil)
	if err != nil {
		return "", fmt.Errorf("mpesa: encrypt initiator password: %w", err)
	}
	return base64.StdEncoding.EncodeToString(enc), nil
}

// --- msisdn normalization ---

var digitsOnly = regexp.MustCompile(`\D+`)

// normalizeMSISDN converts Kenyan inputs (07XXXXXXXX, +2547XXXXXXXX,
// 2547XXXXXXXX, 7XXXXXXXX) into Daraja's canonical 2547XXXXXXXX form.
// Returns an error for anything that isn't a Kenyan mobile number.
func normalizeMSISDN(in string) (string, error) {
	d := digitsOnly.ReplaceAllString(in, "")
	switch {
	case strings.HasPrefix(d, "254") && len(d) == 12:
		// already canonical
	case strings.HasPrefix(d, "0") && len(d) == 10:
		d = "254" + d[1:]
	case len(d) == 9 && (strings.HasPrefix(d, "7") || strings.HasPrefix(d, "1")):
		d = "254" + d
	default:
		return "", fmt.Errorf("mpesa: not a Kenyan msisdn: %q", in)
	}
	// must be Safaricom-routable mobile (Safaricom/Airtel/Telkom prefixes start 7 or 1)
	if d[3] != '7' && d[3] != '1' {
		return "", fmt.Errorf("mpesa: not a mobile msisdn: %q", in)
	}
	return d, nil
}

// --- Accounts ---

func (p *Provider) CreateAccount(_ context.Context, _ string, _ *provider.CreateAccountRequest) (*types.BankAccount, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) GetAccount(ctx context.Context, accountID string) (*types.BankAccount, error) {
	if accountID != p.cfg.Shortcode && accountID != "self" {
		return nil, fmt.Errorf("mpesa: unknown account %q (only configured shortcode is supported)", accountID)
	}
	bal, err := p.GetBalance(ctx, p.cfg.Shortcode)
	if err != nil {
		// balance lookup is async via callback; surface the account regardless
		bal = nil
	}
	return &types.BankAccount{
		ID:          p.cfg.Shortcode,
		Provider:    "mpesa",
		ProviderID:  p.cfg.Shortcode,
		AccountName: "M-Pesa " + p.cfg.Shortcode,
		Currency:    "KES",
		AccountType: "mobile_money",
		BankCountry: "KE",
		Status:      "active",
		Balance:     bal,
	}, nil
}

func (p *Provider) ListAccounts(ctx context.Context, orgID string) ([]*types.BankAccount, error) {
	acct, err := p.GetAccount(ctx, p.cfg.Shortcode)
	if err != nil {
		return nil, err
	}
	acct.OrgID = orgID
	return []*types.BankAccount{acct}, nil
}

func (p *Provider) GetBalance(ctx context.Context, accountID string) (*types.Balance, error) {
	cred, err := p.securityCredential()
	if err != nil {
		return nil, err
	}
	body := map[string]interface{}{
		"Initiator":          p.cfg.InitiatorName,
		"SecurityCredential": cred,
		"CommandID":          "AccountBalance",
		"PartyA":             p.cfg.Shortcode,
		"IdentifierType":     "4", // organization shortcode
		"Remarks":            "balance",
		"QueueTimeOutURL":    p.cfg.QueueTimeoutURL,
		"ResultURL":          p.cfg.ResultURL,
	}
	if _, err := p.doRequest(ctx, "POST", "/mpesa/accountbalance/v1/query", body); err != nil {
		return nil, err
	}
	// Daraja returns the actual balance asynchronously to ResultURL; ack only.
	return &types.Balance{
		Currency:  "KES",
		Available: "",
		Current:   "",
		UpdatedAt: time.Now(),
	}, nil
}

// --- Payments ---

func (p *Provider) CreatePayment(ctx context.Context, req *types.CreatePaymentRequest) (*types.Payment, error) {
	if req.Currency != "" && strings.ToUpper(req.Currency) != "KES" {
		return nil, fmt.Errorf("mpesa: only KES supported, got %q", req.Currency)
	}
	// M-Pesa uses integer KES.
	amt, err := strconv.ParseFloat(req.Amount, 64)
	if err != nil {
		return nil, fmt.Errorf("mpesa: invalid amount %q: %w", req.Amount, err)
	}
	amtInt := int64(amt)
	if float64(amtInt) != amt {
		return nil, fmt.Errorf("mpesa: KES amounts must be integer, got %q", req.Amount)
	}

	switch strings.ToLower(req.Direction) {
	case "credit", "out", "payout", "debit_us":
		return p.createB2C(ctx, req, amtInt)
	case "debit", "in", "collect", "credit_us":
		return p.createSTKPush(ctx, req, amtInt)
	default:
		return nil, fmt.Errorf("mpesa: unknown direction %q (use credit/debit)", req.Direction)
	}
}

func (p *Provider) createB2C(ctx context.Context, req *types.CreatePaymentRequest, amount int64) (*types.Payment, error) {
	if req.Counterparty == nil || req.Counterparty.AccountNumber == "" {
		return nil, fmt.Errorf("mpesa b2c: counterparty msisdn required (in AccountNumber)")
	}
	msisdn, err := normalizeMSISDN(req.Counterparty.AccountNumber)
	if err != nil {
		return nil, err
	}
	cred, err := p.securityCredential()
	if err != nil {
		return nil, err
	}
	body := map[string]interface{}{
		"OriginatorConversationID": req.Reference,
		"InitiatorName":            p.cfg.InitiatorName,
		"SecurityCredential":       cred,
		"CommandID":                "BusinessPayment",
		"Amount":                   amount,
		"PartyA":                   p.cfg.Shortcode,
		"PartyB":                   msisdn,
		"Remarks":                  firstNonEmpty(req.Description, req.Reference, "payout"),
		"QueueTimeOutURL":          p.cfg.QueueTimeoutURL,
		"ResultURL":                p.cfg.ResultURL,
		"Occasion":                 req.Reference,
	}
	data, err := p.doRequest(ctx, "POST", "/mpesa/b2c/v3/paymentrequest", body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		ConversationID           string `json:"ConversationID"`
		OriginatorConversationID string `json:"OriginatorConversationID"`
		ResponseCode             string `json:"ResponseCode"`
		ResponseDescription      string `json:"ResponseDescription"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if resp.ResponseCode != "" && resp.ResponseCode != "0" {
		return nil, fmt.Errorf("mpesa b2c rejected: %s %s", resp.ResponseCode, resp.ResponseDescription)
	}
	return &types.Payment{
		ID:           resp.OriginatorConversationID,
		Provider:     "mpesa",
		ProviderID:   resp.ConversationID,
		Type:         types.PaymentRTP,
		Direction:    req.Direction,
		Amount:       strconv.FormatInt(amount, 10),
		Currency:     "KES",
		Status:       types.PaymentPending,
		Counterparty: req.Counterparty,
		Reference:    req.Reference,
		Description:  req.Description,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		Meta: map[string]string{
			"conversation_id":            resp.ConversationID,
			"originator_conversation_id": resp.OriginatorConversationID,
			"msisdn":                     msisdn,
			"rail":                       "mpesa_b2c",
		},
	}, nil
}

func (p *Provider) createSTKPush(ctx context.Context, req *types.CreatePaymentRequest, amount int64) (*types.Payment, error) {
	if req.Counterparty == nil || req.Counterparty.AccountNumber == "" {
		return nil, fmt.Errorf("mpesa stk: payer msisdn required (in counterparty.AccountNumber)")
	}
	msisdn, err := normalizeMSISDN(req.Counterparty.AccountNumber)
	if err != nil {
		return nil, err
	}
	ts := time.Now().Format("20060102150405")
	password := base64.StdEncoding.EncodeToString([]byte(p.cfg.Shortcode + p.cfg.Passkey + ts))

	txType := "CustomerPayBillOnline"
	if t, ok := req.Meta["till"]; ok && t == "true" {
		txType = "CustomerBuyGoodsOnline"
	}

	body := map[string]interface{}{
		"BusinessShortCode": p.cfg.Shortcode,
		"Password":          password,
		"Timestamp":         ts,
		"TransactionType":   txType,
		"Amount":            amount,
		"PartyA":            msisdn,
		"PartyB":            p.cfg.Shortcode,
		"PhoneNumber":       msisdn,
		"CallBackURL":       p.cfg.CallbackURL,
		"AccountReference":  firstNonEmpty(req.Reference, p.cfg.Shortcode),
		"TransactionDesc":   firstNonEmpty(req.Description, req.Reference, "collect"),
	}
	data, err := p.doRequest(ctx, "POST", "/mpesa/stkpush/v1/processrequest", body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		MerchantRequestID   string `json:"MerchantRequestID"`
		CheckoutRequestID   string `json:"CheckoutRequestID"`
		ResponseCode        string `json:"ResponseCode"`
		ResponseDescription string `json:"ResponseDescription"`
		CustomerMessage     string `json:"CustomerMessage"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if resp.ResponseCode != "" && resp.ResponseCode != "0" {
		return nil, fmt.Errorf("mpesa stk rejected: %s %s", resp.ResponseCode, resp.ResponseDescription)
	}
	return &types.Payment{
		ID:           resp.CheckoutRequestID,
		Provider:     "mpesa",
		ProviderID:   resp.CheckoutRequestID,
		Type:         types.PaymentRTP,
		Direction:    req.Direction,
		Amount:       strconv.FormatInt(amount, 10),
		Currency:     "KES",
		Status:       types.PaymentPending,
		Counterparty: req.Counterparty,
		Reference:    req.Reference,
		Description:  req.Description,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		Meta: map[string]string{
			"merchant_request_id": resp.MerchantRequestID,
			"checkout_request_id": resp.CheckoutRequestID,
			"msisdn":              msisdn,
			"rail":                "mpesa_c2b_stk",
		},
	}, nil
}

func (p *Provider) GetPayment(ctx context.Context, paymentID string) (*types.Payment, error) {
	cred, err := p.securityCredential()
	if err != nil {
		return nil, err
	}
	body := map[string]interface{}{
		"Initiator":                p.cfg.InitiatorName,
		"SecurityCredential":       cred,
		"CommandID":                "TransactionStatusQuery",
		"TransactionID":            paymentID,
		"OriginatorConversationID": paymentID,
		"PartyA":                   p.cfg.Shortcode,
		"IdentifierType":           "4",
		"ResultURL":                p.cfg.ResultURL,
		"QueueTimeOutURL":          p.cfg.QueueTimeoutURL,
		"Remarks":                  "status",
		"Occasion":                 "status",
	}
	data, err := p.doRequest(ctx, "POST", "/mpesa/transactionstatus/v1/query", body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		OriginatorConversationID string `json:"OriginatorConversationID"`
		ConversationID           string `json:"ConversationID"`
		ResponseCode             string `json:"ResponseCode"`
		ResponseDescription      string `json:"ResponseDescription"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	status := types.PaymentProcessing
	if resp.ResponseCode != "" && resp.ResponseCode != "0" {
		status = types.PaymentFailed
	}
	return &types.Payment{
		ID:         paymentID,
		Provider:   "mpesa",
		ProviderID: resp.ConversationID,
		Currency:   "KES",
		Status:     status,
		Meta: map[string]string{
			"response_code":        resp.ResponseCode,
			"response_description": resp.ResponseDescription,
		},
	}, nil
}

func (p *Provider) ListPayments(_ context.Context, _ string) ([]*types.Payment, error) {
	// Daraja has no list endpoint; payments are reconciled via callbacks
	// into the local ledger by treasuryd.
	return nil, provider.ErrNotSupported
}

// CancelPayment is unsupported by the standard interface — Daraja reversals
// are privileged. Use Reverse(ctx, txnID, amount) when permitted.
func (p *Provider) CancelPayment(_ context.Context, _ string) error {
	return provider.ErrNotSupported
}

// Reverse issues a Daraja transaction reversal. This is a privileged
// operation and is exposed outside the Provider interface intentionally.
func (p *Provider) Reverse(ctx context.Context, txnID, amount, remarks string) error {
	cred, err := p.securityCredential()
	if err != nil {
		return err
	}
	amt, err := strconv.ParseInt(amount, 10, 64)
	if err != nil {
		return fmt.Errorf("mpesa reverse: amount %q: %w", amount, err)
	}
	body := map[string]interface{}{
		"Initiator":              p.cfg.InitiatorName,
		"SecurityCredential":     cred,
		"CommandID":              "TransactionReversal",
		"TransactionID":          txnID,
		"Amount":                 amt,
		"ReceiverParty":          p.cfg.Shortcode,
		"RecieverIdentifierType": "11",
		"ResultURL":              p.cfg.ResultURL,
		"QueueTimeOutURL":        p.cfg.QueueTimeoutURL,
		"Remarks":                firstNonEmpty(remarks, "reversal"),
		"Occasion":               firstNonEmpty(remarks, "reversal"),
	}
	_, err = p.doRequest(ctx, "POST", "/mpesa/reversal/v1/request", body)
	return err
}

// --- FX (unsupported) ---

func (p *Provider) GetFXQuote(_ context.Context, _, _, _, _ string) (*types.FXQuote, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) CreateFXConversion(_ context.Context, _ *types.CreateFXConversionRequest) (*types.FXConversion, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) ListFXConversions(_ context.Context) ([]*types.FXConversion, error) {
	return nil, provider.ErrNotSupported
}

// --- Counterparties (unsupported — addressed by msisdn at send time) ---

func (p *Provider) CreateCounterparty(_ context.Context, _ *types.Counterparty) (*types.Counterparty, error) {
	return nil, provider.ErrNotSupported
}

func (p *Provider) ListCounterparties(_ context.Context) ([]*types.Counterparty, error) {
	return nil, provider.ErrNotSupported
}

// --- Capabilities ---

func (p *Provider) Capabilities() *types.ProviderCapability {
	return &types.ProviderCapability{
		Name:            "mpesa",
		PaymentTypes:    []string{"mpesa_b2c", "mpesa_c2b_stk", "mpesa_b2b"},
		Currencies:      []string{"KES"},
		Features:        []string{"payments", "balances", "real_time", "mobile_money"},
		Countries:       []string{"KE"},
		SettlementSpeed: "instant",
		Status:          "active",
	}
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
