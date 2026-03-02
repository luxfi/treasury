package ledger

import (
	"context"
	"testing"
)

func TestCreateAccount(t *testing.T) {
	svc := NewService(NewMemStore())
	ctx := context.Background()

	acct, err := svc.CreateAccount(ctx, "default", "users:alice:main", "asset", nil)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if acct.Address != "users:alice:main" {
		t.Errorf("address = %q, want %q", acct.Address, "users:alice:main")
	}
	if acct.Type != "asset" {
		t.Errorf("type = %q, want %q", acct.Type, "asset")
	}
}

func TestCreateAccountValidation(t *testing.T) {
	svc := NewService(NewMemStore())
	ctx := context.Background()

	_, err := svc.CreateAccount(ctx, "default", "", "asset", nil)
	if err == nil {
		t.Error("expected error for empty address")
	}

	_, err = svc.CreateAccount(ctx, "default", "users:alice", "invalid_type", nil)
	if err == nil {
		t.Error("expected error for invalid type")
	}

	_, err = svc.CreateAccount(ctx, "default", "a::b", "asset", nil)
	if err == nil {
		t.Error("expected error for empty segment")
	}
}

func TestPostTransaction(t *testing.T) {
	svc := NewService(NewMemStore())
	ctx := context.Background()

	tx, err := svc.PostTransaction(ctx, "default", CreateTransactionRequest{
		Reference: "tx-001",
		Postings: []Posting{
			{Source: "world", Destination: "users:alice", Asset: "USD/2", Amount: "10000"},
		},
	})
	if err != nil {
		t.Fatalf("PostTransaction: %v", err)
	}
	if tx.ID == 0 {
		t.Error("expected non-zero transaction ID")
	}
	if len(tx.Postings) != 1 {
		t.Fatalf("postings = %d, want 1", len(tx.Postings))
	}
}

func TestPostTransactionBalanceTracking(t *testing.T) {
	svc := NewService(NewMemStore())
	ctx := context.Background()

	// Fund Alice with 10000
	_, err := svc.PostTransaction(ctx, "default", CreateTransactionRequest{
		Postings: []Posting{
			{Source: "world", Destination: "users:alice", Asset: "USD/2", Amount: "10000"},
		},
	})
	if err != nil {
		t.Fatalf("fund alice: %v", err)
	}

	// Alice pays Bob 3000
	_, err = svc.PostTransaction(ctx, "default", CreateTransactionRequest{
		Postings: []Posting{
			{Source: "users:alice", Destination: "users:bob", Asset: "USD/2", Amount: "3000"},
		},
	})
	if err != nil {
		t.Fatalf("alice→bob: %v", err)
	}

	// Check Alice balance: 10000 - 3000 = 7000
	bal, err := svc.GetAccountBalance(ctx, "default", "users:alice", "USD/2")
	if err != nil {
		t.Fatalf("GetAccountBalance alice: %v", err)
	}
	if bal.Balance != "7000" {
		t.Errorf("alice balance = %s, want 7000", bal.Balance)
	}
	if bal.Volumes.Inputs != "10000" {
		t.Errorf("alice inputs = %s, want 10000", bal.Volumes.Inputs)
	}
	if bal.Volumes.Outputs != "3000" {
		t.Errorf("alice outputs = %s, want 3000", bal.Volumes.Outputs)
	}

	// Check Bob balance: 3000
	bal, err = svc.GetAccountBalance(ctx, "default", "users:bob", "USD/2")
	if err != nil {
		t.Fatalf("GetAccountBalance bob: %v", err)
	}
	if bal.Balance != "3000" {
		t.Errorf("bob balance = %s, want 3000", bal.Balance)
	}

	// World balance: -10000 (world is the unbounded source)
	bal, err = svc.GetAccountBalance(ctx, "default", "world", "USD/2")
	if err != nil {
		t.Fatalf("GetAccountBalance world: %v", err)
	}
	if bal.Balance != "-10000" {
		t.Errorf("world balance = %s, want -10000", bal.Balance)
	}
}

func TestMultiPostingTransaction(t *testing.T) {
	svc := NewService(NewMemStore())
	ctx := context.Background()

	// Fund Alice
	svc.PostTransaction(ctx, "default", CreateTransactionRequest{
		Postings: []Posting{
			{Source: "world", Destination: "users:alice", Asset: "USD/2", Amount: "10000"},
		},
	})

	// Alice splits payment: 9000 to merchant, 1000 to platform fees
	tx, err := svc.PostTransaction(ctx, "default", CreateTransactionRequest{
		Postings: []Posting{
			{Source: "users:alice", Destination: "merchants:042", Asset: "USD/2", Amount: "9000"},
			{Source: "users:alice", Destination: "platform:fees", Asset: "USD/2", Amount: "1000"},
		},
	})
	if err != nil {
		t.Fatalf("multi-posting: %v", err)
	}
	if len(tx.Postings) != 2 {
		t.Errorf("postings = %d, want 2", len(tx.Postings))
	}

	// Alice: 10000 - 9000 - 1000 = 0
	bal, _ := svc.GetAccountBalance(ctx, "default", "users:alice", "USD/2")
	if bal.Balance != "0" {
		t.Errorf("alice balance = %s, want 0", bal.Balance)
	}

	// Merchant: 9000
	bal, _ = svc.GetAccountBalance(ctx, "default", "merchants:042", "USD/2")
	if bal.Balance != "9000" {
		t.Errorf("merchant balance = %s, want 9000", bal.Balance)
	}

	// Platform fees: 1000
	bal, _ = svc.GetAccountBalance(ctx, "default", "platform:fees", "USD/2")
	if bal.Balance != "1000" {
		t.Errorf("fees balance = %s, want 1000", bal.Balance)
	}
}

func TestIdempotency(t *testing.T) {
	svc := NewService(NewMemStore())
	ctx := context.Background()

	tx1, err := svc.PostTransaction(ctx, "default", CreateTransactionRequest{
		Reference: "idempotent-001",
		Postings:  []Posting{{Source: "world", Destination: "users:alice", Asset: "USD/2", Amount: "5000"}},
	})
	if err != nil {
		t.Fatalf("first post: %v", err)
	}

	tx2, err := svc.PostTransaction(ctx, "default", CreateTransactionRequest{
		Reference: "idempotent-001",
		Postings:  []Posting{{Source: "world", Destination: "users:alice", Asset: "USD/2", Amount: "5000"}},
	})
	if err != nil {
		t.Fatalf("second post: %v", err)
	}

	if tx1.ID != tx2.ID {
		t.Errorf("idempotency failed: tx1.ID=%d, tx2.ID=%d", tx1.ID, tx2.ID)
	}

	// Balance should be 5000 (not 10000)
	bal, _ := svc.GetAccountBalance(ctx, "default", "users:alice", "USD/2")
	if bal.Balance != "5000" {
		t.Errorf("alice balance = %s, want 5000 (idempotent)", bal.Balance)
	}
}

func TestRevertTransaction(t *testing.T) {
	svc := NewService(NewMemStore())
	ctx := context.Background()

	// Fund Alice
	tx, _ := svc.PostTransaction(ctx, "default", CreateTransactionRequest{
		Postings: []Posting{{Source: "world", Destination: "users:alice", Asset: "USD/2", Amount: "10000"}},
	})

	// Revert
	revertTx, err := svc.RevertTransaction(ctx, "default", tx.ID)
	if err != nil {
		t.Fatalf("RevertTransaction: %v", err)
	}
	if revertTx.Metadata["revert_of"] == "" {
		t.Error("revert tx missing revert_of metadata")
	}

	// Alice balance should be 0
	bal, _ := svc.GetAccountBalance(ctx, "default", "users:alice", "USD/2")
	if bal.Balance != "0" {
		t.Errorf("alice balance after revert = %s, want 0", bal.Balance)
	}

	// Double revert should fail
	_, err = svc.RevertTransaction(ctx, "default", tx.ID)
	if err == nil {
		t.Error("expected error on double revert")
	}
}

func TestMultiCurrency(t *testing.T) {
	svc := NewService(NewMemStore())
	ctx := context.Background()

	svc.PostTransaction(ctx, "default", CreateTransactionRequest{
		Postings: []Posting{
			{Source: "world", Destination: "users:alice", Asset: "USD/2", Amount: "10000"},
			{Source: "world", Destination: "users:alice", Asset: "EUR/2", Amount: "8000"},
		},
	})

	balances, err := svc.GetAccountBalances(ctx, "default", "users:alice")
	if err != nil {
		t.Fatalf("GetAccountBalances: %v", err)
	}
	if len(balances) != 2 {
		t.Fatalf("balances = %d, want 2", len(balances))
	}

	balMap := map[string]string{}
	for _, b := range balances {
		balMap[b.Asset] = b.Balance
	}
	if balMap["USD/2"] != "10000" {
		t.Errorf("USD balance = %s, want 10000", balMap["USD/2"])
	}
	if balMap["EUR/2"] != "8000" {
		t.Errorf("EUR balance = %s, want 8000", balMap["EUR/2"])
	}
}

func TestMultiLedgerIsolation(t *testing.T) {
	svc := NewService(NewMemStore())
	ctx := context.Background()

	svc.PostTransaction(ctx, "ledger-a", CreateTransactionRequest{
		Postings: []Posting{{Source: "world", Destination: "users:alice", Asset: "USD/2", Amount: "5000"}},
	})
	svc.PostTransaction(ctx, "ledger-b", CreateTransactionRequest{
		Postings: []Posting{{Source: "world", Destination: "users:alice", Asset: "USD/2", Amount: "9000"}},
	})

	balA, _ := svc.GetAccountBalance(ctx, "ledger-a", "users:alice", "USD/2")
	balB, _ := svc.GetAccountBalance(ctx, "ledger-b", "users:alice", "USD/2")

	if balA.Balance != "5000" {
		t.Errorf("ledger-a alice = %s, want 5000", balA.Balance)
	}
	if balB.Balance != "9000" {
		t.Errorf("ledger-b alice = %s, want 9000", balB.Balance)
	}
}

func TestValidation(t *testing.T) {
	svc := NewService(NewMemStore())
	ctx := context.Background()

	tests := []struct {
		name string
		req  CreateTransactionRequest
	}{
		{"no postings", CreateTransactionRequest{}},
		{"empty source", CreateTransactionRequest{Postings: []Posting{{Source: "", Destination: "b", Asset: "USD/2", Amount: "100"}}}},
		{"empty destination", CreateTransactionRequest{Postings: []Posting{{Source: "a", Destination: "", Asset: "USD/2", Amount: "100"}}}},
		{"same src/dst", CreateTransactionRequest{Postings: []Posting{{Source: "a", Destination: "a", Asset: "USD/2", Amount: "100"}}}},
		{"zero amount", CreateTransactionRequest{Postings: []Posting{{Source: "a", Destination: "b", Asset: "USD/2", Amount: "0"}}}},
		{"negative amount", CreateTransactionRequest{Postings: []Posting{{Source: "a", Destination: "b", Asset: "USD/2", Amount: "-100"}}}},
		{"empty asset", CreateTransactionRequest{Postings: []Posting{{Source: "a", Destination: "b", Asset: "", Amount: "100"}}}},
		{"invalid amount", CreateTransactionRequest{Postings: []Posting{{Source: "a", Destination: "b", Asset: "USD/2", Amount: "abc"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.PostTransaction(ctx, "default", tt.req)
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}

func TestListTransactions(t *testing.T) {
	svc := NewService(NewMemStore())
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		svc.PostTransaction(ctx, "default", CreateTransactionRequest{
			Postings: []Posting{{Source: "world", Destination: "users:alice", Asset: "USD/2", Amount: "1000"}},
		})
	}

	txs, err := svc.ListTransactions(ctx, "default", 3, 0)
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if len(txs) != 3 {
		t.Errorf("got %d transactions, want 3", len(txs))
	}
	// Newest first
	if txs[0].ID < txs[1].ID {
		t.Error("expected newest first ordering")
	}
}
