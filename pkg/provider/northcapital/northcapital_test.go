// Source-of-design: Public-Spec
// Source-ref: https://transactapi.readme.io/reference

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

func newTestProvider(t *testing.T, h http.HandlerFunc) *Provider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Provider{
		cfg: Config{
			BaseURL:         srv.URL,
			ClientID:        "test-client",
			DeveloperAPIKey: "test-key",
			HTTPClient:      srv.Client(),
			MaxRetries:      2,
		},
		client: srv.Client(),
	}
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

// TestPaymentEndpoint covers the PaymentType → TransactAPI verb map.
// A regression in the map silently mis-routes ACH to ccFundMovement
// (etc.) in production — exactly the class of bug a single-line
// table-test catches.
func TestPaymentEndpoint(t *testing.T) {
	cases := []struct {
		in   types.PaymentType
		want string
	}{
		{types.PaymentACH, "externalFundMove"},
		{types.PaymentCard, "ccFundMovement"},
		{types.PaymentWire, "createCustodyFundMove"},
		{types.PaymentCheck, "createCustodyFundMove"},
		{"ira", "createCustodyFundMove"},
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

// TestLoadFromKMSWired pins that LoadFromKMS is a real entry point
// now (no longer ErrNotImplemented). The deep conformance tests
// live in kms_test.go; this case is a smoke test that the (ctx,
// KMSGetter, env) signature behaves on the unknown-env validation
// path. ErrNotImplemented is retained as the sentinel for any
// future Provider method that ships its scaffold ahead of its body.
func TestLoadFromKMSWired(t *testing.T) {
	cfg := Config{}
	err := cfg.LoadFromKMS(context.Background(), newMockKMS(), "staging")
	if err == nil {
		t.Fatal("LoadFromKMS unknown env: got nil, want error")
	}
	if errors.Is(err, ErrNotImplemented) {
		t.Fatal("LoadFromKMS is now wired: must not return ErrNotImplemented")
	}
}

// TestMapPaymentStatus pins the TAPI→unified status translation. A
// drift here misreports settlement state into the double-entry
// ledger downstream.
func TestMapPaymentStatus(t *testing.T) {
	cases := map[string]types.PaymentStatus{
		"Pending":   types.PaymentPending,
		"pending":   types.PaymentPending,
		"":          types.PaymentPending,
		"Settled":   types.PaymentCompleted,
		"settled":   types.PaymentCompleted,
		"Returned":  types.PaymentReturned,
		"Submitted": types.PaymentProcessing,
		"Voided":    types.PaymentCancelled,
		"Declined":  types.PaymentFailed,
		"weird":     types.PaymentPending, // unknown → pending, never dropped
	}
	for in, want := range cases {
		if got := mapPaymentStatus(in); got != want {
			t.Fatalf("mapPaymentStatus(%q): got %q, want %q", in, got, want)
		}
	}
}

// TestSplitName and TestFirstNonEmpty pin the small helpers that
// feed body construction.
func TestSplitName(t *testing.T) {
	cases := []struct {
		in           string
		first, last  string
	}{
		{"Jane Doe", "Jane", "Doe"},
		{"Jane Mary Doe", "Jane", "Mary Doe"},
		{"Cher", "Cher", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		f, l := splitName(c.in)
		if f != c.first || l != c.last {
			t.Fatalf("splitName(%q): got %q,%q want %q,%q", c.in, f, l, c.first, c.last)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "x", "y"); got != "x" {
		t.Fatalf("firstNonEmpty: got %q want x", got)
	}
	if got := firstNonEmpty(); got != "" {
		t.Fatalf("firstNonEmpty empty: got %q want empty", got)
	}
}

func TestMetaOrDefault(t *testing.T) {
	m := map[string]string{"k": "v"}
	if got := metaOrDefault(m, "k", "d"); got != "v" {
		t.Fatalf("metaOrDefault hit: %q", got)
	}
	if got := metaOrDefault(m, "missing", "d"); got != "d" {
		t.Fatalf("metaOrDefault miss: %q", got)
	}
	if got := metaOrDefault(nil, "k", "d"); got != "d" {
		t.Fatalf("metaOrDefault nil: %q", got)
	}
}
