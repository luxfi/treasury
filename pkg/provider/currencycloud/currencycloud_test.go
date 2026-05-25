package currencycloud

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/luxfi/treasury/pkg/types"
)

func newTestProvider(t *testing.T, h http.HandlerFunc) *Provider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Provider{
		cfg:       Config{BaseURL: srv.URL, LoginID: "id", APIKey: "k"},
		client:    srv.Client(),
		authToken: "tok",
		tokenExp:  time.Now().Add(time.Hour),
	}
}

func parseFormBody(t *testing.T, r *http.Request) url.Values {
	t.Helper()
	b, _ := io.ReadAll(r.Body)
	v, err := url.ParseQuery(string(b))
	if err != nil {
		t.Fatalf("parse form: %v", err)
	}
	return v
}

// CurrencyCloud carries idempotency on the form field unique_request_id.
func TestCreatePayment_CallerIdempotencyKey_FormField(t *testing.T) {
	var seen string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v2/payments/create") {
			t.Fatalf("path: %s", r.URL.Path)
		}
		seen = parseFormBody(t, r).Get("unique_request_id")
		_, _ = w.Write([]byte(`{"id":"p_1","amount":"100","currency":"USD","status":"pending","created_at":""}`))
	})
	req := &types.CreatePaymentRequest{
		Amount: "100", Currency: "USD",
		IdempotencyKey: "caller-cc",
	}
	if _, err := p.CreatePayment(context.Background(), req); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if seen != "caller-cc" {
		t.Fatalf("unique_request_id: %q", seen)
	}
}

func TestCreatePayment_DeterministicFallback(t *testing.T) {
	var keys []string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, parseFormBody(t, r).Get("unique_request_id"))
		_, _ = w.Write([]byte(`{"id":"p_1","amount":"100","currency":"USD","status":"pending","created_at":""}`))
	})
	req := &types.CreatePaymentRequest{
		Amount: "100", Currency: "USD", OrgID: "org-A",
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

func TestCreateFXConversion_CallerIdempotencyKey_FormField(t *testing.T) {
	var seen string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v2/conversions/create") {
			t.Fatalf("path: %s", r.URL.Path)
		}
		seen = parseFormBody(t, r).Get("unique_request_id")
		_, _ = w.Write([]byte(`{"id":"c_1","sell_currency":"USD","buy_currency":"EUR","client_sell_amount":"100","client_buy_amount":"92","client_rate":"0.92","settlement_date":"","status":"pending","created_at":""}`))
	})
	req := &types.CreateFXConversionRequest{
		SellCurrency: "USD", BuyCurrency: "EUR", Amount: "100", FixedSide: "sell",
		IdempotencyKey: "caller-cc-fx",
	}
	if _, err := p.CreateFXConversion(context.Background(), req); err != nil {
		t.Fatalf("CreateFXConversion: %v", err)
	}
	if seen != "caller-cc-fx" {
		t.Fatalf("unique_request_id: %q", seen)
	}
}
