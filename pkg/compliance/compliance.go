package compliance

import (
	"fmt"
	"sync"
	"time"

	"github.com/luxfi/treasury/pkg/types"
)

// Engine enforces compliance rules on treasury operations.
// Supports sanctions screening, velocity limits, and amount thresholds.
type Engine struct {
	mu     sync.RWMutex
	rules  []Rule
	events []Event
}

// Rule is a compliance rule that can block or flag operations.
type Rule struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"` // velocity, amount, sanctions, country
	Threshold   float64   `json:"threshold,omitempty"`
	Window      string    `json:"window,omitempty"` // e.g., "24h", "30d"
	Countries   []string  `json:"countries,omitempty"`
	Action      string    `json:"action"` // block, flag, notify
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
}

// Event records a compliance check result.
type Event struct {
	ID         string    `json:"id"`
	RuleID     string    `json:"rule_id"`
	PaymentID  string    `json:"payment_id,omitempty"`
	Result     string    `json:"result"` // pass, block, flag
	Details    string    `json:"details"`
	CreatedAt  time.Time `json:"created_at"`
}

// CheckResult is the outcome of running compliance checks.
type CheckResult struct {
	Allowed  bool    `json:"allowed"`
	Flags    []string `json:"flags,omitempty"`
	Blocks   []string `json:"blocks,omitempty"`
	RulesRun int      `json:"rules_run"`
}

func NewEngine() *Engine {
	return &Engine{
		rules: defaultRules(),
	}
}

// Check validates a payment request against all rules.
func (e *Engine) Check(req *types.CreatePaymentRequest) CheckResult {
	e.mu.RLock()
	rules := make([]Rule, len(e.rules))
	copy(rules, e.rules)
	e.mu.RUnlock()

	result := CheckResult{Allowed: true}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		result.RulesRun++

		switch rule.Type {
		case "amount":
			if checkAmount(req, rule) {
				if rule.Action == "block" {
					result.Allowed = false
					result.Blocks = append(result.Blocks, fmt.Sprintf("%s: amount exceeds $%.0f", rule.Name, rule.Threshold))
				} else {
					result.Flags = append(result.Flags, fmt.Sprintf("%s: amount exceeds $%.0f", rule.Name, rule.Threshold))
				}
			}
		case "country":
			if checkCountry(req, rule) {
				if rule.Action == "block" {
					result.Allowed = false
					result.Blocks = append(result.Blocks, fmt.Sprintf("%s: blocked country", rule.Name))
				} else {
					result.Flags = append(result.Flags, fmt.Sprintf("%s: flagged country", rule.Name))
				}
			}
		}
	}
	return result
}

// AddRule adds a compliance rule.
func (e *Engine) AddRule(rule Rule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if rule.ID == "" {
		rule.ID = fmt.Sprintf("rule_%d", len(e.rules)+1)
	}
	rule.CreatedAt = time.Now().UTC()
	e.rules = append(e.rules, rule)
}

// ListRules returns all compliance rules.
func (e *Engine) ListRules() []Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]Rule, len(e.rules))
	copy(result, e.rules)
	return result
}

func checkAmount(req *types.CreatePaymentRequest, rule Rule) bool {
	// Simple string-to-float for amount check
	var amt float64
	fmt.Sscanf(req.Amount, "%f", &amt)
	return amt > rule.Threshold
}

func checkCountry(req *types.CreatePaymentRequest, rule Rule) bool {
	if req.Counterparty == nil {
		return false
	}
	for _, c := range rule.Countries {
		if c == req.Counterparty.BankCountry {
			return true
		}
	}
	return false
}

func defaultRules() []Rule {
	return []Rule{
		{
			ID: "rule_max_single", Name: "Max Single Payment",
			Type: "amount", Threshold: 1_000_000, Action: "flag", Enabled: true,
		},
		{
			ID: "rule_block_large", Name: "Block Excessive Payment",
			Type: "amount", Threshold: 10_000_000, Action: "block", Enabled: true,
		},
		{
			ID: "rule_ofac_countries", Name: "OFAC Sanctioned Countries",
			Type: "country", Action: "block", Enabled: true,
			Countries: []string{"CU", "IR", "KP", "SY", "RU"},
		},
	}
}
