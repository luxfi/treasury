// Source-of-design: Public-Spec
// Source-ref: https://transactapi.readme.io/reference
//
// Conformance suite for the TransactAPI treasury-side adapter.
// Every test uses httptest fixtures; no production HTTP traffic.
// The fixtures replay the documented TAPI v3 envelope shapes and
// the sandbox-observed flat shapes side by side, so a fixture drift
// in either dimension trips the suite.

package northcapital

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luxfi/treasury/pkg/provider"
	"github.com/luxfi/treasury/pkg/types"
)

// --- fixture utilities ---

// captured wraps a single recorded request body for assertions.
type captured struct {
	verb    string
	body    map[string]any
	headers http.Header
}

func mustReadJSON(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	out := map[string]any{}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("unmarshal body: %v: %s", err, string(b))
		}
	}
	return out
}

// verbFromPath extracts the TAPI verb from the request URL.
func verbFromPath(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// newRouter installs a per-verb handler map onto a single httptest
// server so a single test can exercise multiple endpoints.
func newRouter(t *testing.T, handlers map[string]func(*testing.T, *http.Request, http.ResponseWriter)) (*Provider, *[]captured) {
	t.Helper()
	recs := &[]captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verb := verbFromPath(r.URL.Path)
		h, ok := handlers[verb]
		if !ok {
			t.Fatalf("unexpected verb: %s (path %s)", verb, r.URL.Path)
		}
		body := mustReadJSON(t, r)
		*recs = append(*recs, captured{
			verb:    verb,
			body:    body,
			headers: r.Header.Clone(),
		})
		// Reset body for handler to re-read via fresh handler logic.
		r.Body = io.NopCloser(strings.NewReader(""))
		h(t, r, w)
	}))
	t.Cleanup(srv.Close)
	p := &Provider{
		cfg: Config{
			BaseURL:         srv.URL,
			ClientID:        "test-client",
			DeveloperAPIKey: "test-key",
			HTTPClient:      srv.Client(),
			MaxRetries:      2,
		},
		client: srv.Client(),
	}
	return p, recs
}

// writeJSON is a tiny helper for the fixture handlers.
func writeJSON(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc, _ := json.Marshal(body)
	_, _ = w.Write(enc)
}

// --- CreatePayment — five payment-type happy paths ---

// TestConformance_CreatePayment_ACH exercises the externalFundMove
// rail with a full body and asserts that the response RefNum is
// hoisted into types.Payment.ID/ProviderID and that credentials +
// idempotency header are present on the request.
func TestConformance_CreatePayment_ACH(t *testing.T) {
	p, recs := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"externalFundMove": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{
				"statusCode": "101",
				"statusDesc": "Ok. External Fund Move added",
				"RefNum":     "EXT-ACH-001",
				"Status":     "Submitted",
			})
		},
	})

	req := &types.CreatePaymentRequest{
		Type:            types.PaymentACH,
		Direction:       "credit",
		Amount:          "1000.00",
		Currency:        "USD",
		SourceAccountID: "ACC-1",
		OrgID:           "org-A",
		Description:     "test ACH",
		Counterparty:    &types.Counterparty{ID: "P-1", Name: "Jane Doe"},
		Meta: map[string]string{
			"offeringId":       "555",
			"tradeId":          "888",
			"createdIpAddress": "127.0.0.1",
		},
	}
	pay, err := p.CreatePayment(context.Background(), req)
	if err != nil {
		t.Fatalf("CreatePayment ACH: %v", err)
	}
	if pay.ID != "EXT-ACH-001" || pay.ProviderID != "EXT-ACH-001" {
		t.Fatalf("ID: got %q", pay.ID)
	}
	if pay.Status != types.PaymentProcessing { // Submitted → Processing
		t.Fatalf("Status: got %q", pay.Status)
	}
	if pay.Type != types.PaymentACH {
		t.Fatalf("Type: got %q", pay.Type)
	}

	if len(*recs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*recs))
	}
	rec := (*recs)[0]
	if rec.body["clientID"] != "test-client" {
		t.Fatalf("missing clientID: %v", rec.body)
	}
	if rec.body["developerAPIKey"] != "test-key" {
		t.Fatalf("missing developerAPIKey: %v", rec.body)
	}
	if rec.body["accountId"] != "ACC-1" || rec.body["amount"] != "1000.00" {
		t.Fatalf("body fields: %v", rec.body)
	}
	if rec.body["offeringId"] != "555" || rec.body["tradeId"] != "888" {
		t.Fatalf("ACH meta fields lost: %v", rec.body)
	}
	if rec.headers.Get(idempotencyHeader) == "" {
		t.Fatal("missing X-Idempotency-Key header")
	}
}

// TestConformance_CreatePayment_CreditCard exercises the
// ccFundMovement rail and asserts that the credit-card meta fields
// (creditCardName, creditCardNumber, expirationDate, cvv) get folded
// into the body.
func TestConformance_CreatePayment_CreditCard(t *testing.T) {
	p, recs := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"ccFundMovement": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{
				"statusCode": "101",
				"RefNum":     "CC-001",
				"Status":     "Settled",
			})
		},
	})

	req := &types.CreatePaymentRequest{
		Type:            types.PaymentCard,
		Amount:          "499.00",
		Currency:        "USD",
		SourceAccountID: "ACC-2",
		OrgID:           "org-A",
		Meta: map[string]string{
			"creditCardName":   "Jane Doe",
			"creditCardNumber": "4111111111111111",
			"expirationDate":   "12/30",
			"cvv":              "123",
			"billingZip":       "94110",
		},
	}
	pay, err := p.CreatePayment(context.Background(), req)
	if err != nil {
		t.Fatalf("CreatePayment CC: %v", err)
	}
	if pay.ID != "CC-001" || pay.Status != types.PaymentCompleted {
		t.Fatalf("CC payment: %+v", pay)
	}
	rec := (*recs)[0]
	for _, k := range []string{"creditCardName", "creditCardNumber", "expirationDate", "cvv", "billingZip"} {
		if rec.body[k] == "" {
			t.Fatalf("CC field %s missing: %v", k, rec.body)
		}
	}
}

// TestConformance_CreatePayment_Wire exercises the
// createCustodyFundMove rail with the wire fund-move type.
func TestConformance_CreatePayment_Wire(t *testing.T) {
	p, recs := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"createCustodyFundMove": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{
				"statusCode":      "101",
				"referenceNumber": "WIRE-001",
				"status":          "Pending",
			})
		},
	})
	req := &types.CreatePaymentRequest{
		Type:            types.PaymentWire,
		Amount:          "50000",
		Currency:        "USD",
		SourceAccountID: "ACC-3",
		OrgID:           "org-A",
	}
	pay, err := p.CreatePayment(context.Background(), req)
	if err != nil {
		t.Fatalf("CreatePayment Wire: %v", err)
	}
	if pay.ID != "WIRE-001" || pay.Status != types.PaymentPending {
		t.Fatalf("Wire payment: %+v", pay)
	}
	if (*recs)[0].body["fundMoveType"] != "wire" {
		t.Fatalf("fundMoveType: %v", (*recs)[0].body["fundMoveType"])
	}
}

// TestConformance_CreatePayment_Check exercises check via custody.
func TestConformance_CreatePayment_Check(t *testing.T) {
	p, _ := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"createCustodyFundMove": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{
				"statusCode":      "101",
				"referenceNumber": "CHK-001",
				"status":          "Submitted",
			})
		},
	})
	req := &types.CreatePaymentRequest{
		Type:            types.PaymentCheck,
		Amount:          "2500",
		Currency:        "USD",
		SourceAccountID: "ACC-4",
		OrgID:           "org-A",
	}
	pay, err := p.CreatePayment(context.Background(), req)
	if err != nil {
		t.Fatalf("CreatePayment Check: %v", err)
	}
	if pay.ID != "CHK-001" || pay.Status != types.PaymentProcessing {
		t.Fatalf("Check payment: %+v", pay)
	}
}

// TestConformance_CreatePayment_IRA exercises IRA via custody.
func TestConformance_CreatePayment_IRA(t *testing.T) {
	p, recs := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"createCustodyFundMove": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{
				"statusCode":      "101",
				"referenceNumber": "IRA-001",
				"status":          "Pending",
			})
		},
	})
	req := &types.CreatePaymentRequest{
		Type:            "ira",
		Amount:          "6500",
		Currency:        "USD",
		SourceAccountID: "ACC-5",
		OrgID:           "org-A",
	}
	pay, err := p.CreatePayment(context.Background(), req)
	if err != nil {
		t.Fatalf("CreatePayment IRA: %v", err)
	}
	if pay.ID != "IRA-001" {
		t.Fatalf("IRA payment: %+v", pay)
	}
	if (*recs)[0].body["fundMoveType"] != "ira" {
		t.Fatalf("fundMoveType: %v", (*recs)[0].body["fundMoveType"])
	}
}

// TestConformance_CreatePayment_UnsupportedType locks the
// unsupported-type guard.
func TestConformance_CreatePayment_UnsupportedType(t *testing.T) {
	p := New(Config{BaseURL: SandboxURL, ClientID: "x", DeveloperAPIKey: "y"})
	_, err := p.CreatePayment(context.Background(), &types.CreatePaymentRequest{
		Type: types.PaymentSEPA, // SEPA is currencycloud territory
	})
	if err == nil {
		t.Fatal("expected unsupported-type error")
	}
}

// --- GetPayment / ListPayments / CancelPayment / RefundPayment ---

// TestConformance_GetPayment_FallthroughChain exercises the
// external → cc → custody fallthrough that GetPayment uses to
// resolve a RefNum on any of the three surfaces.
func TestConformance_GetPayment_FallthroughChain(t *testing.T) {
	// Only custody returns a populated record; external + cc return
	// empty envelopes (200 with no RefNum).
	p, _ := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"getExternalFundMoveInfo": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{
				"statusCode": "204",
				"statusDesc": "No record",
			})
		},
		"getCCFundMoveInfo": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{
				"statusCode": "204",
				"statusDesc": "No record",
			})
		},
		"getCustodyFundMove": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{
				"statusCode":      "101",
				"referenceNumber": "WIRE-001",
				"status":          "Settled",
				"amount":          "50000",
				"currency":        "USD",
			})
		},
	})
	pay, err := p.GetPayment(context.Background(), "WIRE-001")
	if err != nil {
		t.Fatalf("GetPayment: %v", err)
	}
	if pay.ID != "WIRE-001" || pay.Status != types.PaymentCompleted {
		t.Fatalf("GetPayment shape: %+v", pay)
	}
	if pay.Amount != "50000" {
		t.Fatalf("Amount: %q", pay.Amount)
	}
}

// TestConformance_GetPayment_NotFound exercises the not-found path.
func TestConformance_GetPayment_NotFound(t *testing.T) {
	p, _ := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"getExternalFundMoveInfo": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{"statusCode": "204"})
		},
		"getCCFundMoveInfo": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{"statusCode": "204"})
		},
		"getCustodyFundMove": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{"statusCode": "204"})
		},
	})
	if _, err := p.GetPayment(context.Background(), "X"); err == nil {
		t.Fatal("expected not-found error")
	}
}

// TestConformance_ListPayments paginates two pages of three payments
// each, then a final empty page to terminate.
func TestConformance_ListPayments(t *testing.T) {
	var call int32
	p, _ := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"getAllExternalFundMoves": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			n := atomic.AddInt32(&call, 1)
			switch n {
			case 1:
				writeJSON(t, w, 200, map[string]any{
					"data": []map[string]any{
						{"RefNum": "1", "status": "Pending", "amount": "100", "currency": "USD"},
						{"RefNum": "2", "status": "Settled", "amount": "200", "currency": "USD"},
						{"RefNum": "3", "status": "Voided", "amount": "300", "currency": "USD"},
					},
				})
			case 2:
				writeJSON(t, w, 200, map[string]any{
					"data": []map[string]any{
						{"RefNum": "4", "status": "Settled", "amount": "400", "currency": "USD"},
					},
				})
			default:
				writeJSON(t, w, 200, map[string]any{"data": []map[string]any{}})
			}
		},
	})
	out, err := p.ListPayments(context.Background(), "ACC-1")
	if err != nil {
		t.Fatalf("ListPayments: %v", err)
	}
	if len(out) != 4 {
		t.Fatalf("expected 4 payments across pages, got %d", len(out))
	}
	if out[0].ID != "1" || out[3].ID != "4" {
		t.Fatalf("paging order: %+v", out)
	}
	if out[2].Status != types.PaymentCancelled {
		t.Fatalf("status mapping: %q", out[2].Status)
	}
	if atomic.LoadInt32(&call) != 3 {
		t.Fatalf("expected 3 requests (two with data + one empty), got %d", call)
	}
}

// TestConformance_CancelPayment fires the void path and asserts the
// status=void body field is set.
func TestConformance_CancelPayment(t *testing.T) {
	p, recs := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"updateCustodyFundMove": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{
				"statusCode": "101",
				"statusDesc": "Ok. Custody fund move updated",
			})
		},
	})
	if err := p.CancelPayment(context.Background(), "WIRE-001"); err != nil {
		t.Fatalf("CancelPayment: %v", err)
	}
	if (*recs)[0].body["status"] != "void" {
		t.Fatalf("expected status=void, got %v", (*recs)[0].body["status"])
	}
	if (*recs)[0].body["referenceNumber"] != "WIRE-001" {
		t.Fatalf("referenceNumber: %v", (*recs)[0].body["referenceNumber"])
	}
}

// TestConformance_RefundPayment fires the refund path and asserts
// status=refund body field plus amount/note pass-through.
func TestConformance_RefundPayment(t *testing.T) {
	p, recs := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"updateCustodyFundMove": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{
				"statusCode":      "101",
				"referenceNumber": "REF-001",
				"status":          "Pending",
				"amount":          "500",
				"currency":        "USD",
			})
		},
	})
	refund, err := p.RefundPayment(context.Background(), "WIRE-001", "500", "client requested")
	if err != nil {
		t.Fatalf("RefundPayment: %v", err)
	}
	if refund.ID != "REF-001" || refund.PaymentID != "WIRE-001" || refund.Amount != "500" {
		t.Fatalf("refund: %+v", refund)
	}
	body := (*recs)[0].body
	if body["status"] != "refund" || body["amount"] != "500" || body["note"] != "client requested" {
		t.Fatalf("refund body: %v", body)
	}
}

// --- CreateAccount / GetAccount / ListAccounts / GetBalance ---

// TestConformance_CreateAccount exercises the createAccount verb and
// asserts that the response accountId surfaces into BankAccount.ID
// and that orgID is preserved.
func TestConformance_CreateAccount(t *testing.T) {
	p, recs := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"createAccount": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{
				"statusCode": "101",
				"accountId":  "ACC-NEW",
				"Status":     "Pending",
			})
		},
	})
	req := &provider.CreateAccountRequest{
		AccountName: "Test Custody",
		Currency:    "USD",
		AccountType: "custody",
		Country:     "USA",
	}
	acct, err := p.CreateAccount(context.Background(), "org-A", req)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if acct.ID != "ACC-NEW" || acct.OrgID != "org-A" {
		t.Fatalf("acct: %+v", acct)
	}
	if (*recs)[0].body["accountRegistration"] != "Test Custody" {
		t.Fatalf("accountRegistration: %v", (*recs)[0].body)
	}
	if (*recs)[0].headers.Get(idempotencyHeader) == "" {
		t.Fatal("missing X-Idempotency-Key header on CreateAccount")
	}
}

// TestConformance_CreateAccount_MissingAccountID exercises the
// degenerate response (200 but no accountId) — we surface a clear
// error rather than fabricating an ID.
func TestConformance_CreateAccount_MissingAccountID(t *testing.T) {
	p, _ := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"createAccount": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{
				"statusCode": "101",
				"statusDesc": "Account created but ID missing",
			})
		},
	})
	if _, err := p.CreateAccount(context.Background(), "org-A", &provider.CreateAccountRequest{
		AccountName: "X",
	}); err == nil {
		t.Fatal("expected missing-accountId error")
	}
}

// TestConformance_GetAccount round-trips an account fetch.
func TestConformance_GetAccount(t *testing.T) {
	p, _ := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"getAccount": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{
				"statusCode": "101",
				"accountId":  "ACC-1",
				"Status":     "active",
			})
		},
	})
	acct, err := p.GetAccount(context.Background(), "ACC-1")
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if acct.ID != "ACC-1" || acct.Status != "active" {
		t.Fatalf("acct: %+v", acct)
	}
}

// TestConformance_ListAccounts paginates two pages.
func TestConformance_ListAccounts(t *testing.T) {
	var call int32
	p, _ := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"getAllAccounts": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			n := atomic.AddInt32(&call, 1)
			switch n {
			case 1:
				writeJSON(t, w, 200, map[string]any{
					"data": []map[string]any{
						{"accountId": "A1", "accountRegistration": "One", "approvalStatus": "approved"},
						{"accountId": "A2", "accountRegistration": "Two", "approvalStatus": "approved"},
					},
				})
			default:
				writeJSON(t, w, 200, map[string]any{"data": []map[string]any{}})
			}
		},
	})
	out, err := p.ListAccounts(context.Background(), "org-A")
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}
	if out[0].OrgID != "org-A" {
		t.Fatalf("OrgID propagation: %+v", out[0])
	}
	if atomic.LoadInt32(&call) != 2 {
		t.Fatalf("expected 2 calls (data + empty), got %d", call)
	}
}

// TestConformance_GetBalance reads a custody balance via the flat
// envelope shape.
func TestConformance_GetBalance(t *testing.T) {
	p, _ := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"getCustodyBalances": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{
				"statusCode": "101",
				"available":  "1000.00",
				"current":    "1250.00",
				"currency":   "USD",
			})
		},
	})
	bal, err := p.GetBalance(context.Background(), "ACC-1")
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if bal.Available != "1000.00" || bal.Current != "1250.00" || bal.Currency != "USD" {
		t.Fatalf("balance: %+v", bal)
	}
}

// TestConformance_GetBalance_NestedShape exercises the alternate
// nested {"balance":{...}} envelope used by some sandbox responses.
func TestConformance_GetBalance_NestedShape(t *testing.T) {
	p, _ := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"getCustodyBalances": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{
				"statusCode": "101",
				"balance": map[string]any{
					"available": "42",
					"current":   "42",
					"currency":  "USD",
				},
			})
		},
	})
	bal, err := p.GetBalance(context.Background(), "ACC-1")
	if err != nil {
		t.Fatalf("GetBalance nested: %v", err)
	}
	if bal.Available != "42" || bal.Current != "42" {
		t.Fatalf("nested balance: %+v", bal)
	}
}

// --- Counterparties ---

// TestConformance_CreateCounterparty maps a Lux Counterparty into a
// TAPI Party and reads back the partyId.
func TestConformance_CreateCounterparty(t *testing.T) {
	p, recs := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"createParty": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{
				"statusCode": "101",
				"partyId":    "PARTY-1",
			})
		},
	})
	cp := &types.Counterparty{
		Name:        "Jane Doe",
		Email:       "jane@example.com",
		BankCountry: "USA",
	}
	out, err := p.CreateCounterparty(context.Background(), cp)
	if err != nil {
		t.Fatalf("CreateCounterparty: %v", err)
	}
	if out.ID != "PARTY-1" {
		t.Fatalf("party: %+v", out)
	}
	if (*recs)[0].body["firstName"] != "Jane" || (*recs)[0].body["lastName"] != "Doe" {
		t.Fatalf("name split: %v", (*recs)[0].body)
	}
}

// TestConformance_ListCounterparties paginates parties.
func TestConformance_ListCounterparties(t *testing.T) {
	var call int32
	p, _ := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"getAllParties": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			n := atomic.AddInt32(&call, 1)
			if n == 1 {
				writeJSON(t, w, 200, map[string]any{
					"data": []map[string]any{
						{"partyId": "P1", "firstName": "Jane", "lastName": "Doe", "emailAddress": "jd@example.com"},
					},
				})
				return
			}
			writeJSON(t, w, 200, map[string]any{"data": []map[string]any{}})
		},
	})
	out, err := p.ListCounterparties(context.Background())
	if err != nil {
		t.Fatalf("ListCounterparties: %v", err)
	}
	if len(out) != 1 || out[0].ID != "P1" || out[0].Name != "Jane Doe" {
		t.Fatalf("parties: %+v", out)
	}
}

// --- Idempotency contract ---

// TestConformance_Idempotency_CallerOverride asserts that a
// caller-supplied IdempotencyKey wins over the deterministic
// fallback.
func TestConformance_Idempotency_CallerOverride(t *testing.T) {
	p, recs := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"externalFundMove": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 200, map[string]any{"statusCode": "101", "RefNum": "X", "Status": "Submitted"})
		},
	})
	req := &types.CreatePaymentRequest{
		Type:            types.PaymentACH,
		Amount:          "100",
		Currency:        "USD",
		SourceAccountID: "ACC-1",
		OrgID:           "org-A",
		IdempotencyKey:  "caller-supplied-XYZ",
	}
	if _, err := p.CreatePayment(context.Background(), req); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if got := (*recs)[0].headers.Get(idempotencyHeader); got != "caller-supplied-XYZ" {
		t.Fatalf("X-Idempotency-Key: got %q, want caller-supplied-XYZ", got)
	}
}

// TestConformance_Idempotency_DeterministicFallback asserts that an
// unset caller key produces a stable derived key — two retries of
// the same logical request collapse to the same key.
func TestConformance_Idempotency_DeterministicFallback(t *testing.T) {
	var keys []string
	p, _ := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"externalFundMove": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			keys = append(keys, r.Header.Get(idempotencyHeader))
			writeJSON(t, w, 200, map[string]any{"statusCode": "101", "RefNum": "X", "Status": "Submitted"})
		},
	})
	req := &types.CreatePaymentRequest{
		Type:            types.PaymentACH,
		Amount:          "100",
		Currency:        "USD",
		SourceAccountID: "ACC-1",
		OrgID:           "org-A",
		Meta: map[string]string{
			"offeringId": "1", "tradeId": "1", "createdIpAddress": "1.1.1.1",
		},
	}
	for i := 0; i < 3; i++ {
		if _, err := p.CreatePayment(context.Background(), req); err != nil {
			t.Fatalf("CreatePayment attempt %d: %v", i, err)
		}
	}
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	if keys[0] == "" {
		t.Fatal("derived key empty")
	}
	if keys[0] != keys[1] || keys[1] != keys[2] {
		t.Fatalf("derived keys not stable: %v", keys)
	}
	if len(keys[0]) != 64 {
		t.Fatalf("derived key length: got %d, want 64 (sha256 hex)", len(keys[0]))
	}
}

// TestConformance_Idempotency_DerivationDepends asserts that
// changing OrgID or body content produces a different derived key.
func TestConformance_Idempotency_DerivationDepends(t *testing.T) {
	var keys []string
	p, _ := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"externalFundMove": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			keys = append(keys, r.Header.Get(idempotencyHeader))
			writeJSON(t, w, 200, map[string]any{"statusCode": "101", "RefNum": "X", "Status": "Submitted"})
		},
	})
	baseReq := types.CreatePaymentRequest{
		Type:            types.PaymentACH,
		Amount:          "100",
		Currency:        "USD",
		SourceAccountID: "ACC-1",
		OrgID:           "org-A",
		Meta:            map[string]string{"offeringId": "1", "tradeId": "1", "createdIpAddress": "1.1.1.1"},
	}
	if _, err := p.CreatePayment(context.Background(), &baseReq); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// Different orgID → different key.
	differentOrg := baseReq
	differentOrg.OrgID = "org-B"
	if _, err := p.CreatePayment(context.Background(), &differentOrg); err != nil {
		t.Fatalf("differentOrg: %v", err)
	}

	// Different amount → different key.
	differentAmt := baseReq
	differentAmt.Amount = "200"
	if _, err := p.CreatePayment(context.Background(), &differentAmt); err != nil {
		t.Fatalf("differentAmt: %v", err)
	}

	if keys[0] == keys[1] || keys[0] == keys[2] || keys[1] == keys[2] {
		t.Fatalf("derived keys collided: %v", keys)
	}
}

// --- Authentication header presence ---

// TestConformance_Authentication asserts that clientID and
// developerAPIKey are present in the body on every call.
func TestConformance_Authentication(t *testing.T) {
	cases := []struct {
		verb string
		exec func(p *Provider) error
	}{
		{"createAccount", func(p *Provider) error {
			_, err := p.CreateAccount(context.Background(), "org-A", &provider.CreateAccountRequest{AccountName: "x"})
			return err
		}},
		{"getAccount", func(p *Provider) error {
			_, err := p.GetAccount(context.Background(), "ACC")
			return err
		}},
		{"externalFundMove", func(p *Provider) error {
			_, err := p.CreatePayment(context.Background(), &types.CreatePaymentRequest{
				Type: types.PaymentACH, Amount: "10", Currency: "USD",
				SourceAccountID: "A", OrgID: "o",
			})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			var seen map[string]any
			p, _ := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
				tc.verb: func(t *testing.T, r *http.Request, w http.ResponseWriter) {
					writeJSON(t, w, 200, map[string]any{"statusCode": "101", "accountId": "ACC", "RefNum": "X"})
				},
			})
			// Hook to capture the first body — the router already
			// records, but we want a direct accessor for clarity.
			old := p.client
			p.client = old
			if err := tc.exec(p); err != nil {
				t.Fatalf("exec %s: %v", tc.verb, err)
			}
			// Re-execute via a dedicated server we can inspect.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = mustReadJSON(t, r)
				writeJSON(t, w, 200, map[string]any{"statusCode": "101", "accountId": "ACC", "RefNum": "X"})
			}))
			t.Cleanup(srv.Close)
			p.cfg.BaseURL = srv.URL
			p.client = srv.Client()
			if err := tc.exec(p); err != nil {
				t.Fatalf("re-exec %s: %v", tc.verb, err)
			}
			if seen["clientID"] != "test-client" || seen["developerAPIKey"] != "test-key" {
				t.Fatalf("credentials missing from body: %v", seen)
			}
		})
	}
}

// --- 4xx error mapping ---

// TestConformance_4xxErrorMapping ensures a 400 response surfaces
// with the upstream body bytes embedded in the error string so an
// operator can diagnose without re-hitting NCPS.
func TestConformance_4xxErrorMapping(t *testing.T) {
	p, _ := newRouter(t, map[string]func(*testing.T, *http.Request, http.ResponseWriter){
		"externalFundMove": func(t *testing.T, r *http.Request, w http.ResponseWriter) {
			writeJSON(t, w, 400, map[string]any{
				"statusCode": "189",
				"statusDesc": "Insufficient funds",
			})
		},
	})
	_, err := p.CreatePayment(context.Background(), &types.CreatePaymentRequest{
		Type: types.PaymentACH, Amount: "100", Currency: "USD",
		SourceAccountID: "ACC-1", OrgID: "org-A",
	})
	if err == nil {
		t.Fatal("expected error on 400 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("error must mention 400: %v", err)
	}
	if !strings.Contains(err.Error(), "Insufficient funds") {
		t.Fatalf("error must echo upstream body: %v", err)
	}
}

// TestConformance_429RetryHonorsRetryAfter exercises the 429
// backoff loop. The first response is a 429 with Retry-After=1, the
// second is a 200; the call succeeds on the second attempt.
func TestConformance_429RetryHonorsRetryAfter(t *testing.T) {
	var call int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&call, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		writeJSON(t, w, 200, map[string]any{"statusCode": "101", "RefNum": "RETRY-OK", "Status": "Submitted"})
	}))
	defer srv.Close()
	p := &Provider{
		cfg: Config{
			BaseURL:         srv.URL,
			ClientID:        "c",
			DeveloperAPIKey: "k",
			HTTPClient:      srv.Client(),
			MaxRetries:      3,
			BackoffBase:     10 * time.Millisecond,
		},
		client: srv.Client(),
	}
	pay, err := p.CreatePayment(context.Background(), &types.CreatePaymentRequest{
		Type: types.PaymentACH, Amount: "1", Currency: "USD",
		SourceAccountID: "A", OrgID: "o",
	})
	if err != nil {
		t.Fatalf("CreatePayment with retry: %v", err)
	}
	if pay.ID != "RETRY-OK" {
		t.Fatalf("expected RETRY-OK, got %q", pay.ID)
	}
	if atomic.LoadInt32(&call) != 2 {
		t.Fatalf("expected 2 calls (429 + 200), got %d", call)
	}
}

// TestConformance_429ExhaustsRetries asserts the retry budget is
// respected — once exhausted, the original error surfaces.
func TestConformance_429ExhaustsRetries(t *testing.T) {
	var call int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&call, 1)
		http.Error(w, "still rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	p := &Provider{
		cfg: Config{
			BaseURL:         srv.URL,
			ClientID:        "c",
			DeveloperAPIKey: "k",
			HTTPClient:      srv.Client(),
			MaxRetries:      2,
			BackoffBase:     1 * time.Millisecond,
		},
		client: srv.Client(),
	}
	_, err := p.CreatePayment(context.Background(), &types.CreatePaymentRequest{
		Type: types.PaymentACH, Amount: "1", Currency: "USD",
		SourceAccountID: "A", OrgID: "o",
	})
	if err == nil {
		t.Fatal("expected exhausted-retries error")
	}
	// 1 initial + 2 retries == 3 calls total.
	if atomic.LoadInt32(&call) != 3 {
		t.Fatalf("expected 3 calls (1 + 2 retries), got %d", call)
	}
}

// TestConformance_ContextCancellation ensures the backoff loop bails
// promptly on context cancel rather than waiting out the sleep.
func TestConformance_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "still rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	p := &Provider{
		cfg: Config{
			BaseURL:         srv.URL,
			ClientID:        "c",
			DeveloperAPIKey: "k",
			HTTPClient:      srv.Client(),
			MaxRetries:      10,
			BackoffBase:     5 * time.Second,
		},
		client: srv.Client(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel quickly so the very first backoff aborts.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := p.CreatePayment(ctx, &types.CreatePaymentRequest{
		Type: types.PaymentACH, Amount: "1", Currency: "USD",
		SourceAccountID: "A", OrgID: "o",
	})
	if err == nil {
		t.Fatal("expected context error")
	}
	if !errors.Is(err, context.Canceled) {
		// Some failure modes wrap; accept either via string match.
		if !strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "context") {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("context cancellation took too long: %v", elapsed)
	}
}
