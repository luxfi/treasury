package settlement

// TradeSettlementRequest is the input for settling a secondary market trade's funds.
type TradeSettlementRequest struct {
	TradeID          string `json:"trade_id"`
	GrossAmount      string `json:"gross_amount"`       // total buyer payment (decimal string)
	BuyerCommission  string `json:"buyer_commission"`    // buyer BD commission
	SellerCommission string `json:"seller_commission"`   // seller BD commission
	PlatformFee      string `json:"platform_fee"`        // platform fee (can be "0")
	NetAmount        string `json:"net_amount"`          // seller receives (gross - seller commission - platform fee)
	Currency         string `json:"currency"`            // e.g., "USD"
	BuyerWalletID    string `json:"buyer_wallet_id"`     // buyer ledger account address
	SellerWalletID   string `json:"seller_wallet_id"`    // seller ledger account address
	SettlementType   string `json:"settlement_type"`     // bilateral, dvp, free_delivery
}

// TradeSettlementResult is the outcome of a trade settlement.
type TradeSettlementResult struct {
	TradeID              string `json:"trade_id"`
	LedgerTransactionID  int64  `json:"ledger_transaction_id"`
	BuyerDebit           string `json:"buyer_debit"`
	SellerCredit         string `json:"seller_credit"`
	BuyerCommissionDebit string `json:"buyer_commission_debit"`
	SellerCommissionDebit string `json:"seller_commission_debit"`
	PlatformFeeDebit     string `json:"platform_fee_debit"`
	Status               string `json:"status"` // settled, failed
}
