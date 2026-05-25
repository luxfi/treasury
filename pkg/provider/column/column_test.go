package column

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luxfi/treasury/pkg/types"
)

func newTestProvider(t *testing.T, h http.HandlerFunc) *Provider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Provider{cfg: Config{BaseURL: srv.URL, APIKey: "k"}, client: srv.Client()}
}

func TestCreatePayment_CallerIdempotencyKey(t *testing.T) {
	var seen string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Idempotency-Key")
		_, _ = w.Write([]byte(`{"id":"acht_1","amount":100,"currency_code":"USD","status":"pending"}`))
	})
	req := &types.CreatePaymentRequest{
		Type: types.PaymentACH, Amount: "100", Currency: "USD",
		SourceAccountID: "bacc_1", IdempotencyKey: "caller-1",
	}
	if _, err := p.CreatePayment(context.Background(), req); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if seen != "caller-1" {
		t.Fatalf("Idempotency-Key: got %q", seen)
	}
}

func TestCreatePayment_DeterministicFallback(t *testing.T) {
	var keys []string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		_, _ = w.Write([]byte(`{"id":"acht_1","amount":100,"currency_code":"USD","status":"pending"}`))
	})
	req := &types.CreatePaymentRequest{
		Type: types.PaymentACH, Amount: "100", Currency: "USD",
		SourceAccountID: "bacc_1", OrgID: "org-A",
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
