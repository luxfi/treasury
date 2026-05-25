package wise

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
	return &Provider{cfg: Config{BaseURL: srv.URL, APIToken: "t", ProfileID: "p"}, client: srv.Client()}
}

// Wise CreatePayment does two POSTs: quote → transfer. Only the transfer
// must carry X-idempotence-uuid (it's the money-moving call).
func TestCreatePayment_CallerIdempotencyKeyOnTransferOnly(t *testing.T) {
	var quoteHdr, transferHdr string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/quotes"):
			quoteHdr = r.Header.Get("X-idempotence-uuid")
			_, _ = w.Write([]byte(`{"id":"quote-1"}`))
		case strings.HasSuffix(r.URL.Path, "/transfers"):
			transferHdr = r.Header.Get("X-idempotence-uuid")
			_, _ = w.Write([]byte(`{"id":1,"status":"incoming_payment_waiting","sourceCurrency":"USD","sourceValue":100,"targetCurrency":"USD","targetValue":100,"rate":1.0}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	})
	req := &types.CreatePaymentRequest{
		Amount: "100", Currency: "USD", DestAccountID: "999",
		IdempotencyKey: "caller-wise",
	}
	if _, err := p.CreatePayment(context.Background(), req); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if quoteHdr != "" {
		t.Errorf("quote must not carry X-idempotence-uuid, got %q", quoteHdr)
	}
	if transferHdr != "caller-wise" {
		t.Fatalf("transfer X-idempotence-uuid: got %q, want %q", transferHdr, "caller-wise")
	}
}

func TestCreatePayment_DeterministicFallback(t *testing.T) {
	var keys []string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/quotes"):
			_, _ = w.Write([]byte(`{"id":"quote-1"}`))
		case strings.HasSuffix(r.URL.Path, "/transfers"):
			keys = append(keys, r.Header.Get("X-idempotence-uuid"))
			_, _ = w.Write([]byte(`{"id":1,"status":"incoming_payment_waiting","sourceCurrency":"USD","sourceValue":100,"targetCurrency":"USD","targetValue":100,"rate":1.0}`))
		}
	})
	req := &types.CreatePaymentRequest{
		Amount: "100", Currency: "USD", DestAccountID: "999", OrgID: "org-A",
	}
	for i := 0; i < 2; i++ {
		if _, err := p.CreatePayment(context.Background(), req); err != nil {
			t.Fatalf("CreatePayment: %v", err)
		}
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 transfer calls, got %d", len(keys))
	}
	if keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("expected identical deterministic keys, got %v", keys)
	}
}
