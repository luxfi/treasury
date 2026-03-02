package reconciliation

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/luxfi/treasury/pkg/ledger"
	"github.com/luxfi/treasury/pkg/provider"
)

// BalanceProvider abstracts fetching balances from external providers.
// In production, this wraps the provider.Registry.
type BalanceProvider interface {
	GetBalances(ctx context.Context, providerName string) (map[string]string, error) // asset → balance
}

// Engine runs reconciliation between ledger and external providers.
type Engine struct {
	ledger   *ledger.Service
	balances BalanceProvider
	mu       sync.Mutex
	policies map[string]*Policy
	results  []*Result
	nextID   int
}

func NewEngine(ledgerSvc *ledger.Service, balances BalanceProvider) *Engine {
	return &Engine{
		ledger:   ledgerSvc,
		balances: balances,
		policies: make(map[string]*Policy),
	}
}

func (e *Engine) genID(prefix string) string {
	e.nextID++
	return fmt.Sprintf("%s_%d", prefix, e.nextID)
}

// AddPolicy registers a reconciliation policy.
func (e *Engine) AddPolicy(name, ledgerName, ledgerQuery, providerName string) *Policy {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := &Policy{
		ID:           e.genID("pol"),
		Name:         name,
		LedgerName:   ledgerName,
		LedgerQuery:  ledgerQuery,
		ProviderName: providerName,
		CreatedAt:    time.Now().UTC(),
	}
	e.policies[p.ID] = p
	return p
}

// Run executes reconciliation for a given policy.
func (e *Engine) Run(ctx context.Context, policyID string) (*Result, error) {
	e.mu.Lock()
	pol, ok := e.policies[policyID]
	if !ok {
		e.mu.Unlock()
		return nil, fmt.Errorf("policy %q not found", policyID)
	}
	e.mu.Unlock()

	result := &Result{
		ID:       e.genID("rec"),
		PolicyID: policyID,
		RunAt:    time.Now().UTC(),
	}

	// Get ledger balances for the queried account
	ledgerBalances, err := e.ledger.GetAccountBalances(ctx, pol.LedgerName, pol.LedgerQuery)
	if err != nil {
		result.Status = StatusError
		result.Error = fmt.Sprintf("ledger query failed: %v", err)
		e.storeResult(result)
		return result, nil
	}
	result.LedgerBalances = make(map[string]string)
	for _, b := range ledgerBalances {
		result.LedgerBalances[b.Asset] = b.Balance
	}

	// Get provider balances
	provBalances, err := e.balances.GetBalances(ctx, pol.ProviderName)
	if err != nil {
		result.Status = StatusError
		result.Error = fmt.Sprintf("provider query failed: %v", err)
		e.storeResult(result)
		return result, nil
	}
	result.ProviderBalances = provBalances

	// Compare
	allAssets := map[string]bool{}
	for a := range result.LedgerBalances {
		allAssets[a] = true
	}
	for a := range result.ProviderBalances {
		allAssets[a] = true
	}

	result.Status = StatusOK
	for asset := range allAssets {
		lStr := result.LedgerBalances[asset]
		pStr := result.ProviderBalances[asset]
		if lStr == "" {
			lStr = "0"
		}
		if pStr == "" {
			pStr = "0"
		}

		lBal, _ := new(big.Int).SetString(lStr, 10)
		pBal, _ := new(big.Int).SetString(pStr, 10)
		if lBal == nil {
			lBal = new(big.Int)
		}
		if pBal == nil {
			pBal = new(big.Int)
		}

		if lBal.Cmp(pBal) != 0 {
			diff := new(big.Int).Sub(lBal, pBal)
			result.Drifts = append(result.Drifts, Drift{
				Asset:           asset,
				LedgerBalance:   lBal.String(),
				ProviderBalance: pBal.String(),
				Difference:      diff.String(),
			})
			result.Status = StatusDrift
		}
	}

	e.storeResult(result)
	return result, nil
}

func (e *Engine) storeResult(r *Result) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.results = append(e.results, r)
}

// ListResults returns reconciliation results, newest first.
func (e *Engine) ListResults() []*Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]*Result, len(e.results))
	for i, r := range e.results {
		result[len(e.results)-1-i] = r
	}
	return result
}

// ListPolicies returns all policies.
func (e *Engine) ListPolicies() []*Policy {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]*Policy, 0, len(e.policies))
	for _, p := range e.policies {
		result = append(result, p)
	}
	return result
}

// RegistryBalanceProvider wraps provider.Registry to implement BalanceProvider.
type RegistryBalanceProvider struct {
	Registry *provider.Registry
}

func (p *RegistryBalanceProvider) GetBalances(ctx context.Context, providerName string) (map[string]string, error) {
	prov, err := p.Registry.Get(providerName)
	if err != nil {
		return nil, err
	}

	// List all accounts and aggregate balances
	accts, err := prov.ListAccounts(ctx, "")
	if err != nil {
		return nil, err
	}

	balances := map[string]string{}
	for _, acct := range accts {
		if acct.Balance != nil && acct.Balance.Currency != "" {
			cur := acct.Balance.Currency
			existing, _ := new(big.Int).SetString(balances[cur], 10)
			if existing == nil {
				existing = new(big.Int)
			}
			add, _ := new(big.Int).SetString(acct.Balance.Available, 10)
			if add == nil {
				add = new(big.Int)
			}
			existing.Add(existing, add)
			balances[cur] = existing.String()
		}
	}
	return balances, nil
}
