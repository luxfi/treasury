package unit

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
	return &Provider{cfg: Config{BaseURL: srv.URL, Token: "k"}, client: srv.Client()}
}

func TestCreatePayment_CallerIdempotencyKey(t *testing.T) {
	var seen string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Idempotency-Key")
		_, _ = w.Write([]byte(`{"data":{"id":"p_1","attributes":{"amount":100,"direction":"Credit","status":"Pending"}}}`))
	})
	req := &types.CreatePaymentRequest{
		Direction: "Credit", Amount: "100", SourceAccountID: "a_1",
		IdempotencyKey: "caller-u",
	}
	if _, err := p.CreatePayment(context.Background(), req); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if seen != "caller-u" {
		t.Fatalf("Idempotency-Key: %q", seen)
	}
}

func TestCreatePayment_DeterministicFallback(t *testing.T) {
	var keys []string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		_, _ = w.Write([]byte(`{"data":{"id":"p_1","attributes":{"amount":100,"direction":"Credit","status":"Pending"}}}`))
	})
	req := &types.CreatePaymentRequest{
		Direction: "Credit", Amount: "100", SourceAccountID: "a_1", OrgID: "org-A",
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
