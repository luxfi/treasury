package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/luxfi/treasury/pkg/compliance"
	"github.com/luxfi/treasury/pkg/ledger"
	"github.com/luxfi/treasury/pkg/provider"
	"github.com/luxfi/treasury/pkg/types"
)

type Server struct {
	registry   *provider.Registry
	ledger     *ledger.Ledger
	compliance *compliance.Engine
	router     chi.Router
	server     *http.Server
}

func NewServer(registry *provider.Registry, listenAddr string) *Server {
	s := &Server{
		registry:   registry,
		ledger:     ledger.New(),
		compliance: compliance.NewEngine(),
	}

	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(60 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://lux.financial", "https://app.lux.financial", "http://localhost:3000"},
		AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-API-Key"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":    "ok",
			"providers": registry.List(),
		})
	})

	r.Route("/api/v1", func(r chi.Router) {
		// Providers
		r.Get("/providers", s.handleListProviders)
		r.Get("/providers/capabilities", s.handleGetCapabilities)

		// Bank Accounts
		r.Get("/accounts", s.handleListAccounts)
		r.Post("/accounts", s.handleCreateAccount)
		r.Get("/accounts/{provider}/{accountId}", s.handleGetAccount)
		r.Get("/accounts/{provider}/{accountId}/balance", s.handleGetBalance)

		// Payments
		r.Post("/payments", s.handleCreatePayment)
		r.Get("/payments", s.handleListPayments)
		r.Get("/payments/{provider}/{paymentId}", s.handleGetPayment)
		r.Delete("/payments/{provider}/{paymentId}", s.handleCancelPayment)

		// FX
		r.Get("/fx/quote", s.handleGetFXQuote)
		r.Post("/fx/convert", s.handleCreateFXConversion)
		r.Get("/fx/conversions", s.handleListFXConversions)
		r.Get("/fx/rates", s.handleGetFXRates)

		// Counterparties
		r.Post("/counterparties", s.handleCreateCounterparty)
		r.Get("/counterparties", s.handleListCounterparties)

		// Ledger
		r.Post("/ledger/accounts", s.handleCreateLedgerAccount)
		r.Get("/ledger/accounts", s.handleListLedgerAccounts)
		r.Get("/ledger/accounts/{accountId}/entries", s.handleGetLedgerEntries)
		r.Post("/ledger/transactions", s.handlePostLedgerTransaction)

		// Compliance
		r.Post("/compliance/check", s.handleComplianceCheck)
		r.Get("/compliance/rules", s.handleListComplianceRules)
	})

	s.router = r
	s.server = &http.Server{
		Addr:         listenAddr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	return s
}

func (s *Server) Start() error {
	return s.server.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// --- Provider Handlers ---

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"providers": s.registry.List()})
}

func (s *Server) handleGetCapabilities(w http.ResponseWriter, r *http.Request) {
	caps := make([]*types.ProviderCapability, 0)
	for _, p := range s.registry.All() {
		caps = append(caps, p.Capabilities())
	}
	writeJSON(w, http.StatusOK, caps)
}

// --- Account Handlers ---

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	provName := r.URL.Query().Get("provider")
	orgID := r.Header.Get("X-Org-ID")

	if provName != "" {
		p, err := s.registry.Get(provName)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		accts, err := p.ListAccounts(r.Context(), orgID)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, accts)
		return
	}

	all := make([]*types.BankAccount, 0)
	for _, name := range s.registry.List() {
		p, _ := s.registry.Get(name)
		accts, err := p.ListAccounts(r.Context(), orgID)
		if err == nil {
			all = append(all, accts...)
		}
	}
	writeJSON(w, http.StatusOK, all)
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider    string `json:"provider"`
		AccountName string `json:"account_name"`
		Currency    string `json:"currency"`
		AccountType string `json:"account_type"`
		Country     string `json:"country,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	p, err := s.registry.Get(req.Provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	orgID := r.Header.Get("X-Org-ID")
	acct, err := p.CreateAccount(r.Context(), orgID, &provider.CreateAccountRequest{
		AccountName: req.AccountName,
		Currency:    req.Currency,
		AccountType: req.AccountType,
		Country:     req.Country,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, acct)
}

func (s *Server) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	p, err := s.registry.Get(chi.URLParam(r, "provider"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	acct, err := p.GetAccount(r.Context(), chi.URLParam(r, "accountId"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, acct)
}

func (s *Server) handleGetBalance(w http.ResponseWriter, r *http.Request) {
	p, err := s.registry.Get(chi.URLParam(r, "provider"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	bal, err := p.GetBalance(r.Context(), chi.URLParam(r, "accountId"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, bal)
}

// --- Payment Handlers ---

func (s *Server) handleCreatePayment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string                    `json:"provider"`
		Payment  types.CreatePaymentRequest `json:"payment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Compliance check
	result := s.compliance.Check(&req.Payment)
	if !result.Allowed {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{
			"error":  "compliance check failed",
			"blocks": result.Blocks,
			"flags":  result.Flags,
		})
		return
	}

	p, err := s.registry.Get(req.Provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	payment, err := p.CreatePayment(r.Context(), &req.Payment)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"payment":           payment,
		"compliance_flags":  result.Flags,
	})
}

func (s *Server) handleListPayments(w http.ResponseWriter, r *http.Request) {
	provName := r.URL.Query().Get("provider")
	accountID := r.URL.Query().Get("account_id")

	if provName == "" {
		writeError(w, http.StatusBadRequest, "provider query param required")
		return
	}

	p, err := s.registry.Get(provName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payments, err := p.ListPayments(r.Context(), accountID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, payments)
}

func (s *Server) handleGetPayment(w http.ResponseWriter, r *http.Request) {
	p, err := s.registry.Get(chi.URLParam(r, "provider"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payment, err := p.GetPayment(r.Context(), chi.URLParam(r, "paymentId"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, payment)
}

func (s *Server) handleCancelPayment(w http.ResponseWriter, r *http.Request) {
	p, err := s.registry.Get(chi.URLParam(r, "provider"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := p.CancelPayment(r.Context(), chi.URLParam(r, "paymentId")); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// --- FX Handlers ---

func (s *Server) handleGetFXQuote(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	provName := q.Get("provider")
	if provName == "" {
		provName = s.firstFXProvider()
	}
	if provName == "" {
		writeError(w, http.StatusBadRequest, "no FX provider available")
		return
	}

	p, err := s.registry.Get(provName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	quote, err := p.GetFXQuote(r.Context(),
		q.Get("sell_currency"), q.Get("buy_currency"),
		q.Get("amount"), q.Get("fixed_side"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, quote)
}

func (s *Server) handleCreateFXConversion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider   string                        `json:"provider"`
		Conversion types.CreateFXConversionRequest `json:"conversion"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Provider == "" {
		req.Provider = s.firstFXProvider()
	}

	p, err := s.registry.Get(req.Provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	conv, err := p.CreateFXConversion(r.Context(), &req.Conversion)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, conv)
}

func (s *Server) handleListFXConversions(w http.ResponseWriter, r *http.Request) {
	provName := r.URL.Query().Get("provider")
	if provName == "" {
		provName = s.firstFXProvider()
	}

	p, err := s.registry.Get(provName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	conversions, err := p.ListFXConversions(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, conversions)
}

func (s *Server) handleGetFXRates(w http.ResponseWriter, r *http.Request) {
	// Aggregate FX quotes from all providers for comparison
	q := r.URL.Query()
	sellCcy := q.Get("sell_currency")
	buyCcy := q.Get("buy_currency")
	amount := q.Get("amount")
	fixedSide := q.Get("fixed_side")

	if sellCcy == "" || buyCcy == "" {
		writeError(w, http.StatusBadRequest, "sell_currency and buy_currency required")
		return
	}
	if fixedSide == "" {
		fixedSide = "sell"
	}
	if amount == "" {
		amount = "10000"
	}

	type rateResult struct {
		Provider string         `json:"provider"`
		Quote    *types.FXQuote `json:"quote,omitempty"`
		Error    string         `json:"error,omitempty"`
	}

	var results []rateResult
	for _, name := range s.registry.List() {
		p, _ := s.registry.Get(name)
		quote, err := p.GetFXQuote(r.Context(), sellCcy, buyCcy, amount, fixedSide)
		if err != nil {
			if err.Error() != provider.ErrNotSupported.Error() {
				results = append(results, rateResult{Provider: name, Error: err.Error()})
			}
			continue
		}
		results = append(results, rateResult{Provider: name, Quote: quote})
	}
	writeJSON(w, http.StatusOK, results)
}

// --- Counterparty Handlers ---

func (s *Server) handleCreateCounterparty(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider     string            `json:"provider"`
		Counterparty types.Counterparty `json:"counterparty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	p, err := s.registry.Get(req.Provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	cp, err := p.CreateCounterparty(r.Context(), &req.Counterparty)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, cp)
}

func (s *Server) handleListCounterparties(w http.ResponseWriter, r *http.Request) {
	provName := r.URL.Query().Get("provider")
	if provName == "" {
		writeError(w, http.StatusBadRequest, "provider query param required")
		return
	}
	p, err := s.registry.Get(provName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cps, err := p.ListCounterparties(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cps)
}

// --- Ledger Handlers ---

func (s *Server) handleCreateLedgerAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Currency string `json:"currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	acct, err := s.ledger.CreateAccount(req.Name, req.Type, req.Currency)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, acct)
}

func (s *Server) handleListLedgerAccounts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.ledger.ListAccounts())
}

func (s *Server) handleGetLedgerEntries(w http.ResponseWriter, r *http.Request) {
	entries := s.ledger.GetEntries(chi.URLParam(r, "accountId"))
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handlePostLedgerTransaction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Description string `json:"description"`
		Entries     []struct {
			AccountID string  `json:"account_id"`
			Amount    float64 `json:"amount"`
			Currency  string  `json:"currency"`
			Direction string  `json:"direction"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	entries := make([]ledger.TransactionEntry, len(req.Entries))
	for i, e := range req.Entries {
		entries[i] = ledger.TransactionEntry{
			AccountID:   e.AccountID,
			AmountFloat: e.Amount,
			Currency:    e.Currency,
			Direction:   e.Direction,
		}
	}

	txID, err := s.ledger.PostTransaction(req.Description, entries)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"transaction_id": txID})
}

// --- Compliance Handlers ---

func (s *Server) handleComplianceCheck(w http.ResponseWriter, r *http.Request) {
	var req types.CreatePaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result := s.compliance.Check(&req)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleListComplianceRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.compliance.ListRules())
}

// --- Helpers ---

func (s *Server) firstFXProvider() string {
	for _, p := range s.registry.All() {
		cap := p.Capabilities()
		for _, f := range cap.Features {
			if f == "fx" {
				return cap.Name
			}
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
