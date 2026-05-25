package mpesa

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luxfi/treasury/pkg/types"
)

func selfSignedCertPEM(t *testing.T) ([]byte, *rsa.PrivateKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mpesa-test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), priv
}

func newTestProvider(t *testing.T, h http.HandlerFunc) *Provider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	certPEM, _ := selfSignedCertPEM(t)
	return &Provider{
		cfg: Config{
			BaseURL:           srv.URL,
			ConsumerKey:       "ck",
			ConsumerSecret:    "cs",
			Shortcode:         "600000",
			InitiatorName:     "testapi",
			InitiatorPassword: "Safaricom999!*!",
			Passkey:           "bfb279f9aa9bdbcf158e97dd71a467cd2e0c893059b10f78e6b72ada1ed2c919",
			CallbackURL:       "https://example.test/cb",
			ResultURL:         "https://example.test/res",
			QueueTimeoutURL:   "https://example.test/to",
			Environment:       "sandbox",
			CertPEM:           certPEM,
		},
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func b2cReq(msisdn, amount, ref, direction string) types.CreatePaymentRequest {
	return types.CreatePaymentRequest{
		Direction: direction,
		Amount:    amount,
		Currency:  "KES",
		Reference: ref,
		Counterparty: &types.Counterparty{
			Name:          "test",
			AccountNumber: msisdn,
		},
	}
}

// --- OAuth ---

func TestOAuthTokenAcquisitionAndCache(t *testing.T) {
	var hits int32
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/oauth/v1/generate") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Basic ") {
			t.Fatalf("missing basic auth: %s", got)
		}
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"access_token":"tok-abc","expires_in":"3599"}`))
	})

	for i := 0; i < 3; i++ {
		tok, err := p.accessToken(context.Background())
		if err != nil {
			t.Fatalf("accessToken: %v", err)
		}
		if tok != "tok-abc" {
			t.Fatalf("token: %q", tok)
		}
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("expected 1 oauth call, got %d", n)
	}

	// Force refresh window (token expires within 60s).
	p.tokenExp = time.Now().Add(30 * time.Second)
	if _, err := p.accessToken(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Fatalf("expected 2 oauth calls after refresh, got %d", n)
	}
}

// --- msisdn normalization ---

func TestNormalizeMSISDN(t *testing.T) {
	cases := []struct {
		in, want string
		err      bool
	}{
		{"254712345678", "254712345678", false},
		{"0712345678", "254712345678", false},
		{"+254712345678", "254712345678", false},
		{"712345678", "254712345678", false},
		{"254 712 345 678", "254712345678", false},
		{"254112345678", "254112345678", false},
		{"112345678", "254112345678", false},
		{"15551234567", "", true},
		{"abc", "", true},
		{"25412345", "", true},
		{"254512345678", "", true},
	}
	for _, c := range cases {
		got, err := normalizeMSISDN(c.in)
		if c.err {
			if err == nil {
				t.Errorf("normalizeMSISDN(%q) expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeMSISDN(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("normalizeMSISDN(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- Security credential ---

func TestSecurityCredential(t *testing.T) {
	certPEM, priv := selfSignedCertPEM(t)
	p := &Provider{cfg: Config{InitiatorPassword: "Safaricom999!*!", CertPEM: certPEM}}
	enc, err := p.securityCredential()
	if err != nil {
		t.Fatalf("securityCredential: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("base64: %v", err)
	}
	plain, err := rsa.DecryptOAEP(sha1.New(), rand.Reader, priv, raw, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(plain) != "Safaricom999!*!" {
		t.Fatalf("decrypted %q", plain)
	}
}

// --- B2C ---

func TestCreatePaymentB2C(t *testing.T) {
	var captured map[string]interface{}
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/oauth/"):
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":"3599"}`))
		case r.URL.Path == "/mpesa/b2c/v3/paymentrequest":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &captured)
			_, _ = w.Write([]byte(`{"ConversationID":"AG_X","OriginatorConversationID":"orig-1","ResponseCode":"0","ResponseDescription":"Accept the service"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	req := b2cReq("254712345678", "1500", "salary-may", "credit")
	pay, err := p.CreatePayment(context.Background(), &req)
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if pay.Status != types.PaymentPending || pay.Currency != "KES" || pay.Amount != "1500" {
		t.Fatalf("payment shape: %+v", pay)
	}
	if pay.Meta["rail"] != "mpesa_b2c" || pay.Meta["msisdn"] != "254712345678" {
		t.Fatalf("meta: %+v", pay.Meta)
	}
	if captured["CommandID"] != "BusinessPayment" {
		t.Fatalf("CommandID: %v", captured["CommandID"])
	}
	if v, _ := captured["Amount"].(float64); v != 1500 {
		t.Fatalf("Amount: %v", captured["Amount"])
	}
	if captured["PartyA"] != "600000" || captured["PartyB"] != "254712345678" {
		t.Fatalf("parties: %v / %v", captured["PartyA"], captured["PartyB"])
	}
	if _, ok := captured["SecurityCredential"].(string); !ok {
		t.Fatal("missing SecurityCredential")
	}
}

// IdempotencyKey, when set, MUST take precedence over Reference for
// Daraja's OriginatorConversationID — Daraja's existing per-request
// dedupe key. Behaviour for callers who pre-date IdempotencyKey is
// preserved (Reference falls through when IdempotencyKey is empty).
func TestB2CIdempotencyKeyPrecedence(t *testing.T) {
	var captured map[string]interface{}
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/oauth/"):
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":"3599"}`))
		case r.URL.Path == "/mpesa/b2c/v3/paymentrequest":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &captured)
			_, _ = w.Write([]byte(`{"ConversationID":"AG","OriginatorConversationID":"k","ResponseCode":"0","ResponseDescription":"ok"}`))
		}
	})

	req := b2cReq("254712345678", "1500", "ref-fallback", "credit")
	req.IdempotencyKey = "idem-wins"
	if _, err := p.CreatePayment(context.Background(), &req); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if got := captured["OriginatorConversationID"]; got != "idem-wins" {
		t.Fatalf("OriginatorConversationID: got %v, want %q (IdempotencyKey must beat Reference)", got, "idem-wins")
	}

	// Same request, no IdempotencyKey → fall back to Reference.
	captured = nil
	req.IdempotencyKey = ""
	if _, err := p.CreatePayment(context.Background(), &req); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if got := captured["OriginatorConversationID"]; got != "ref-fallback" {
		t.Fatalf("OriginatorConversationID fallback: got %v, want %q", got, "ref-fallback")
	}
}

func TestB2CRejectsNonIntegerKES(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":"3599"}`))
	})
	req := b2cReq("254712345678", "1500.50", "ref", "credit")
	if _, err := p.CreatePayment(context.Background(), &req); err == nil {
		t.Fatal("expected error for decimal KES")
	}
}

func TestB2CRejectsNonKES(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":"3599"}`))
	})
	req := b2cReq("254712345678", "1500", "ref", "credit")
	req.Currency = "USD"
	if _, err := p.CreatePayment(context.Background(), &req); err == nil {
		t.Fatal("expected error for non-KES")
	}
}

// --- STK Push ---

func TestCreatePaymentSTKPush(t *testing.T) {
	var captured map[string]interface{}
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/oauth/"):
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":"3599"}`))
		case r.URL.Path == "/mpesa/stkpush/v1/processrequest":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &captured)
			_, _ = w.Write([]byte(`{"MerchantRequestID":"mr-1","CheckoutRequestID":"ws_co_1","ResponseCode":"0","ResponseDescription":"Success","CustomerMessage":"Sent"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	req := b2cReq("0712345678", "10", "INV-1", "debit")
	pay, err := p.CreatePayment(context.Background(), &req)
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if pay.ID != "ws_co_1" || pay.Meta["rail"] != "mpesa_c2b_stk" {
		t.Fatalf("payment: %+v", pay)
	}
	if captured["TransactionType"] != "CustomerPayBillOnline" {
		t.Fatalf("TransactionType: %v", captured["TransactionType"])
	}
	if captured["PhoneNumber"] != "254712345678" {
		t.Fatalf("PhoneNumber: %v", captured["PhoneNumber"])
	}
	pw, _ := captured["Password"].(string)
	ts, _ := captured["Timestamp"].(string)
	if pw == "" || ts == "" {
		t.Fatal("missing Password/Timestamp")
	}
	expected := base64.StdEncoding.EncodeToString([]byte("600000" + p.cfg.Passkey + ts))
	if pw != expected {
		t.Fatalf("password mismatch:\n got %s\nwant %s", pw, expected)
	}
}

func TestSTKPushTillVariant(t *testing.T) {
	var captured map[string]interface{}
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/oauth/"):
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":"3599"}`))
		case r.URL.Path == "/mpesa/stkpush/v1/processrequest":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &captured)
			_, _ = w.Write([]byte(`{"MerchantRequestID":"mr","CheckoutRequestID":"ws","ResponseCode":"0","ResponseDescription":"ok","CustomerMessage":"ok"}`))
		}
	})
	req := b2cReq("0712345678", "10", "INV", "debit")
	req.Meta = map[string]string{"till": "true"}
	if _, err := p.CreatePayment(context.Background(), &req); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if captured["TransactionType"] != "CustomerBuyGoodsOnline" {
		t.Fatalf("TransactionType: %v", captured["TransactionType"])
	}
}

// --- Transaction status ---

func TestGetPayment(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/oauth/"):
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":"3599"}`))
		case r.URL.Path == "/mpesa/transactionstatus/v1/query":
			_, _ = w.Write([]byte(`{"OriginatorConversationID":"orig-1","ConversationID":"AG_X","ResponseCode":"0","ResponseDescription":"Accepted"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})
	pay, err := p.GetPayment(context.Background(), "orig-1")
	if err != nil {
		t.Fatalf("GetPayment: %v", err)
	}
	if pay.Status != types.PaymentProcessing {
		t.Fatalf("status: %s", pay.Status)
	}
}

// --- Error envelope ---

func TestDarajaErrorEnvelope(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/oauth/"):
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":"3599"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"requestId":"1234-5678","errorCode":"500.001.1001","errorMessage":"Unable to lock subscriber"}`))
		}
	})
	req := b2cReq("254712345678", "10", "ref", "credit")
	_, err := p.CreatePayment(context.Background(), &req)
	if err == nil || !strings.Contains(err.Error(), "Unable to lock") {
		t.Fatalf("expected daraja error, got %v", err)
	}
}

// --- Unsupported surface ---

func TestUnsupportedMethods(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {})
	ctx := context.Background()
	if _, err := p.CreateAccount(ctx, "", nil); err == nil {
		t.Error("CreateAccount should be unsupported")
	}
	if _, err := p.CreateCounterparty(ctx, nil); err == nil {
		t.Error("CreateCounterparty should be unsupported")
	}
	if _, err := p.GetFXQuote(ctx, "KES", "USD", "1", "sell"); err == nil {
		t.Error("GetFXQuote should be unsupported")
	}
	if err := p.CancelPayment(ctx, "x"); err == nil {
		t.Error("CancelPayment should be unsupported")
	}
	if _, err := p.ListPayments(ctx, ""); err == nil {
		t.Error("ListPayments should be unsupported")
	}
	if _, err := p.ListCounterparties(ctx); err == nil {
		t.Error("ListCounterparties should be unsupported")
	}
	if _, err := p.ListFXConversions(ctx); err == nil {
		t.Error("ListFXConversions should be unsupported")
	}
	if _, err := p.CreateFXConversion(ctx, nil); err == nil {
		t.Error("CreateFXConversion should be unsupported")
	}
}

func TestCapabilities(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {})
	c := p.Capabilities()
	if c.Name != "mpesa" || len(c.Currencies) != 1 || c.Currencies[0] != "KES" {
		t.Fatalf("capabilities: %+v", c)
	}
}

// --- webhook parser ---

func TestParseCallbackSTKSuccess(t *testing.T) {
	body := []byte(`{
		"Body": {
			"stkCallback": {
				"MerchantRequestID": "mr-1",
				"CheckoutRequestID": "ws_co_1",
				"ResultCode": 0,
				"ResultDesc": "The service request is processed successfully.",
				"CallbackMetadata": {
					"Item": [
						{"Name":"Amount","Value":1500.00},
						{"Name":"MpesaReceiptNumber","Value":"NLJ7RT61SV"},
						{"Name":"PhoneNumber","Value":254712345678}
					]
				}
			}
		}
	}`)
	ev, err := ParseCallback(body)
	if err != nil {
		t.Fatalf("ParseCallback: %v", err)
	}
	if ev.EventType != "stk_callback" || ev.Status != "completed" {
		t.Fatalf("event: %+v", ev)
	}
	if ev.ExternalID != "ws_co_1" || ev.MSISDN != "254712345678" || ev.Amount != "1500" {
		t.Fatalf("event fields: %+v", ev)
	}
}

func TestParseCallbackSTKFailure(t *testing.T) {
	body := []byte(`{"Body":{"stkCallback":{"MerchantRequestID":"x","CheckoutRequestID":"y","ResultCode":1032,"ResultDesc":"Request cancelled by user"}}}`)
	ev, err := ParseCallback(body)
	if err != nil {
		t.Fatalf("ParseCallback: %v", err)
	}
	if ev.Status != "failed" || ev.ResultCode != 1032 {
		t.Fatalf("event: %+v", ev)
	}
}

func TestParseCallbackB2CResult(t *testing.T) {
	body := []byte(`{"Result":{"ResultType":0,"ResultCode":0,"ResultDesc":"The service request is processed successfully.","OriginatorConversationID":"orig-1","ConversationID":"AG_X","TransactionID":"LK451H35OP","ResultParameters":{"ResultParameter":[{"Key":"TransactionAmount","Value":1500},{"Key":"ReceiverPartyPublicName","Value":"254712345678 - John Doe"}]}}}`)
	ev, err := ParseCallback(body)
	if err != nil {
		t.Fatalf("ParseCallback: %v", err)
	}
	if ev.Status != "completed" || ev.ExternalID != "orig-1" || ev.Amount != "1500" || ev.MSISDN != "254712345678" {
		t.Fatalf("event: %+v", ev)
	}
}

func TestParseCallbackEmpty(t *testing.T) {
	if _, err := ParseCallback(nil); err == nil {
		t.Fatal("expected error for empty body")
	}
	if _, err := ParseCallback([]byte(`{"unknown":1}`)); err == nil {
		t.Fatal("expected error for unknown shape")
	}
}
