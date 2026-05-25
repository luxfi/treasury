package stripe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/luxfi/treasury/pkg/types"
)

func newTestProvider(t *testing.T, h http.HandlerFunc) *Provider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Provider{cfg: Config{BaseURL: srv.URL, SecretKey: "sk_test"}, client: srv.Client()}
}

// TestCreatePayment_CallerSuppliedIdempotencyKey: the caller-supplied
// IdempotencyKey reaches the upstream as the Idempotency-Key header.
func TestCreatePayment_CallerSuppliedIdempotencyKey(t *testing.T) {
	var seen string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Idempotency-Key")
		_, _ = w.Write([]byte(`{"id":"opay_1","amount":100,"currency":"usd","status":"pending"}`))
	})

	req := &types.CreatePaymentRequest{
		Amount:          "100",
		Currency:        "usd",
		SourceAccountID: "fa_1",
		IdempotencyKey:  "caller-key-abc",
	}
	if _, err := p.CreatePayment(context.Background(), req); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if seen != "caller-key-abc" {
		t.Fatalf("Idempotency-Key: got %q, want %q", seen, "caller-key-abc")
	}
}

// TestCreatePayment_RetryWithSamePayloadGetsSameKey: when the caller
// does not supply IdempotencyKey, the deterministic fallback must
// produce the SAME header value for the same (org, endpoint, body).
func TestCreatePayment_RetryWithSamePayloadGetsSameKey(t *testing.T) {
	var keys []string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		_, _ = w.Write([]byte(`{"id":"opay_1","amount":100,"currency":"usd","status":"pending"}`))
	})

	req := &types.CreatePaymentRequest{
		Amount: "100", Currency: "usd", SourceAccountID: "fa_1", OrgID: "org-A",
	}
	for i := 0; i < 3; i++ {
		if _, err := p.CreatePayment(context.Background(), req); err != nil {
			t.Fatalf("CreatePayment: %v", err)
		}
	}
	if len(keys) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(keys))
	}
	if keys[0] == "" {
		t.Fatal("deterministic fallback must produce a non-empty key")
	}
	if keys[0] != keys[1] || keys[1] != keys[2] {
		t.Fatalf("expected identical deterministic keys, got %v", keys)
	}
}

// TestCreatePayment_DifferentOrgGetsDifferentKey: same body / different
// org must not collide on a single provider key.
func TestCreatePayment_DifferentOrgGetsDifferentKey(t *testing.T) {
	var keys []string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		_, _ = w.Write([]byte(`{"id":"opay_1","amount":100,"currency":"usd","status":"pending"}`))
	})

	base := types.CreatePaymentRequest{
		Amount: "100", Currency: "usd", SourceAccountID: "fa_1",
	}
	for _, org := range []string{"org-A", "org-B"} {
		r := base
		r.OrgID = org
		if _, err := p.CreatePayment(context.Background(), &r); err != nil {
			t.Fatalf("CreatePayment: %v", err)
		}
	}
	if keys[0] == keys[1] {
		t.Fatalf("org-A and org-B must produce different keys, both got %q", keys[0])
	}
}

// Sanity: the body Stripe receives is form-encoded as expected.
func TestCreatePayment_FormEncoded(t *testing.T) {
	var body string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
			t.Fatalf("Content-Type: %q", ct)
		}
		_, _ = w.Write([]byte(`{"id":"opay_1","amount":100,"currency":"usd","status":"pending"}`))
	})
	req := &types.CreatePaymentRequest{
		Amount: "100", Currency: "usd", SourceAccountID: "fa_1", IdempotencyKey: "k",
	}
	if _, err := p.CreatePayment(context.Background(), req); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if !strings.Contains(body, "financial_account=fa_1") || !strings.Contains(body, "amount=100") {
		t.Fatalf("unexpected body: %q", body)
	}
}
