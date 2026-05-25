package moderntreasury

import (
	"context"
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
	return &Provider{cfg: Config{BaseURL: srv.URL, OrgID: "org", APIKey: "k"}, client: srv.Client()}
}

func TestCreatePayment_CallerIdempotencyKey(t *testing.T) {
	var seen string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/payment_orders") {
			t.Fatalf("path: %s", r.URL.Path)
		}
		seen = r.Header.Get("Idempotency-Key")
		_, _ = w.Write([]byte(`{"id":"po_1","type":"ach","amount":100,"currency":"USD","status":"pending"}`))
	})
	req := &types.CreatePaymentRequest{
		Type: types.PaymentACH, Direction: "credit", Amount: "100", Currency: "USD",
		SourceAccountID: "ia_1", IdempotencyKey: "caller-mt",
	}
	if _, err := p.CreatePayment(context.Background(), req); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if seen != "caller-mt" {
		t.Fatalf("Idempotency-Key: %q", seen)
	}
}

func TestCreatePayment_DeterministicFallback(t *testing.T) {
	var keys []string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		_, _ = w.Write([]byte(`{"id":"po_1","type":"ach","amount":100,"currency":"USD","status":"pending"}`))
	})
	req := &types.CreatePaymentRequest{
		Type: types.PaymentACH, Direction: "credit", Amount: "100", Currency: "USD",
		SourceAccountID: "ia_1", OrgID: "org-A",
	}
	for i := 0; i < 2; i++ {
		if _, err := p.CreatePayment(context.Background(), req); err != nil {
			t.Fatalf("CreatePayment: %v", err)
		}
	}
	if keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("expected identical deterministic keys, got %v", keys)
	}
}
