// Source-of-design: Public-Spec
// Source-ref: https://transactapi.readme.io/

package northcapital

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luxfi/treasury/pkg/provider"
	"github.com/luxfi/treasury/pkg/types"
)

func newTestProvider(t *testing.T, h http.HandlerFunc) provider.Provider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(Config{
		BaseURL:         srv.URL,
		ClientID:        "test-client",
		DeveloperAPIKey: "test-key",
		HTTPClient:      srv.Client(),
	})
}

// TestNew verifies that a Provider can be constructed against the
// sandbox base URL with the documented credential fields, and that
// the Name() + Capabilities() surface is wired up.
func TestNew(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{name: "sandbox", cfg: Config{BaseURL: SandboxURL, ClientID: "cid", DeveloperAPIKey: "dak"}},
		{name: "prod", cfg: Config{BaseURL: ProdURL, ClientID: "cid", DeveloperAPIKey: "dak"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := New(tc.cfg)
			if p == nil {
				t.Fatal("New returned nil")
			}
			if p.Name() != "northcapital" {
				t.Fatalf("Name: got %q, want %q", p.Name(), "northcapital")
			}
		})
	}
}

// TestCapabilities locks the capability declaration so a regression
// (e.g., dropping a payment-type when adding a method body) trips a
// test rather than silently mis-routing in production.
func TestCapabilities(t *testing.T) {
	p := New(Config{BaseURL: SandboxURL, ClientID: "cid", DeveloperAPIKey: "dak"})
	caps := p.Capabilities()
	if caps == nil {
		t.Fatal("Capabilities returned nil")
	}
	if caps.Name != "northcapital" {
		t.Fatalf("Name: got %q, want %q", caps.Name, "northcapital")
	}
	if caps.Status != "active" {
		t.Fatalf("Status: got %q, want %q", caps.Status, "active")
	}

	wantPayments := map[string]bool{"ach": true, "credit_card": true, "wire": true, "check": true, "ira": true}
	for _, pt := range caps.PaymentTypes {
		if !wantPayments[pt] {
			t.Fatalf("unexpected payment type %q", pt)
		}
		delete(wantPayments, pt)
	}
	if len(wantPayments) > 0 {
		t.Fatalf("missing payment types: %v", wantPayments)
	}

	wantFeatures := map[string]bool{"bd_payments": true, "ats_payments": true, "custody_funding": true, "refunds": true, "ira": true}
	for _, f := range caps.Features {
		if !wantFeatures[f] {
			t.Fatalf("unexpected feature %q", f)
		}
		delete(wantFeatures, f)
	}
	if len(wantFeatures) > 0 {
		t.Fatalf("missing features: %v", wantFeatures)
	}

	if len(caps.Currencies) != 1 || caps.Currencies[0] != "USD" {
		t.Fatalf("Currencies: got %v, want [USD]", caps.Currencies)
	}
	if len(caps.Countries) != 1 || caps.Countries[0] != "US" {
		t.Fatalf("Countries: got %v, want [US]", caps.Countries)
	}
}

// TestStubsReturnNotImplemented locks the scaffolding contract:
// every interface method (except the two FX methods, which return
// the canonical ErrNotSupported) returns ErrNotImplemented until its
// follow-up pass lands.
func TestStubsReturnNotImplemented(t *testing.T) {
	p := newTestProvider(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("scaffolding stubs must not issue HTTP traffic")
	})
	ctx := context.Background()

	if _, err := p.CreateAccount(ctx, "org-A", &provider.CreateAccountRequest{}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("CreateAccount: got %v, want ErrNotImplemented", err)
	}
	if _, err := p.GetAccount(ctx, "acct-1"); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("GetAccount: got %v, want ErrNotImplemented", err)
	}
	if _, err := p.ListAccounts(ctx, "org-A"); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("ListAccounts: got %v, want ErrNotImplemented", err)
	}
	if _, err := p.GetBalance(ctx, "acct-1"); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("GetBalance: got %v, want ErrNotImplemented", err)
	}
	if _, err := p.CreatePayment(ctx, &types.CreatePaymentRequest{Type: types.PaymentACH}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("CreatePayment: got %v, want ErrNotImplemented", err)
	}
	if _, err := p.GetPayment(ctx, "pay-1"); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("GetPayment: got %v, want ErrNotImplemented", err)
	}
	if _, err := p.ListPayments(ctx, "acct-1"); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("ListPayments: got %v, want ErrNotImplemented", err)
	}
	if err := p.CancelPayment(ctx, "pay-1"); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("CancelPayment: got %v, want ErrNotImplemented", err)
	}
	if _, err := p.CreateCounterparty(ctx, &types.Counterparty{}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("CreateCounterparty: got %v, want ErrNotImplemented", err)
	}
	if _, err := p.ListCounterparties(ctx); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("ListCounterparties: got %v, want ErrNotImplemented", err)
	}
}

// TestFXNotSupported pins the conscious choice that TransactAPI is
// USD-only and explicitly returns the canonical provider.ErrNotSupported
// (NOT ErrNotImplemented) so callers + the smart-order router can
// distinguish "this provider does not do FX, route to currencycloud"
// from "implementation pending, retry later".
func TestFXNotSupported(t *testing.T) {
	p := New(Config{BaseURL: SandboxURL, ClientID: "cid", DeveloperAPIKey: "dak"})
	ctx := context.Background()
	if _, err := p.GetFXQuote(ctx, "USD", "EUR", "100", "sell"); !errors.Is(err, provider.ErrNotSupported) {
		t.Fatalf("GetFXQuote: got %v, want provider.ErrNotSupported", err)
	}
	if _, err := p.CreateFXConversion(ctx, &types.CreateFXConversionRequest{}); !errors.Is(err, provider.ErrNotSupported) {
		t.Fatalf("CreateFXConversion: got %v, want provider.ErrNotSupported", err)
	}
	if _, err := p.ListFXConversions(ctx); !errors.Is(err, provider.ErrNotSupported) {
		t.Fatalf("ListFXConversions: got %v, want provider.ErrNotSupported", err)
	}
}

// TestPaymentEndpoint covers the PaymentType → TransactAPI endpoint
// map. A regression in the map silently mis-routes ACH to /sendWire
// (etc.) in production — exactly the class of bug a single line
// table-test catches.
func TestPaymentEndpoint(t *testing.T) {
	cases := []struct {
		in   types.PaymentType
		want string
	}{
		{types.PaymentACH, "/sendACH"},
		{types.PaymentWire, "/sendWire"},
		{types.PaymentCheck, "/sendCheck"},
		{types.PaymentCard, "/sendCreditCard"},
		{"ira", "/sendIRA"},
	}
	for _, tc := range cases {
		got, err := paymentEndpoint(tc.in)
		if err != nil {
			t.Fatalf("paymentEndpoint(%q): unexpected error %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("paymentEndpoint(%q): got %q, want %q", tc.in, got, tc.want)
		}
	}

	if _, err := paymentEndpoint(types.PaymentSEPA); err == nil {
		t.Fatal("paymentEndpoint(sepa): expected unsupported error")
	}
}
