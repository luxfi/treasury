package settlement

import (
	"context"
	"testing"

	"github.com/luxfi/treasury/pkg/ledger"
)

func setupService() (*Service, *ledger.Service) {
	store := ledger.NewMemStore()
	ledgerSvc := ledger.NewService(store)
	svc := NewService(ledgerSvc)
	return svc, ledgerSvc
}

func fundBuyer(t *testing.T, ledgerSvc *ledger.Service, buyer, asset, amount string) {
	t.Helper()
	ctx := context.Background()
	_, err := ledgerSvc.PostTransaction(ctx, "trades", ledger.CreateTransactionRequest{
		Postings: []ledger.Posting{
			{Source: "world", Destination: buyer, Asset: asset, Amount: amount},
		},
	})
	if err != nil {
		t.Fatalf("fund buyer: %v", err)
	}
}

func TestBasicTradeSettlement(t *testing.T) {
	svc, ledgerSvc := setupService()
	ctx := context.Background()

	// Fund buyer with enough to cover gross + buyer commission.
	// Gross: 45000, Buyer commission: 11250 => total: 56250
	fundBuyer(t, ledgerSvc, "wallets:buyer:001", "USD/2", "5625000")

	result, err := svc.SettleTrade(ctx, TradeSettlementRequest{
		TradeID:          "trade-001",
		GrossAmount:      "4500000", // $45,000.00 in cents
		BuyerCommission:  "11250",   // $112.50
		SellerCommission: "11250",   // $112.50
		PlatformFee:      "0",
		NetAmount:        "4488750", // $44,887.50
		Currency:         "USD",
		BuyerWalletID:    "wallets:buyer:001",
		SellerWalletID:   "wallets:seller:001",
		SettlementType:   "bilateral",
	})
	if err != nil {
		t.Fatalf("SettleTrade: %v", err)
	}

	if result.Status != "settled" {
		t.Fatalf("status = %s, want settled", result.Status)
	}
	if result.TradeID != "trade-001" {
		t.Fatalf("trade_id = %s, want trade-001", result.TradeID)
	}
	if result.LedgerTransactionID == 0 {
		t.Fatal("expected non-zero ledger transaction ID")
	}
	// Total buyer pays = 4500000 + 11250 = 4511250
	if result.BuyerDebit != "4511250" {
		t.Fatalf("buyer_debit = %s, want 4511250", result.BuyerDebit)
	}
	// Net to seller = 4500000 - 11250 - 0 = 4488750
	if result.SellerCredit != "4488750" {
		t.Fatalf("seller_credit = %s, want 4488750", result.SellerCredit)
	}

	// Verify ledger balances.
	sellerBal, err := ledgerSvc.GetAccountBalance(ctx, "trades", "wallets:seller:001", "USD/2")
	if err != nil {
		t.Fatalf("GetAccountBalance seller: %v", err)
	}
	if sellerBal.Balance != "4488750" {
		t.Fatalf("seller balance = %s, want 4488750", sellerBal.Balance)
	}

	buyerBDCommBal, err := ledgerSvc.GetAccountBalance(ctx, "trades", "commissions:buyer_bd:trade-001", "USD/2")
	if err != nil {
		t.Fatalf("GetAccountBalance buyer BD: %v", err)
	}
	if buyerBDCommBal.Balance != "11250" {
		t.Fatalf("buyer BD commission = %s, want 11250", buyerBDCommBal.Balance)
	}

	sellerBDCommBal, err := ledgerSvc.GetAccountBalance(ctx, "trades", "commissions:seller_bd:trade-001", "USD/2")
	if err != nil {
		t.Fatalf("GetAccountBalance seller BD: %v", err)
	}
	if sellerBDCommBal.Balance != "11250" {
		t.Fatalf("seller BD commission = %s, want 11250", sellerBDCommBal.Balance)
	}

	// Escrow should be zero (all funds distributed).
	escrowBal, err := ledgerSvc.GetAccountBalance(ctx, "trades", "escrow:trades", "USD/2")
	if err != nil {
		t.Fatalf("GetAccountBalance escrow: %v", err)
	}
	if escrowBal.Balance != "0" {
		t.Fatalf("escrow balance = %s, want 0", escrowBal.Balance)
	}
}

func TestSettlementWithPlatformFee(t *testing.T) {
	svc, ledgerSvc := setupService()
	ctx := context.Background()

	// Gross: 10000000, Buyer comm: 25000, Seller comm: 25000, Platform fee: 50000
	// Total buyer pays: 10000000 + 25000 = 10025000
	// Net to seller: 10000000 - 25000 - 50000 = 9925000
	fundBuyer(t, ledgerSvc, "wallets:buyer:002", "USD/2", "20000000")

	result, err := svc.SettleTrade(ctx, TradeSettlementRequest{
		TradeID:          "trade-002",
		GrossAmount:      "10000000",
		BuyerCommission:  "25000",
		SellerCommission: "25000",
		PlatformFee:      "50000",
		NetAmount:        "9925000",
		Currency:         "USD",
		BuyerWalletID:    "wallets:buyer:002",
		SellerWalletID:   "wallets:seller:002",
		SettlementType:   "dvp",
	})
	if err != nil {
		t.Fatalf("SettleTrade: %v", err)
	}

	if result.BuyerDebit != "10025000" {
		t.Fatalf("buyer_debit = %s, want 10025000", result.BuyerDebit)
	}
	if result.SellerCredit != "9925000" {
		t.Fatalf("seller_credit = %s, want 9925000", result.SellerCredit)
	}
	if result.PlatformFeeDebit != "50000" {
		t.Fatalf("platform_fee = %s, want 50000", result.PlatformFeeDebit)
	}

	// Platform fees account should have received 50000.
	feesBal, err := ledgerSvc.GetAccountBalance(ctx, "trades", "platform:fees", "USD/2")
	if err != nil {
		t.Fatalf("GetAccountBalance fees: %v", err)
	}
	if feesBal.Balance != "50000" {
		t.Fatalf("platform fees balance = %s, want 50000", feesBal.Balance)
	}

	// Escrow should be zero.
	escrowBal, err := ledgerSvc.GetAccountBalance(ctx, "trades", "escrow:trades", "USD/2")
	if err != nil {
		t.Fatalf("GetAccountBalance escrow: %v", err)
	}
	if escrowBal.Balance != "0" {
		t.Fatalf("escrow balance = %s, want 0", escrowBal.Balance)
	}
}

func TestIdempotentSettlement(t *testing.T) {
	svc, ledgerSvc := setupService()
	ctx := context.Background()

	fundBuyer(t, ledgerSvc, "wallets:buyer:003", "USD/2", "10000000")

	req := TradeSettlementRequest{
		TradeID:          "trade-003",
		GrossAmount:      "5000000",
		BuyerCommission:  "10000",
		SellerCommission: "10000",
		PlatformFee:      "0",
		NetAmount:        "4990000",
		Currency:         "USD",
		BuyerWalletID:    "wallets:buyer:003",
		SellerWalletID:   "wallets:seller:003",
		SettlementType:   "bilateral",
	}

	result1, err := svc.SettleTrade(ctx, req)
	if err != nil {
		t.Fatalf("first SettleTrade: %v", err)
	}

	result2, err := svc.SettleTrade(ctx, req)
	if err != nil {
		t.Fatalf("second SettleTrade: %v", err)
	}

	// Same ledger transaction ID means it was idempotent.
	if result1.LedgerTransactionID != result2.LedgerTransactionID {
		t.Fatalf("idempotency failed: tx1=%d, tx2=%d", result1.LedgerTransactionID, result2.LedgerTransactionID)
	}

	// Seller balance should be 4990000 (not doubled).
	sellerBal, err := ledgerSvc.GetAccountBalance(ctx, "trades", "wallets:seller:003", "USD/2")
	if err != nil {
		t.Fatalf("GetAccountBalance seller: %v", err)
	}
	if sellerBal.Balance != "4990000" {
		t.Fatalf("seller balance = %s, want 4990000 (idempotent)", sellerBal.Balance)
	}
}

func TestSettlementValidation(t *testing.T) {
	svc, _ := setupService()
	ctx := context.Background()

	tests := []struct {
		name string
		req  TradeSettlementRequest
	}{
		{"empty trade_id", TradeSettlementRequest{
			GrossAmount: "1000", BuyerCommission: "0", SellerCommission: "0", PlatformFee: "0",
			BuyerWalletID: "a", SellerWalletID: "b",
		}},
		{"empty buyer wallet", TradeSettlementRequest{
			TradeID: "t1", GrossAmount: "1000", BuyerCommission: "0", SellerCommission: "0", PlatformFee: "0",
			SellerWalletID: "b",
		}},
		{"zero gross", TradeSettlementRequest{
			TradeID: "t2", GrossAmount: "0", BuyerCommission: "0", SellerCommission: "0", PlatformFee: "0",
			BuyerWalletID: "a", SellerWalletID: "b",
		}},
		{"negative commission", TradeSettlementRequest{
			TradeID: "t3", GrossAmount: "1000", BuyerCommission: "-1", SellerCommission: "0", PlatformFee: "0",
			BuyerWalletID: "a", SellerWalletID: "b",
		}},
		{"fees exceed gross", TradeSettlementRequest{
			TradeID: "t4", GrossAmount: "100", BuyerCommission: "0", SellerCommission: "50", PlatformFee: "60",
			BuyerWalletID: "a", SellerWalletID: "b",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.SettleTrade(ctx, tt.req)
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}
