package wallet

import (
	"context"
	"testing"

	"github.com/luxfi/treasury/pkg/ledger"
)

func newTestService() *Service {
	store := ledger.NewMemStore()
	svc := ledger.NewService(store)
	return NewService(svc)
}

func TestCreateWalletAndCredit(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	w, err := svc.CreateWallet(ctx, "default", "Alice Wallet", nil)
	if err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}
	if w.ID == "" {
		t.Error("expected non-empty wallet ID")
	}

	err = svc.Credit(ctx, w.ID, CreditRequest{Asset: "USD/2", Amount: "10000"})
	if err != nil {
		t.Fatalf("Credit: %v", err)
	}

	balances, err := svc.GetBalances(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetBalances: %v", err)
	}
	if len(balances) != 1 {
		t.Fatalf("got %d balances, want 1", len(balances))
	}
	if balances[0].Available != "10000" {
		t.Errorf("available = %s, want 10000", balances[0].Available)
	}
}

func TestDebit(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	w, _ := svc.CreateWallet(ctx, "default", "Test", nil)
	svc.Credit(ctx, w.ID, CreditRequest{Asset: "USD/2", Amount: "5000"})

	err := svc.Debit(ctx, w.ID, DebitRequest{Asset: "USD/2", Amount: "2000"})
	if err != nil {
		t.Fatalf("Debit: %v", err)
	}

	balances, _ := svc.GetBalances(ctx, w.ID)
	if balances[0].Available != "3000" {
		t.Errorf("available = %s, want 3000", balances[0].Available)
	}
}

func TestHoldConfirmFull(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	w, _ := svc.CreateWallet(ctx, "default", "Test", nil)
	svc.Credit(ctx, w.ID, CreditRequest{Asset: "USD/2", Amount: "10000"})

	// Create hold for 3000
	hold, err := svc.CreateHold(ctx, w.ID, CreateHoldRequest{
		Asset:       "USD/2",
		Amount:      "3000",
		Destination: "merchants:042",
	})
	if err != nil {
		t.Fatalf("CreateHold: %v", err)
	}
	if hold.Status != "pending" {
		t.Errorf("hold status = %s, want pending", hold.Status)
	}

	// Main balance should be 7000 (10000 - 3000 held)
	balances, _ := svc.GetBalances(ctx, w.ID)
	if balances[0].Available != "7000" {
		t.Errorf("available = %s, want 7000", balances[0].Available)
	}
	if balances[0].Held != "3000" {
		t.Errorf("held = %s, want 3000", balances[0].Held)
	}
	if balances[0].Total != "10000" {
		t.Errorf("total = %s, want 10000", balances[0].Total)
	}

	// Confirm hold (full)
	confirmed, err := svc.ConfirmHold(ctx, hold.ID, ConfirmHoldRequest{})
	if err != nil {
		t.Fatalf("ConfirmHold: %v", err)
	}
	if confirmed.Status != "confirmed" {
		t.Errorf("hold status = %s, want confirmed", confirmed.Status)
	}
	if confirmed.Remaining != "0" {
		t.Errorf("remaining = %s, want 0", confirmed.Remaining)
	}

	// Balance: 7000 available, 0 held
	balances, _ = svc.GetBalances(ctx, w.ID)
	if balances[0].Available != "7000" {
		t.Errorf("available after confirm = %s, want 7000", balances[0].Available)
	}
	if balances[0].Held != "0" {
		t.Errorf("held after confirm = %s, want 0", balances[0].Held)
	}
}

func TestHoldPartialConfirm(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	w, _ := svc.CreateWallet(ctx, "default", "Test", nil)
	svc.Credit(ctx, w.ID, CreditRequest{Asset: "USD/2", Amount: "10000"})

	hold, _ := svc.CreateHold(ctx, w.ID, CreateHoldRequest{
		Asset:  "USD/2",
		Amount: "5000",
	})

	// Partial confirm: 2000 of 5000
	h, err := svc.ConfirmHold(ctx, hold.ID, ConfirmHoldRequest{Amount: "2000"})
	if err != nil {
		t.Fatalf("partial confirm: %v", err)
	}
	if h.Status != "partial" {
		t.Errorf("status = %s, want partial", h.Status)
	}
	if h.Remaining != "3000" {
		t.Errorf("remaining = %s, want 3000", h.Remaining)
	}
}

func TestHoldVoid(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	w, _ := svc.CreateWallet(ctx, "default", "Test", nil)
	svc.Credit(ctx, w.ID, CreditRequest{Asset: "USD/2", Amount: "10000"})

	hold, _ := svc.CreateHold(ctx, w.ID, CreateHoldRequest{
		Asset:  "USD/2",
		Amount: "4000",
	})

	// Void hold — funds return to main
	voided, err := svc.VoidHold(ctx, hold.ID)
	if err != nil {
		t.Fatalf("VoidHold: %v", err)
	}
	if voided.Status != "voided" {
		t.Errorf("status = %s, want voided", voided.Status)
	}

	// All 10000 back in main
	balances, _ := svc.GetBalances(ctx, w.ID)
	if balances[0].Available != "10000" {
		t.Errorf("available after void = %s, want 10000", balances[0].Available)
	}
}

func TestHoldPartialConfirmThenVoid(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	w, _ := svc.CreateWallet(ctx, "default", "Test", nil)
	svc.Credit(ctx, w.ID, CreditRequest{Asset: "USD/2", Amount: "10000"})

	hold, _ := svc.CreateHold(ctx, w.ID, CreateHoldRequest{
		Asset:  "USD/2",
		Amount: "6000",
	})

	// Partial confirm 2000
	svc.ConfirmHold(ctx, hold.ID, ConfirmHoldRequest{Amount: "2000"})

	// Void remaining 4000
	voided, err := svc.VoidHold(ctx, hold.ID)
	if err != nil {
		t.Fatalf("void after partial: %v", err)
	}
	if voided.Remaining != "0" {
		t.Errorf("remaining = %s, want 0", voided.Remaining)
	}

	// Main: 10000 - 6000 (hold) + 4000 (voided back) = 8000
	// Actually: main started at 10000, hold took 6000 → main=4000
	// confirm sent 2000 to world, void returned 4000 to main → main=8000
	balances, _ := svc.GetBalances(ctx, w.ID)
	if balances[0].Available != "8000" {
		t.Errorf("available = %s, want 8000", balances[0].Available)
	}
}

func TestDoubleConfirmFails(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	w, _ := svc.CreateWallet(ctx, "default", "Test", nil)
	svc.Credit(ctx, w.ID, CreditRequest{Asset: "USD/2", Amount: "5000"})

	hold, _ := svc.CreateHold(ctx, w.ID, CreateHoldRequest{Asset: "USD/2", Amount: "3000"})
	svc.ConfirmHold(ctx, hold.ID, ConfirmHoldRequest{})

	_, err := svc.ConfirmHold(ctx, hold.ID, ConfirmHoldRequest{})
	if err == nil {
		t.Error("expected error on double confirm")
	}
}
