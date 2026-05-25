package square

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luxfi/treasury/pkg/types"
)

func newTestProvider(t *testing.T, h http.HandlerFunc) *Provider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Provider{cfg: Config{BaseURL: srv.URL, AccessToken: "k"}, client: srv.Client()}
}

// Square uses a JSON body field, not a header.
func TestCreatePayment_CallerIdempotencyKey_BodyField(t *testing.T) {
	var seen string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var got map[string]interface{}
		_ = json.Unmarshal(b, &got)
		seen, _ = got["idempotency_key"].(string)
		_, _ = w.Write([]byte(`{"payment":{"id":"sq_1","status":"COMPLETED","amount_money":{"amount":100,"currency":"USD"}}}`))
	})
	req := &types.CreatePaymentRequest{
		Amount: "100", Currency: "USD", SourceAccountID: "src",
		IdempotencyKey: "caller-sq",
	}
	if _, err := p.CreatePayment(context.Background(), req); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if seen != "caller-sq" {
		t.Fatalf("idempotency_key body field: %q", seen)
	}
}

func TestCreatePayment_DeterministicFallback_StableAcrossRetries(t *testing.T) {
	var seen []string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var got map[string]interface{}
		_ = json.Unmarshal(b, &got)
		k, _ := got["idempotency_key"].(string)
		seen = append(seen, k)
		_, _ = w.Write([]byte(`{"payment":{"id":"sq_1","status":"COMPLETED","amount_money":{"amount":100,"currency":"USD"}}}`))
	})
	req := &types.CreatePaymentRequest{
		Amount: "100", Currency: "USD", SourceAccountID: "src", OrgID: "org-A",
	}
	for i := 0; i < 3; i++ {
		if _, err := p.CreatePayment(context.Background(), req); err != nil {
			t.Fatalf("CreatePayment: %v", err)
		}
	}
	if seen[0] == "" || seen[0] != seen[1] || seen[1] != seen[2] {
		t.Fatalf("expected identical deterministic body-field keys, got %v", seen)
	}
}
