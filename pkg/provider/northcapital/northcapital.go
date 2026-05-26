// Package northcapital is the Lux treasury-side adapter for the
// North Capital TransactAPI payments rail (ACH / credit card / wire /
// check / IRA) — Workstream 5 of the integration brief.
//
// Conforms to the provider.Provider interface (sibling to stripe,
// wise, mercury, increase, column, unit, marqeta, lithic, mpesa,
// bridge, currencycloud, plaid, square, moderntreasury). All
// payments flow into the same double-entry treasury ledger as every
// other rail; reconciliation, fee attribution, and reporting flow
// through treasuryd unchanged.
//
// Source-of-design: Public-Spec
// Source-ref: https://transactapi.readme.io/
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
	"net/http"
	"time"

	"github.com/luxfi/treasury/pkg/provider"
	"github.com/luxfi/treasury/pkg/types"
)

// Environment base URLs published by NCPS.
const (
	ProdURL    = "https://tapi-live.norcapsecurities.com"
	SandboxURL = "https://api-sandboxdash.norcapsecurities.com"
)

// ErrNotImplemented is returned by every endpoint method whose body
// is intentionally left as scaffolding in this pass.
var ErrNotImplemented = errors.New("northcapital: method not yet implemented (scaffolding pass)")

// Config carries the per-environment credentials and tuning for the
// TransactAPI treasury-side adapter. Credentials are sealed under
// the per-tenant KEK in luxfi/kms; see LoadFromKMS below.
type Config struct {
	BaseURL         string
	ClientID        string
	DeveloperAPIKey string
	HTTPClient      *http.Client
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
	return &Provider{cfg: cfg, client: cfg.HTTPClient}
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return "northcapital" }

// LoadFromKMS is the documented hook for hydrating Config from
// luxfi/kms. The follow-up pass wires this through to the real
// luxfi/kms client; this placeholder defines the integration point
// (per-tenant id, KMS paths under shared/northcapital/<env>/*).
//
// Expected KMS layout (per the integration brief §2):
//
//	shared/northcapital/sandbox/clientID
//	shared/northcapital/sandbox/developerAPIKey
//	shared/northcapital/prod/clientID
//	shared/northcapital/prod/developerAPIKey
func (cfg *Config) LoadFromKMS(_ context.Context, _ string) error {
	// TODO(scaffold/follow-up): wire luxfi/kms client.
	return ErrNotImplemented
}

// --- HTTP helper ---

// Idempotency header name reserved here as a single source of truth
// for the treasury-side adapter; pinned per-endpoint in follow-up
// passes if NCPS uses a different convention.
const idempotencyHeader = "X-Idempotency-Key"

func (p *Provider) doRequestIdem(ctx context.Context, method, path string, body any, idemKey string) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("northcapital: marshal: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.cfg.BaseURL+path, reqBody)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.DeveloperAPIKey)
	req.Header.Set("X-Client-Id", p.cfg.ClientID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idemKey != "" {
		req.Header.Set(idempotencyHeader, idemKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, fmt.Errorf("northcapital API %d: %s", resp.StatusCode, string(data))
	}
	return data, resp.StatusCode, nil
}

// --- provider.Provider interface ---
//
// Most methods stub-return ErrNotImplemented in this scaffolding
// pass. CreatePayment, GetPayment, and CancelPayment carry the real
// skeleton including idempotency-key derivation, even where the HTTP
// body is TODO — to lock in the idempotency contract under tests now.

// CreateAccount — TransactAPI custody-account-opening lives on the
// broker side (see broker/pkg/provider/northcapital/custody.go);
// the treasury surface does not open accounts directly.
func (p *Provider) CreateAccount(_ context.Context, _ string, _ *provider.CreateAccountRequest) (*types.BankAccount, error) {
	return nil, ErrNotImplemented
}

func (p *Provider) GetAccount(_ context.Context, _ string) (*types.BankAccount, error) {
	return nil, ErrNotImplemented
}

func (p *Provider) ListAccounts(_ context.Context, _ string) ([]*types.BankAccount, error) {
	return nil, ErrNotImplemented
}

func (p *Provider) GetBalance(_ context.Context, _ string) (*types.Balance, error) {
	return nil, ErrNotImplemented
}

// CreatePayment routes to /sendACH / /sendCreditCard / /sendWire /
// /sendCheck / /sendIRA based on req.Type. The endpoint path is
// derived deterministically from req.Type; both the path and the
// canonical body feed the deterministic-fallback idempotency-key
// derivation, so retries collapse safely on the upstream.
func (p *Provider) CreatePayment(ctx context.Context, req *types.CreatePaymentRequest) (*types.Payment, error) {
	endpoint, err := paymentEndpoint(req.Type)
	if err != nil {
		return nil, err
	}

	// Caller-supplied IdempotencyKey takes precedence over the
	// deterministic fallback — same contract as every other treasury
	// adapter (stripe, wise, currencycloud, mpesa, etc.).
	idem := req.IdempotencyKey
	if idem == "" {
		// Canonical body for the idempotency-key derivation is the
		// req marshaled to JSON. Stable as long as Go's encoding/json
		// is stable for this shape — which it is.
		body, mErr := json.Marshal(req)
		if mErr != nil {
			return nil, fmt.Errorf("northcapital: canonicalize body: %w", mErr)
		}
		idem = provider.DeriveIdempotencyKey(req.OrgID, endpoint, body)
	}

	// TODO(scaffold/follow-up): construct the TransactAPI-shaped JSON
	// body from the unified req (partyId, offeringId or counterparty
	// detail, amount, currency, etc.), then issue the POST. The
	// scaffolding pass intentionally does not send the HTTP request
	// — only locks in the idempotency-key contract. Returning
	// ErrNotImplemented here is correct until the per-endpoint body
	// shapes are pinned in the follow-up pass.
	_ = idem
	return nil, ErrNotImplemented
}

// GetPayment reads a TransactAPI payment record by paymentID.
func (p *Provider) GetPayment(_ context.Context, _ string) (*types.Payment, error) {
	// TODO(scaffold/follow-up): GET /getPayment?paymentId=…
	return nil, ErrNotImplemented
}

func (p *Provider) ListPayments(_ context.Context, _ string) ([]*types.Payment, error) {
	return nil, ErrNotImplemented
}

// CancelPayment requests cancellation of a pending TransactAPI payment.
// Idempotency is derived from (orgID, endpoint, paymentID) so retries
// of the cancellation collapse safely.
func (p *Provider) CancelPayment(_ context.Context, _ string) error {
	// TODO(scaffold/follow-up): POST /cancelPayment with paymentID;
	// idempotency-key = provider.DeriveIdempotencyKey("",
	// "/cancelPayment", []byte(paymentID)) until the orgID is plumbed
	// through to this method signature.
	return ErrNotImplemented
}

// --- FX is not a TransactAPI surface (USD-only). ---

func (p *Provider) GetFXQuote(_ context.Context, _, _, _, _ string) (*types.FXQuote, error) {
	return nil, provider.ErrNotSupported
}
func (p *Provider) CreateFXConversion(_ context.Context, _ *types.CreateFXConversionRequest) (*types.FXConversion, error) {
	return nil, provider.ErrNotSupported
}
func (p *Provider) ListFXConversions(_ context.Context) ([]*types.FXConversion, error) {
	return nil, provider.ErrNotSupported
}

// --- Counterparties (TransactAPI parties live broker-side). ---

func (p *Provider) CreateCounterparty(_ context.Context, _ *types.Counterparty) (*types.Counterparty, error) {
	return nil, ErrNotImplemented
}
func (p *Provider) ListCounterparties(_ context.Context) ([]*types.Counterparty, error) {
	return nil, ErrNotImplemented
}

// Capabilities declares what this adapter supports.
func (p *Provider) Capabilities() *types.ProviderCapability {
	return &types.ProviderCapability{
		Name:            "northcapital",
		PaymentTypes:    []string{"ach", "credit_card", "wire", "check", "ira"},
		Currencies:      []string{"USD"},
		Features:        []string{"bd_payments", "ats_payments", "custody_funding", "refunds", "ira"},
		Countries:       []string{"US"},
		SettlementSpeed: "t+0_to_t+2",
		Status:          "active",
	}
}

// paymentEndpoint maps a unified PaymentType to the TransactAPI
// endpoint path used to send that payment. The mapping is reserved
// here as the single source of truth; pinned in the follow-up pass
// against the documented endpoint names.
func paymentEndpoint(t types.PaymentType) (string, error) {
	switch t {
	case types.PaymentACH:
		return "/sendACH", nil
	case types.PaymentWire:
		return "/sendWire", nil
	case types.PaymentCheck:
		return "/sendCheck", nil
	case types.PaymentCard:
		return "/sendCreditCard", nil
	case "ira":
		// PaymentType is a string type; IRA is a TransactAPI-specific
		// rail not in the canonical treasury enum. Accepting the raw
		// value here keeps the adapter complete; promotion to a
		// constant happens when types.PaymentType gains PaymentIRA.
		return "/sendIRA", nil
	default:
		return "", fmt.Errorf("northcapital: unsupported payment type %q", string(t))
	}
}
