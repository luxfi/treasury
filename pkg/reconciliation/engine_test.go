package reconciliation

import (
	"context"
	"testing"

	"github.com/luxfi/treasury/pkg/ledger"
)

type mockBalanceProvider struct {
	balances map[string]string
}

func (m *mockBalanceProvider) GetBalances(_ context.Context, _ string) (map[string]string, error) {
	return m.balances, nil
}

func setup(providerBalances map[string]string) (*Engine, *ledger.Service) {
	store := ledger.NewMemStore()
	svc := ledger.NewService(store)
	bp := &mockBalanceProvider{balances: providerBalances}
	eng := NewEngine(svc, bp)
	return eng, svc
}

func TestReconciliationOK(t *testing.T) {
	eng, lsvc := setup(map[string]string{"USD/2": "10000"})
	ctx := context.Background()

	// Set ledger balance to match provider
	lsvc.PostTransaction(ctx, "default", ledger.CreateTransactionRequest{
		Postings: []ledger.Posting{
			{Source: "world", Destination: "providers:stripe", Asset: "USD/2", Amount: "10000"},
		},
	})

	pol := eng.AddPolicy("stripe-daily", "default", "providers:stripe", "stripe")
	result, err := eng.Run(ctx, pol.ID)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != StatusOK {
		t.Errorf("status = %s, want ok", result.Status)
	}
	if len(result.Drifts) != 0 {
		t.Errorf("drifts = %d, want 0", len(result.Drifts))
	}
}

func TestReconciliationDrift(t *testing.T) {
	eng, lsvc := setup(map[string]string{"USD/2": "9500"})
	ctx := context.Background()

	// Ledger says 10000, provider says 9500 → drift of 500
	lsvc.PostTransaction(ctx, "default", ledger.CreateTransactionRequest{
		Postings: []ledger.Posting{
			{Source: "world", Destination: "providers:stripe", Asset: "USD/2", Amount: "10000"},
		},
	})

	pol := eng.AddPolicy("stripe-check", "default", "providers:stripe", "stripe")
	result, _ := eng.Run(ctx, pol.ID)

	if result.Status != StatusDrift {
		t.Errorf("status = %s, want drift", result.Status)
	}
	if len(result.Drifts) != 1 {
		t.Fatalf("drifts = %d, want 1", len(result.Drifts))
	}
	if result.Drifts[0].Difference != "500" {
		t.Errorf("difference = %s, want 500", result.Drifts[0].Difference)
	}
}

func TestReconciliationMultiAsset(t *testing.T) {
	eng, lsvc := setup(map[string]string{"USD/2": "10000", "EUR/2": "5000"})
	ctx := context.Background()

	lsvc.PostTransaction(ctx, "default", ledger.CreateTransactionRequest{
		Postings: []ledger.Posting{
			{Source: "world", Destination: "providers:wise", Asset: "USD/2", Amount: "10000"},
			{Source: "world", Destination: "providers:wise", Asset: "EUR/2", Amount: "4800"},
		},
	})

	pol := eng.AddPolicy("wise-fx", "default", "providers:wise", "wise")
	result, _ := eng.Run(ctx, pol.ID)

	if result.Status != StatusDrift {
		t.Errorf("status = %s, want drift (EUR mismatch)", result.Status)
	}

	// USD matches (10000=10000), EUR drifts (4800 vs 5000 = -200)
	found := false
	for _, d := range result.Drifts {
		if d.Asset == "EUR/2" {
			found = true
			if d.Difference != "-200" {
				t.Errorf("EUR diff = %s, want -200", d.Difference)
			}
		}
		if d.Asset == "USD/2" {
			t.Error("USD should not have drift")
		}
	}
	if !found {
		t.Error("EUR drift not found")
	}
}

func TestListResults(t *testing.T) {
	eng, lsvc := setup(map[string]string{"USD/2": "1000"})
	ctx := context.Background()

	lsvc.PostTransaction(ctx, "default", ledger.CreateTransactionRequest{
		Postings: []ledger.Posting{{Source: "world", Destination: "providers:test", Asset: "USD/2", Amount: "1000"}},
	})

	pol := eng.AddPolicy("test", "default", "providers:test", "test")
	eng.Run(ctx, pol.ID)
	eng.Run(ctx, pol.ID)

	results := eng.ListResults()
	if len(results) != 2 {
		t.Errorf("results = %d, want 2", len(results))
	}
}
