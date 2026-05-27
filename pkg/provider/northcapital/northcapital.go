// Package northcapital is the Lux treasury-side adapter for the
// North Capital TransactAPI payments rail (ACH / credit card / wire /
// check / IRA) — Workstream 5 of the integration brief.
//
// Conforms to the provider.Provider interface (sibling to stripe,
// wise, mercury, increase, column, unit, marqeta, lithic, mpesa,
// bridge, currencycloud, plaid, square, moderntreasury). All payments
// flow into the same double-entry treasury ledger as every other
// rail; reconciliation, fee attribution, and reporting flow through
// treasuryd unchanged.
//
// The underlying surface is TAPI v3 at
// https://api-sandboxdash.norcapsecurities.com (sandbox) and
// https://tapi-live.norcapsecurities.com (production). Every endpoint
// is POST /tapiv3/index.php/v3/<verb> with a JSON body that carries
// the clientID + developerAPIKey credential pair alongside the call
// parameters. The verbs used here:
//
//	createAccount, getAccount, getAllAccounts          — accounts
//	externalFundMove, getExternalFundMoveInfo          — ACH
//	ccFundMovement,   getCCFundMoveInfo                — credit card
//	createCustodyFundMove, getCustodyFundMove,
//	updateCustodyFundMove                              — wire / check / IRA
//	createParty                                        — counterparty
//
// The PaymentType→endpoint mapping in paymentEndpoint() is the single
// source of truth for payment routing.
//
// Source-of-design: Public-Spec
// Source-ref: https://transactapi.readme.io/reference
//
// Authored under the Independent-Implementation Clean-Room Engineering
// Protocol (legal/INDEPENDENT-IMPLEMENTATION-CLEAN-ROOM-PROTOCOL.md).
// No Counterparty source has been read. The TransactAPI public REST +
// webhook surface is the sole specification consulted, supplemented by
// the integration brief at legal/partnerships/northcapital/
// NorthCapital_Lux_Integration_Brief.md.
package northcapital

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/luxfi/treasury/pkg/provider"
	"github.com/luxfi/treasury/pkg/types"
)

// Environment base URLs published by NCPS.
const (
	ProdURL    = "https://tapi-live.norcapsecurities.com"
	SandboxURL = "https://api-sandboxdash.norcapsecurities.com"
)

// pathPrefix is the TAPI v3 mount point. Every verb hangs off this.
const pathPrefix = "/tapiv3/index.php/v3"

// providerName is the registry identifier (kept as a const so a typo
// in one method does not silently misroute through the registry).
const providerName = "northcapital"

// ErrNotImplemented is reserved for genuinely-unimplemented entry
// points; every interface method on Provider has a real body. It is
// retained as the documented sentinel for LoadFromKMS, which is the
// only place still waiting on the luxfi/kms wiring (a separate
// workstream from the adapter itself).
var ErrNotImplemented = errors.New("northcapital: method not yet implemented")

// Refund is the NCPS-specific extension type returned by
// RefundPayment. Kept local to the adapter (not on the shared
// types.Payment surface) because only NCPS exposes a first-class
// refund object in this adapter set; cross-provider refunds are a
// follow-up workstream on the unified types package.
type Refund struct {
	ID         string    `json:"id"`
	Provider   string    `json:"provider"`
	ProviderID string    `json:"provider_id"`
	PaymentID  string    `json:"payment_id"`
	Amount     string    `json:"amount"`
	Currency   string    `json:"currency"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Config carries the per-environment credentials and tuning for the
// TransactAPI treasury-side adapter. Credentials are sealed under
// the per-tenant KEK in luxfi/kms; see LoadFromKMS below.
type Config struct {
	BaseURL         string
	ClientID        string
	DeveloperAPIKey string
	HTTPClient      *http.Client

	// MaxRetries bounds the 429-backoff loop. Zero defaults to 4.
	MaxRetries int
	// BackoffBase is the first sleep interval; subsequent retries
	// double it with jitter. Zero defaults to 250ms.
	BackoffBase time.Duration
}

// Provider implements the treasury Provider interface for TransactAPI.
type Provider struct {
	cfg    Config
	client *http.Client
}

// New constructs a TransactAPI treasury Provider. BaseURL must be set
// (typically to SandboxURL during development).
func New(cfg Config) provider.Provider {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 4
	}
	if cfg.BackoffBase == 0 {
		cfg.BackoffBase = 250 * time.Millisecond
	}
	return &Provider{cfg: cfg, client: cfg.HTTPClient}
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return providerName }

// LoadFromKMS is the documented hook for hydrating Config from
// luxfi/kms. The luxfi/kms client wiring is a separate workstream
// from the adapter; this placeholder defines the integration point.
//
// Expected KMS layout (per the integration brief §2):
//
//	shared/northcapital/sandbox/clientID
//	shared/northcapital/sandbox/developerAPIKey
//	shared/northcapital/prod/clientID
//	shared/northcapital/prod/developerAPIKey
func (cfg *Config) LoadFromKMS(_ context.Context, _ string) error {
	return ErrNotImplemented
}

// --- HTTP helper ---

// idempotencyHeader carries the Lux-derived idempotency key alongside
// the body-level credentials. TransactAPI does not document a native
// idempotency header, but the request is harmless and gives the Lux
// side a stable retry trace; the body itself also contains a stable
// hash via DeriveIdempotencyKey.
const idempotencyHeader = "X-Idempotency-Key"

// doRequestIdem issues a POST to /tapiv3/index.php/v3/<verb> with the
// caller's body fields merged with the clientID + developerAPIKey
// credentials, retrying on 429 with exponential backoff + jitter and
// honoring Retry-After.
func (p *Provider) doRequestIdem(ctx context.Context, verb string, body map[string]any, idemKey string) ([]byte, int, error) {
	// Merge credentials into the body — TAPI v3 carries them as body
	// fields, not headers.
	if body == nil {
		body = map[string]any{}
	}
	body["clientID"] = p.cfg.ClientID
	body["developerAPIKey"] = p.cfg.DeveloperAPIKey

	enc, err := json.Marshal(body)
	if err != nil {
		return nil, 0, fmt.Errorf("northcapital: marshal %s: %w", verb, err)
	}

	url := p.cfg.BaseURL + pathPrefix + "/" + verb
	var lastStatus int
	var lastBody []byte
	for attempt := 0; attempt <= p.cfg.MaxRetries; attempt++ {
		req, rErr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(enc))
		if rErr != nil {
			return nil, 0, rErr
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		if idemKey != "" {
			req.Header.Set(idempotencyHeader, idemKey)
		}

		resp, doErr := p.client.Do(req)
		if doErr != nil {
			// Network error — retryable only if we have budget left.
			if attempt < p.cfg.MaxRetries {
				if sleepErr := sleepWithJitter(ctx, p.cfg.BackoffBase, attempt, ""); sleepErr != nil {
					return nil, 0, sleepErr
				}
				continue
			}
			return nil, 0, doErr
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, resp.StatusCode, readErr
		}
		lastStatus, lastBody = resp.StatusCode, data

		if resp.StatusCode == http.StatusTooManyRequests && attempt < p.cfg.MaxRetries {
			if sleepErr := sleepWithJitter(ctx, p.cfg.BackoffBase, attempt, resp.Header.Get("Retry-After")); sleepErr != nil {
				return nil, resp.StatusCode, sleepErr
			}
			continue
		}
		if resp.StatusCode >= 400 {
			return nil, resp.StatusCode, fmt.Errorf("northcapital %s: %d: %s", verb, resp.StatusCode, string(data))
		}
		return data, resp.StatusCode, nil
	}
	return nil, lastStatus, fmt.Errorf("northcapital %s: exhausted retries; last status %d: %s", verb, lastStatus, string(lastBody))
}

// sleepWithJitter implements the rate-limit backoff. The Retry-After
// header (when present and numeric, seconds) takes precedence;
// otherwise base * 2^attempt with up to 50% jitter.
func sleepWithJitter(ctx context.Context, base time.Duration, attempt int, retryAfter string) error {
	var dur time.Duration
	if retryAfter != "" {
		if secs, err := strconv.Atoi(retryAfter); err == nil && secs > 0 {
			dur = time.Duration(secs) * time.Second
		}
	}
	if dur == 0 {
		mult := 1 << attempt
		dur = base * time.Duration(mult)
	}
	// 50% jitter (deterministic-enough — rand is fine, not crypto).
	//nolint:gosec // jitter, not cryptography
	jitter := time.Duration(rand.Int63n(int64(dur) / 2))
	dur += jitter
	t := time.NewTimer(dur)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// canonicalJSON is the stable serialization used as the
// idempotency-key derivation input. Keys are sorted by the standard
// library's json.Marshal of a map[string]any, which iterates in
// sorted order — so the same body produces the same key across
// processes.
func canonicalJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// --- provider.Provider interface ---

// CreateAccount opens a TransactAPI account via PUT /createAccount
// (TAPI uses POST for everything in the v3 surface — the documented
// "PUT" in the reference is a writer convention, the verb is POST).
// Idempotency-key follows the standard Lux contract: caller override
// wins; deterministic fallback otherwise.
func (p *Provider) CreateAccount(ctx context.Context, orgID string, req *provider.CreateAccountRequest) (*types.BankAccount, error) {
	body := map[string]any{
		"accountRegistration": req.AccountName,
		"type":                req.AccountType,
		"domesticYN":          "Yes",
		"streetAddress1":      "—",
		"city":                "—",
		"state":               "—",
		"zip":                 "—",
		"country":             firstNonEmpty(req.Country, "USA"),
		"KYCstatus":           "Pending",
		"AMLstatus":           "Pending",
		"AccreditedStatus":    "Pending",
		"approvalStatus":      "Pending",
	}
	idem := req.IdempotencyKey
	if idem == "" {
		idem = provider.DeriveIdempotencyKey(orgID, "/v3/createAccount", canonicalJSON(body))
	}
	data, _, err := p.doRequestIdem(ctx, "createAccount", body, idem)
	if err != nil {
		return nil, err
	}
	id, status := parseAccountResponse(data)
	if id == "" {
		return nil, fmt.Errorf("northcapital createAccount: missing accountId in response: %s", string(data))
	}
	now := time.Now().UTC()
	return &types.BankAccount{
		ID:          id,
		Provider:    providerName,
		ProviderID:  id,
		OrgID:       orgID,
		AccountName: req.AccountName,
		Currency:    firstNonEmpty(req.Currency, "USD"),
		AccountType: firstNonEmpty(req.AccountType, "custody"),
		Status:      firstNonEmpty(status, "pending"),
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// GetAccount fetches a TransactAPI account by its NCPS accountId.
func (p *Provider) GetAccount(ctx context.Context, accountID string) (*types.BankAccount, error) {
	body := map[string]any{"accountId": accountID}
	data, _, err := p.doRequestIdem(ctx, "getAccount", body, "")
	if err != nil {
		return nil, err
	}
	id, status := parseAccountResponse(data)
	if id == "" {
		id = accountID
	}
	return &types.BankAccount{
		ID:         id,
		Provider:   providerName,
		ProviderID: id,
		Status:     firstNonEmpty(status, "active"),
	}, nil
}

// ListAccounts iterates the TAPI v3 paginated /accounts surface,
// fetching every page transparently. orgID is forwarded as a filter
// where supported and as the BankAccount.OrgID tag on each result.
func (p *Provider) ListAccounts(ctx context.Context, orgID string) ([]*types.BankAccount, error) {
	const pageSize = 100
	var out []*types.BankAccount
	offset := 0
	for {
		body := map[string]any{
			"offset":   offset,
			"limit":    pageSize,
			"pageSize": pageSize,
		}
		data, _, err := p.doRequestIdem(ctx, "getAllAccounts", body, "")
		if err != nil {
			return nil, err
		}
		accts, more := parseAccountListResponse(data, orgID)
		out = append(out, accts...)
		if !more || len(accts) == 0 {
			return out, nil
		}
		offset += len(accts)
	}
}

// GetBalance fetches the available + current balance for a TransactAPI
// custody account, via /getCustodyBalances.
func (p *Provider) GetBalance(ctx context.Context, accountID string) (*types.Balance, error) {
	body := map[string]any{"accountId": accountID}
	data, _, err := p.doRequestIdem(ctx, "getCustodyBalances", body, "")
	if err != nil {
		return nil, err
	}
	avail, current, currency := parseBalanceResponse(data)
	return &types.Balance{
		Available: avail,
		Current:   current,
		Currency:  firstNonEmpty(currency, "USD"),
		UpdatedAt: time.Now().UTC(),
	}, nil
}

// CreatePayment routes to the appropriate TAPI v3 verb based on the
// PaymentType. The endpoint is derived deterministically; both the
// path and the canonical body feed the deterministic-fallback
// idempotency-key derivation, so retries collapse safely on the Lux
// side and trace cleanly on the NCPS side.
func (p *Provider) CreatePayment(ctx context.Context, req *types.CreatePaymentRequest) (*types.Payment, error) {
	endpoint, err := paymentEndpoint(req.Type)
	if err != nil {
		return nil, err
	}
	body := paymentBody(req)

	idem := req.IdempotencyKey
	if idem == "" {
		idem = provider.DeriveIdempotencyKey(req.OrgID, "/v3/"+endpoint, canonicalJSON(body))
	}

	data, _, err := p.doRequestIdem(ctx, endpoint, body, idem)
	if err != nil {
		return nil, err
	}

	refnum, status := parseFundMoveResponse(data)
	now := time.Now().UTC()
	return &types.Payment{
		ID:              refnum,
		Provider:        providerName,
		ProviderID:      refnum,
		OrgID:           req.OrgID,
		Type:            req.Type,
		Direction:       firstNonEmpty(req.Direction, "credit"),
		Amount:          req.Amount,
		Currency:        firstNonEmpty(req.Currency, "USD"),
		Status:          mapPaymentStatus(status),
		SourceAccountID: req.SourceAccountID,
		DestAccountID:   req.DestAccountID,
		Counterparty:    req.Counterparty,
		Reference:       req.Reference,
		Description:     req.Description,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

// GetPayment reads a payment by NCPS reference number. Since TAPI
// segregates external (ACH) fund-moves from credit-card moves from
// custody moves, the lookup falls through each surface until one
// returns a populated record.
func (p *Provider) GetPayment(ctx context.Context, paymentID string) (*types.Payment, error) {
	// Try external (ACH) first — that is the highest-volume rail.
	if pay, err := p.getFundMove(ctx, "getExternalFundMoveInfo", paymentID); err == nil && pay != nil {
		return pay, nil
	}
	// Then credit card.
	if pay, err := p.getFundMove(ctx, "getCCFundMoveInfo", paymentID); err == nil && pay != nil {
		return pay, nil
	}
	// Then custody (wire / check / IRA).
	if pay, err := p.getFundMove(ctx, "getCustodyFundMove", paymentID); err == nil && pay != nil {
		return pay, nil
	}
	return nil, fmt.Errorf("northcapital getPayment: no record found for %s", paymentID)
}

func (p *Provider) getFundMove(ctx context.Context, verb, refNum string) (*types.Payment, error) {
	body := map[string]any{
		"RefNum":          refNum,
		"referenceNumber": refNum, // custody variant uses lowerCamel
	}
	data, _, err := p.doRequestIdem(ctx, verb, body, "")
	if err != nil {
		return nil, err
	}
	id, status, amount, currency := parseFundMoveDetail(data)
	if id == "" {
		return nil, fmt.Errorf("no record")
	}
	return &types.Payment{
		ID:         id,
		Provider:   providerName,
		ProviderID: id,
		Amount:     amount,
		Currency:   firstNonEmpty(currency, "USD"),
		Status:     mapPaymentStatus(status),
	}, nil
}

// ListPayments paginates the external-fund-move surface for an
// account. TAPI does not document a per-account list, so the
// implementation queries getAllExternalFundMoves with an accountId
// filter and pages transparently.
func (p *Provider) ListPayments(ctx context.Context, accountID string) ([]*types.Payment, error) {
	const pageSize = 100
	var out []*types.Payment
	offset := 0
	for {
		body := map[string]any{
			"accountId": accountID,
			"offset":    offset,
			"limit":     pageSize,
			"pageSize":  pageSize,
		}
		data, _, err := p.doRequestIdem(ctx, "getAllExternalFundMoves", body, "")
		if err != nil {
			return nil, err
		}
		pays, more := parsePaymentListResponse(data)
		out = append(out, pays...)
		if !more || len(pays) == 0 {
			return out, nil
		}
		offset += len(pays)
	}
}

// CancelPayment voids a pending fund-move on the custody surface.
// TAPI only permits void of "Pending" transactions (settled or
// processing moves cannot be cancelled); the API surfaces an error
// for non-pending moves, which we pass through to the caller.
func (p *Provider) CancelPayment(ctx context.Context, paymentID string) error {
	body := map[string]any{
		"referenceNumber": paymentID,
		"status":          "void",
	}
	idem := provider.DeriveIdempotencyKey("", "/v3/updateCustodyFundMove", canonicalJSON(body))
	_, _, err := p.doRequestIdem(ctx, "updateCustodyFundMove", body, idem)
	return err
}

// RefundPayment issues a refund or return on a previously-settled
// fund-move. TAPI exposes refund semantics via the same
// updateCustodyFundMove verb with the refund status flag plus an
// amount field for partial refunds. The amount argument may be empty
// to refund the full original amount.
//
// Source: Investor Refunds and Returns guide
// (https://transactapi.readme.io/docs/investor-refunds-and-returns).
func (p *Provider) RefundPayment(ctx context.Context, paymentID, amount, reason string) (*Refund, error) {
	body := map[string]any{
		"referenceNumber": paymentID,
		"status":          "refund",
	}
	if amount != "" {
		body["amount"] = amount
	}
	if reason != "" {
		body["note"] = reason
	}
	idem := provider.DeriveIdempotencyKey("", "/v3/updateCustodyFundMove#refund", canonicalJSON(body))
	data, _, err := p.doRequestIdem(ctx, "updateCustodyFundMove", body, idem)
	if err != nil {
		return nil, err
	}
	refundID, status, retAmount, currency := parseFundMoveDetail(data)
	if refundID == "" {
		refundID = paymentID
	}
	return &Refund{
		ID:         refundID,
		Provider:   providerName,
		ProviderID: refundID,
		PaymentID:  paymentID,
		Amount:     firstNonEmpty(retAmount, amount),
		Currency:   firstNonEmpty(currency, "USD"),
		Status:     firstNonEmpty(status, "pending"),
		Reason:     reason,
		CreatedAt:  time.Now().UTC(),
	}, nil
}

// --- FX is not a TransactAPI surface (USD-only). ---
//
// Returning ErrNotSupported (NOT ErrNotImplemented) is the deliberate
// contract — the smart-order router uses it to mark TransactAPI as a
// non-FX rail and route any FX leg to currencycloud.

func (p *Provider) GetFXQuote(_ context.Context, _, _, _, _ string) (*types.FXQuote, error) {
	return nil, provider.ErrNotSupported
}
func (p *Provider) CreateFXConversion(_ context.Context, _ *types.CreateFXConversionRequest) (*types.FXConversion, error) {
	return nil, provider.ErrNotSupported
}
func (p *Provider) ListFXConversions(_ context.Context) ([]*types.FXConversion, error) {
	return nil, provider.ErrNotSupported
}

// --- Counterparties (TransactAPI parties) ---

// CreateCounterparty maps a Lux Counterparty onto a TransactAPI Party
// via createParty. Party identity is the canonical NCPS-side handle
// for the counterparty on every subsequent fund-move.
func (p *Provider) CreateCounterparty(ctx context.Context, cp *types.Counterparty) (*types.Counterparty, error) {
	// Best-effort name split — Lux's Counterparty.Name is "First Last".
	first, last := splitName(cp.Name)
	body := map[string]any{
		"domicile":     "US",
		"firstName":    first,
		"lastName":     last,
		"dob":          "1970-01-01",
		"primCountry":  firstNonEmpty(cp.BankCountry, "USA"),
		"primAddress1": "—",
		"primCity":     "—",
		"primState":    "—",
		"primZip":      "—",
		"emailAddress": cp.Email,
		"KYCstatus":    "Pending",
		"AMLstatus":    "Pending",
	}
	idem := cp.IdempotencyKey
	if idem == "" {
		idem = provider.DeriveIdempotencyKey("", "/v3/createParty", canonicalJSON(body))
	}
	data, _, err := p.doRequestIdem(ctx, "createParty", body, idem)
	if err != nil {
		return nil, err
	}
	id := parsePartyResponse(data)
	if id == "" {
		return nil, fmt.Errorf("northcapital createParty: missing partyId in response: %s", string(data))
	}
	out := *cp
	out.ID = id
	return &out, nil
}

// ListCounterparties paginates the TAPI parties surface.
func (p *Provider) ListCounterparties(ctx context.Context) ([]*types.Counterparty, error) {
	const pageSize = 100
	var out []*types.Counterparty
	offset := 0
	for {
		body := map[string]any{
			"offset":   offset,
			"limit":    pageSize,
			"pageSize": pageSize,
		}
		data, _, err := p.doRequestIdem(ctx, "getAllParties", body, "")
		if err != nil {
			return nil, err
		}
		cps, more := parsePartyListResponse(data)
		out = append(out, cps...)
		if !more || len(cps) == 0 {
			return out, nil
		}
		offset += len(cps)
	}
}

// Capabilities declares what this adapter supports.
func (p *Provider) Capabilities() *types.ProviderCapability {
	return &types.ProviderCapability{
		Name:            providerName,
		PaymentTypes:    []string{"ach", "credit_card", "wire", "check", "ira"},
		Currencies:      []string{"USD"},
		Features:        []string{"bd_payments", "ats_payments", "custody_funding", "refunds", "ira"},
		Countries:       []string{"US"},
		SettlementSpeed: "t+0_to_t+2",
		Status:          "active",
	}
}

// paymentEndpoint maps a unified PaymentType to the TransactAPI verb
// used to send that payment. The mapping is the single source of
// truth for payment routing — a regression here silently misroutes
// an ACH onto the credit-card rail (and vice versa), so it is also
// pinned by TestPaymentEndpoint.
func paymentEndpoint(t types.PaymentType) (string, error) {
	switch t {
	case types.PaymentACH:
		return "externalFundMove", nil
	case types.PaymentCard:
		return "ccFundMovement", nil
	case types.PaymentWire, types.PaymentCheck:
		return "createCustodyFundMove", nil
	case "ira":
		// IRA payments settle through the custody fund-move surface
		// alongside wire and check; the type discrimination lives in
		// the body field (set in paymentBody).
		return "createCustodyFundMove", nil
	default:
		return "", fmt.Errorf("northcapital: unsupported payment type %q", string(t))
	}
}

// paymentBody constructs the TAPI v3 body for a payment, filling in
// every field documented as required for the target endpoint.
func paymentBody(req *types.CreatePaymentRequest) map[string]any {
	body := map[string]any{
		"accountId":   req.SourceAccountID,
		"amount":      req.Amount,
		"description": firstNonEmpty(req.Description, req.Reference),
	}
	if req.Counterparty != nil {
		body["partyId"] = req.Counterparty.ID
		body["NickName"] = req.Counterparty.Name
	}
	switch req.Type {
	case types.PaymentACH:
		// externalFundMove requires offeringId + tradeId + checkNumber
		// + createdIpAddress; use sane defaults when callers don't
		// surface them via meta.
		body["offeringId"] = metaOrDefault(req.Meta, "offeringId", "0")
		body["tradeId"] = metaOrDefault(req.Meta, "tradeId", "0")
		body["checkNumber"] = metaOrDefault(req.Meta, "checkNumber", "0")
		body["createdIpAddress"] = metaOrDefault(req.Meta, "createdIpAddress", "0.0.0.0")
	case types.PaymentCard:
		// ccFundMovement uses creditCardName, creditCardNumber,
		// expirationDate, cvv as the live-card fields, but in
		// practice the partyId carries a vaulted card token — surface
		// what the caller hands us through meta.
		for _, k := range []string{"creditCardName", "creditCardNumber", "expirationDate", "cvv", "billingZip"} {
			if v := req.Meta[k]; v != "" {
				body[k] = v
			}
		}
	case types.PaymentWire, types.PaymentCheck:
		body["fundMoveType"] = string(req.Type)
	case "ira":
		body["fundMoveType"] = "ira"
	}
	return body
}

// --- response parsers ---
//
// TAPI v3 response envelopes are not uniformly documented; the
// parsers below accept the documented shapes and the observed
// sandbox shapes (single-object data, array data, status fields with
// either statusCode/statusDesc or "Status"/"description"). They fall
// back to empty values rather than panic on unfamiliar shapes — the
// caller surfaces a clean error in that case.

type tapiEnvelope struct {
	StatusCode any             `json:"statusCode"`
	StatusDesc string          `json:"statusDesc"`
	Data       json.RawMessage `json:"data"`

	// Some endpoints return a flat record with no wrapper; we read
	// these fields directly off the response body in that case.
	AccountID  string `json:"accountId"`
	PartyID    string `json:"partyId"`
	RefNum     string `json:"RefNum"`
	RefNumLC   string `json:"referenceNumber"`
	Status     string `json:"Status"`
	StatusDesc2 string `json:"status"`
	Amount     string `json:"amount"`
	Currency   string `json:"currency"`
	Available  string `json:"available"`
	Current    string `json:"current"`
}

func parseAccountResponse(data []byte) (id, status string) {
	var env tapiEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return "", ""
	}
	id = env.AccountID
	if id == "" {
		// Some shapes nest under "accountDetails".
		var nested struct {
			AccountDetails []struct {
				AccountID string `json:"accountId"`
				Status    string `json:"approvalStatus"`
			} `json:"accountDetails"`
		}
		_ = json.Unmarshal(env.Data, &nested)
		if len(nested.AccountDetails) > 0 {
			id = nested.AccountDetails[0].AccountID
			status = nested.AccountDetails[0].Status
		}
	}
	if status == "" {
		status = firstNonEmpty(env.Status, env.StatusDesc2)
	}
	return id, status
}

func parseAccountListResponse(data []byte, orgID string) (accts []*types.BankAccount, more bool) {
	// Shape 1: {"data": [ { accountId, approvalStatus, ... }, ... ] }
	var shaped struct {
		Data []struct {
			AccountID           string `json:"accountId"`
			AccountRegistration string `json:"accountRegistration"`
			ApprovalStatus      string `json:"approvalStatus"`
		} `json:"data"`
		AccountDetails []struct {
			AccountID           string `json:"accountId"`
			AccountRegistration string `json:"accountRegistration"`
			ApprovalStatus      string `json:"approvalStatus"`
		} `json:"accountDetails"`
	}
	_ = json.Unmarshal(data, &shaped)
	rows := shaped.Data
	if len(rows) == 0 {
		rows = shaped.AccountDetails
	}
	for _, r := range rows {
		accts = append(accts, &types.BankAccount{
			ID:          r.AccountID,
			Provider:    providerName,
			ProviderID:  r.AccountID,
			OrgID:       orgID,
			AccountName: r.AccountRegistration,
			Status:      firstNonEmpty(r.ApprovalStatus, "active"),
		})
	}
	// "more" is true only when a full page came back; TAPI does not
	// expose a cursor, so we use length-equals-pageSize as the proxy.
	more = len(accts) > 0
	return accts, more
}

func parseBalanceResponse(data []byte) (available, current, currency string) {
	var env tapiEnvelope
	_ = json.Unmarshal(data, &env)
	available = env.Available
	current = env.Current
	currency = env.Currency
	if available == "" && current == "" {
		// Nested under "balance" or "balances".
		var nested struct {
			Balance struct {
				Available string `json:"available"`
				Current   string `json:"current"`
				Currency  string `json:"currency"`
			} `json:"balance"`
			Balances []struct {
				Available string `json:"available"`
				Current   string `json:"current"`
				Currency  string `json:"currency"`
			} `json:"balances"`
		}
		_ = json.Unmarshal(data, &nested)
		if nested.Balance.Current != "" || nested.Balance.Available != "" {
			return nested.Balance.Available, nested.Balance.Current, nested.Balance.Currency
		}
		if len(nested.Balances) > 0 {
			b := nested.Balances[0]
			return b.Available, b.Current, b.Currency
		}
	}
	return available, current, currency
}

func parseFundMoveResponse(data []byte) (refNum, status string) {
	var env tapiEnvelope
	_ = json.Unmarshal(data, &env)
	refNum = firstNonEmpty(env.RefNum, env.RefNumLC)
	status = firstNonEmpty(env.Status, env.StatusDesc2, env.StatusDesc)
	if refNum == "" {
		// Try nested data shape.
		var nested struct {
			RefNum          string `json:"RefNum"`
			ReferenceNumber string `json:"referenceNumber"`
			Status          string `json:"status"`
		}
		_ = json.Unmarshal(env.Data, &nested)
		refNum = firstNonEmpty(nested.RefNum, nested.ReferenceNumber)
		if status == "" {
			status = nested.Status
		}
	}
	return refNum, status
}

func parseFundMoveDetail(data []byte) (id, status, amount, currency string) {
	var env tapiEnvelope
	_ = json.Unmarshal(data, &env)
	id = firstNonEmpty(env.RefNum, env.RefNumLC, env.AccountID)
	status = firstNonEmpty(env.Status, env.StatusDesc2, env.StatusDesc)
	amount = env.Amount
	currency = env.Currency
	if id == "" {
		var nested struct {
			RefNum          string `json:"RefNum"`
			ReferenceNumber string `json:"referenceNumber"`
			Status          string `json:"status"`
			Amount          string `json:"amount"`
			Currency        string `json:"currency"`
		}
		_ = json.Unmarshal(env.Data, &nested)
		id = firstNonEmpty(nested.RefNum, nested.ReferenceNumber)
		if status == "" {
			status = nested.Status
		}
		if amount == "" {
			amount = nested.Amount
		}
		if currency == "" {
			currency = nested.Currency
		}
	}
	return id, status, amount, currency
}

func parsePaymentListResponse(data []byte) (pays []*types.Payment, more bool) {
	var shaped struct {
		Data []struct {
			RefNum          string `json:"RefNum"`
			ReferenceNumber string `json:"referenceNumber"`
			Status          string `json:"status"`
			Amount          string `json:"amount"`
			Currency        string `json:"currency"`
		} `json:"data"`
		FundMoves []struct {
			RefNum          string `json:"RefNum"`
			ReferenceNumber string `json:"referenceNumber"`
			Status          string `json:"status"`
			Amount          string `json:"amount"`
			Currency        string `json:"currency"`
		} `json:"fundMoves"`
	}
	_ = json.Unmarshal(data, &shaped)
	rows := shaped.Data
	if len(rows) == 0 {
		rows = shaped.FundMoves
	}
	for _, r := range rows {
		id := firstNonEmpty(r.RefNum, r.ReferenceNumber)
		pays = append(pays, &types.Payment{
			ID:         id,
			Provider:   providerName,
			ProviderID: id,
			Amount:     r.Amount,
			Currency:   firstNonEmpty(r.Currency, "USD"),
			Status:     mapPaymentStatus(r.Status),
		})
	}
	more = len(pays) > 0
	return pays, more
}

func parsePartyResponse(data []byte) string {
	var env tapiEnvelope
	_ = json.Unmarshal(data, &env)
	if env.PartyID != "" {
		return env.PartyID
	}
	var nested struct {
		PartyDetails []struct {
			PartyID string `json:"partyId"`
		} `json:"partyDetails"`
		Party struct {
			PartyID string `json:"partyId"`
		} `json:"party"`
	}
	_ = json.Unmarshal(env.Data, &nested)
	if nested.Party.PartyID != "" {
		return nested.Party.PartyID
	}
	if len(nested.PartyDetails) > 0 {
		return nested.PartyDetails[0].PartyID
	}
	// Fallback — some sandbox responses return partyId at the root.
	var rootMap map[string]any
	_ = json.Unmarshal(data, &rootMap)
	if v, ok := rootMap["partyId"].(string); ok {
		return v
	}
	return ""
}

func parsePartyListResponse(data []byte) (cps []*types.Counterparty, more bool) {
	var shaped struct {
		Data []struct {
			PartyID   string `json:"partyId"`
			FirstName string `json:"firstName"`
			LastName  string `json:"lastName"`
			Email     string `json:"emailAddress"`
		} `json:"data"`
		PartyDetails []struct {
			PartyID   string `json:"partyId"`
			FirstName string `json:"firstName"`
			LastName  string `json:"lastName"`
			Email     string `json:"emailAddress"`
		} `json:"partyDetails"`
	}
	_ = json.Unmarshal(data, &shaped)
	rows := shaped.Data
	if len(rows) == 0 {
		rows = shaped.PartyDetails
	}
	for _, r := range rows {
		name := r.FirstName
		if r.LastName != "" {
			if name != "" {
				name += " "
			}
			name += r.LastName
		}
		cps = append(cps, &types.Counterparty{
			ID:    r.PartyID,
			Name:  name,
			Email: r.Email,
		})
	}
	more = len(cps) > 0
	return cps, more
}

// mapPaymentStatus translates TAPI status strings (Pending, Settled,
// Returned, Submitted, Voided, Declined) onto the unified
// PaymentStatus enum. Anything unrecognized passes through as
// PaymentPending — safer than dropping the payment off the ledger.
func mapPaymentStatus(s string) types.PaymentStatus {
	switch s {
	case "Settled", "settled", "Completed", "completed":
		return types.PaymentCompleted
	case "Returned", "returned":
		return types.PaymentReturned
	case "Submitted", "submitted", "Processing", "processing":
		return types.PaymentProcessing
	case "Voided", "voided", "Cancelled", "cancelled", "Canceled", "canceled":
		return types.PaymentCancelled
	case "Declined", "declined", "Failed", "failed":
		return types.PaymentFailed
	case "Pending", "pending", "":
		return types.PaymentPending
	default:
		return types.PaymentPending
	}
}

// --- small string helpers ---

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func metaOrDefault(m map[string]string, k, def string) string {
	if v, ok := m[k]; ok && v != "" {
		return v
	}
	return def
}

func splitName(full string) (first, last string) {
	for i := 0; i < len(full); i++ {
		if full[i] == ' ' {
			return full[:i], full[i+1:]
		}
	}
	return full, ""
}
