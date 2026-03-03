package types

import "time"

// --- Core Banking Types ---

// BankAccount represents an external or virtual bank account.
type BankAccount struct {
	ID              string            `json:"id"`
	Provider        string            `json:"provider"`
	ProviderID      string            `json:"provider_id"`
	OrgID           string            `json:"org_id"`
	AccountName     string            `json:"account_name"`
	AccountNumber   string            `json:"account_number,omitempty"`
	RoutingNumber   string            `json:"routing_number,omitempty"`
	IBAN            string            `json:"iban,omitempty"`
	SwiftBIC        string            `json:"swift_bic,omitempty"`
	SortCode        string            `json:"sort_code,omitempty"`
	BankName        string            `json:"bank_name,omitempty"`
	BankCountry     string            `json:"bank_country,omitempty"`
	Currency        string            `json:"currency"`
	AccountType     string            `json:"account_type"` // checking, savings, virtual
	Status          string            `json:"status"`       // active, pending, closed
	Balance         *Balance          `json:"balance,omitempty"`
	Meta            map[string]string `json:"meta,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// Balance represents account balance with available/pending breakdown.
type Balance struct {
	Available string `json:"available"`
	Current   string `json:"current"`
	Pending   string `json:"pending,omitempty"`
	Currency  string `json:"currency"`
	UpdatedAt time.Time `json:"updated_at"`
}

// --- Payment Types ---

// Payment represents a fund movement (ACH, wire, SEPA, SWIFT, book transfer).
type Payment struct {
	ID              string            `json:"id"`
	Provider        string            `json:"provider"`
	ProviderID      string            `json:"provider_id"`
	OrgID           string            `json:"org_id"`
	Type            PaymentType       `json:"type"`
	Direction       string            `json:"direction"` // credit, debit
	Amount          string            `json:"amount"`
	Currency        string            `json:"currency"`
	Status          PaymentStatus     `json:"status"`
	SourceAccountID string            `json:"source_account_id,omitempty"`
	DestAccountID   string            `json:"dest_account_id,omitempty"`
	Counterparty    *Counterparty     `json:"counterparty,omitempty"`
	Reference       string            `json:"reference,omitempty"`
	Description     string            `json:"description,omitempty"`
	FeeAmount       string            `json:"fee_amount,omitempty"`
	FeeCurrency     string            `json:"fee_currency,omitempty"`
	ExchangeRate    string            `json:"exchange_rate,omitempty"`
	SettlementDate  string            `json:"settlement_date,omitempty"`
	Meta            map[string]string `json:"meta,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type PaymentType string

const (
	PaymentACH      PaymentType = "ach"
	PaymentWire     PaymentType = "wire"
	PaymentSEPA     PaymentType = "sepa"
	PaymentSWIFT    PaymentType = "swift"
	PaymentBook     PaymentType = "book"      // internal transfer
	PaymentRTP      PaymentType = "rtp"       // real-time payments
	PaymentFedNow   PaymentType = "fednow"
	PaymentCheck    PaymentType = "check"
	PaymentCard     PaymentType = "card"
)

type PaymentStatus string

const (
	PaymentPending    PaymentStatus = "pending"
	PaymentProcessing PaymentStatus = "processing"
	PaymentCompleted  PaymentStatus = "completed"
	PaymentFailed     PaymentStatus = "failed"
	PaymentCancelled  PaymentStatus = "cancelled"
	PaymentReturned   PaymentStatus = "returned"
)

// CreatePaymentRequest for initiating payments.
type CreatePaymentRequest struct {
	Type            PaymentType       `json:"type"`
	Direction       string            `json:"direction"`
	Amount          string            `json:"amount"`
	Currency        string            `json:"currency"`
	SourceAccountID string            `json:"source_account_id"`
	DestAccountID   string            `json:"dest_account_id,omitempty"`
	Counterparty    *Counterparty     `json:"counterparty,omitempty"`
	Reference       string            `json:"reference,omitempty"`
	Description     string            `json:"description,omitempty"`
	Meta            map[string]string `json:"meta,omitempty"`
}

// Counterparty is the external party in a payment.
type Counterparty struct {
	ID            string `json:"id,omitempty"`
	Name          string `json:"name"`
	AccountNumber string `json:"account_number,omitempty"`
	RoutingNumber string `json:"routing_number,omitempty"`
	IBAN          string `json:"iban,omitempty"`
	SwiftBIC      string `json:"swift_bic,omitempty"`
	BankName      string `json:"bank_name,omitempty"`
	BankCountry   string `json:"bank_country,omitempty"`
	Email         string `json:"email,omitempty"`
}

// --- FX Types ---

// FXQuote is a foreign exchange rate quote.
type FXQuote struct {
	ID              string    `json:"id"`
	Provider        string    `json:"provider"`
	SellCurrency    string    `json:"sell_currency"`
	BuyCurrency     string    `json:"buy_currency"`
	SellAmount      string    `json:"sell_amount,omitempty"`
	BuyAmount       string    `json:"buy_amount,omitempty"`
	Rate            string    `json:"rate"`
	InverseRate     string    `json:"inverse_rate,omitempty"`
	FixedSide       string    `json:"fixed_side"` // buy or sell
	ExpiresAt       time.Time `json:"expires_at"`
	CreatedAt       time.Time `json:"created_at"`
}

// FXConversion is an executed currency conversion.
type FXConversion struct {
	ID              string    `json:"id"`
	Provider        string    `json:"provider"`
	ProviderID      string    `json:"provider_id"`
	QuoteID         string    `json:"quote_id,omitempty"`
	SellCurrency    string    `json:"sell_currency"`
	BuyCurrency     string    `json:"buy_currency"`
	SellAmount      string    `json:"sell_amount"`
	BuyAmount       string    `json:"buy_amount"`
	Rate            string    `json:"rate"`
	SettlementDate  string    `json:"settlement_date"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
}

// CreateFXConversionRequest for currency conversion.
type CreateFXConversionRequest struct {
	SellCurrency string `json:"sell_currency"`
	BuyCurrency  string `json:"buy_currency"`
	Amount       string `json:"amount"`
	FixedSide    string `json:"fixed_side"` // buy or sell
	QuoteID      string `json:"quote_id,omitempty"`
}

// --- Multi-Currency Wallet Types ---

// Wallet is a multi-currency virtual account/wallet.
type Wallet struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	OrgID     string    `json:"org_id"`
	Name      string    `json:"name"`
	Balances  []Balance `json:"balances"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// --- Card Types ---

// Card represents an issued payment card.
type Card struct {
	ID            string    `json:"id"`
	Provider      string    `json:"provider"`
	ProviderID    string    `json:"provider_id"`
	OrgID         string    `json:"org_id"`
	Last4         string    `json:"last4"`
	Brand         string    `json:"brand"` // visa, mastercard
	Type          string    `json:"type"`  // virtual, physical
	Currency      string    `json:"currency"`
	SpendLimit    string    `json:"spend_limit,omitempty"`
	SpendInterval string    `json:"spend_interval,omitempty"` // per_transaction, daily, monthly
	Status        string    `json:"status"`
	ExpiresAt     string    `json:"expires_at"`
	CreatedAt     time.Time `json:"created_at"`
}

// CreateCardRequest for card issuance.
type CreateCardRequest struct {
	Type          string `json:"type"` // virtual, physical
	Currency      string `json:"currency"`
	SpendLimit    string `json:"spend_limit,omitempty"`
	SpendInterval string `json:"spend_interval,omitempty"`
	CardholderID  string `json:"cardholder_id"`
}

// --- Identity/KYC Types ---

// IdentityVerification is the result of a KYC/identity check.
type IdentityVerification struct {
	ID          string            `json:"id"`
	Provider    string            `json:"provider"`
	ProviderID  string            `json:"provider_id"`
	Status      string            `json:"status"` // pending, verified, failed
	Level       string            `json:"level"`  // basic, enhanced, full
	Checks      []KYCCheck        `json:"checks"`
	Meta        map[string]string `json:"meta,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	VerifiedAt  *time.Time        `json:"verified_at,omitempty"`
}

type KYCCheck struct {
	Type   string `json:"type"`   // document, address, sanctions, pep
	Status string `json:"status"` // pass, fail, pending, review
	Detail string `json:"detail,omitempty"`
}

// --- Ledger Types ---

// LedgerEntry is a double-entry bookkeeping record.
type LedgerEntry struct {
	ID            string    `json:"id"`
	LedgerID      string    `json:"ledger_id"`
	TransactionID string    `json:"transaction_id"`
	AccountID     string    `json:"account_id"`
	Amount        string    `json:"amount"`
	Currency      string    `json:"currency"`
	Direction     string    `json:"direction"` // debit, credit
	Status        string    `json:"status"`    // pending, posted, archived
	Description   string    `json:"description,omitempty"`
	EffectiveDate string    `json:"effective_date"`
	PostedAt      time.Time `json:"posted_at"`
}

// LedgerAccount is a bookkeeping account in the ledger.
type LedgerAccount struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"` // asset, liability, equity, revenue, expense
	Currency  string    `json:"currency"`
	Balance   string    `json:"balance"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// --- Webhook Types ---

// WebhookEvent is a normalized event from any treasury provider.
type WebhookEvent struct {
	ID         string      `json:"id"`
	Provider   string      `json:"provider"`
	Type       string      `json:"type"` // payment.completed, payment.failed, fx.settled, etc.
	ResourceID string      `json:"resource_id"`
	Data       interface{} `json:"data"`
	CreatedAt  time.Time   `json:"created_at"`
}

// --- Provider Capability ---

// ProviderCapability describes what a treasury provider supports.
type ProviderCapability struct {
	Name            string   `json:"name"`
	PaymentTypes    []string `json:"payment_types"`    // ach, wire, sepa, swift, rtp, fednow
	Currencies      []string `json:"currencies"`       // USD, EUR, GBP, etc.
	Features        []string `json:"features"`         // fx, cards, ledger, kyc, wallets
	Countries       []string `json:"countries"`        // supported countries
	SettlementSpeed string   `json:"settlement_speed"` // instant, same_day, t+1, t+2
	Status          string   `json:"status"`
}
