# Treasury — integration patterns

`treasuryd` is a Postgres-backed double-entry ledger, a virtual-wallet
layer over that ledger, and a uniform provider interface for talking to
the rails and orchestrators of the regulated financial system. This
document describes the model and how to add a new provider adapter.

It is deliberately generic. Adapter packages live at
`pkg/provider/<key>/` and are referenced here by package path only.

## 1. Ledger model — `pkg/ledger`

Strict double-entry, multi-tenant by ledger name, immutable history.

| Type           | Role |
|----------------|------|
| `Account`      | Address-keyed (`users:alice:main`, `platform:fees`). Typed asset / liability / equity / revenue / expense. Tenancy via `ledger` column. |
| `Transaction`  | Atomic group of postings. Carries `Reference` (idempotency key) and metadata. Reversal is by *new* transaction with `RevertedAt` marker — never mutation. |
| `Posting`      | A single `source → destination` movement of an `Asset` (`USD/2`, `BTC/8`, `LQDTY/18` — currency / decimals) for an `Amount` (arbitrary-precision decimal string). |
| `Move`         | Per-account materialisation of a posting. Two moves per posting (source = debit, destination = credit). Carries `PostCommitInputs/Outputs` so balances are reconstructable per row. |
| `Volumes`      | Running totals of inputs (credits) and outputs (debits) per `account + asset`. Balance = `inputs − outputs`. |
| `LogEntry`     | Append-only audit trail keyed by `IdempotencyKey`; supports hash chaining for tamper-evidence. |

Stores: `pkg/ledger/memstore.go` (tests / local) and `pkg/ledger/pgstore.go`
(production, pgx). Same `Store` interface; the service layer
(`service.go`) is store-agnostic.

**Multi-tenancy.** Every row carries `ledger string`. There is no
shared-state assumption between ledgers. Pick one ledger per tenant /
business unit / regulated entity, never mix.

**Immutability.** Moves and postings are never updated in place. A
mistake is corrected by a reversing transaction.

## 2. Virtual wallets — `pkg/wallet`

A `Wallet` is a multi-currency virtual account backed by ledger account
`wallets:{wallet_id}:main`. The wallet API is a thin, opinionated
surface over the ledger so product code never writes raw postings.

| Operation                  | Ledger effect |
|----------------------------|---------------|
| `Credit`                   | `source → wallets:{id}:main` (default source = `world`). |
| `Debit`                    | `wallets:{id}:main → destination`. |
| `CreateHold`               | `wallets:{id}:main → wallets:{id}:hold:{hold_id}` — funds reserved but not paid out. |
| `ConfirmHold` (full/part)  | `hold sub-account → destination`. Partial confirms supported; `Hold.Remaining` tracks balance. |
| `VoidHold`                 | `hold sub-account → wallets:{id}:main`. |

`WalletBalance` returns `Available` (main balance only) / `Held` (sum
of open holds) / `Total`. Holds are the only correct way to represent
authorised-but-not-yet-captured value (card auths, pending wires,
escrow on a trade).

## 3. Provider interface — `pkg/provider`

```go
type Provider interface {
    Name() string

    CreateAccount(ctx, orgID, *CreateAccountRequest) (*BankAccount, error)
    GetAccount(ctx, accountID) (*BankAccount, error)
    ListAccounts(ctx, orgID) ([]*BankAccount, error)
    GetBalance(ctx, accountID) (*Balance, error)

    CreatePayment(ctx, *CreatePaymentRequest) (*Payment, error)
    GetPayment(ctx, paymentID) (*Payment, error)
    ListPayments(ctx, accountID) ([]*Payment, error)
    CancelPayment(ctx, paymentID) error

    GetFXQuote(ctx, sellCcy, buyCcy, amount, fixedSide) (*FXQuote, error)
    CreateFXConversion(ctx, *CreateFXConversionRequest) (*FXConversion, error)
    ListFXConversions(ctx) ([]*FXConversion, error)

    CreateCounterparty(ctx, *Counterparty) (*Counterparty, error)
    ListCounterparties(ctx) ([]*Counterparty, error)

    Capabilities() *ProviderCapability
}
```

Every backend implements the *same* interface. Methods a provider does
not support return `provider.ErrNotSupported`. `Capabilities()`
advertises what the adapter can actually do so the API layer can route
intelligently.

### Categories of adapter

The interface is the same; the adapter *type* depends on what real-world
substrate sits behind it. Today's tree covers seven categories:

| Category | Purpose | Example package paths |
|----------|---------|-----------------------|
| **Bank-rail provider** | Direct ACH / wire / RTP / FedNow against a chartered counterparty. | `pkg/provider/column`, `pkg/provider/increase`, `pkg/provider/mercury`, `pkg/provider/unit` |
| **Treasury / multi-rail orchestrator** | Unified payment-ops API across many rails and banks; ledger-like primitives over a bank-of-record. | `pkg/provider/moderntreasury` |
| **Payment gateway** | Card acceptance, treasury balances, payouts. | `pkg/provider/column`, `pkg/provider/square` |
| **Card issuer** | Virtual + physical card issuing, authorisation, settlement. | `pkg/provider/lithic`, `pkg/provider/marqeta` |
| **Bank-link aggregator** | Account linking, balance read, transaction history, identity. | `pkg/provider/plaid` |
| **FX / cross-border** | Multi-currency accounts, FX quotes and conversions, local rails in each market. | `pkg/provider/currencycloud`, `pkg/provider/wise` |
| **Mobile-money operator** | Carrier-driven wallets and merchant collections in mobile-first markets. | `pkg/provider/mpesa` |
| **Cross-chain bridge** | Stablecoin movement and on/off-ramp between chains. | `pkg/provider/bridge` |

A single deployment can register any subset. Routing between adapters
is the responsibility of the calling service, informed by
`Capabilities()` and counterparty metadata.

### Adding a new adapter

Mirror the shape of `pkg/provider/wise/` (smallest reference) or
`pkg/provider/mpesa/` (full reference with auth, signed requests, and
webhook handling):

1. **Package layout.** `pkg/provider/<key>/<key>.go` for the adapter,
   `<key>_test.go` for tests, `webhook.go` (optional) for inbound
   callbacks, `doc.go` for package commentary. Lower-case `<key>`
   matches the env-var prefix and `Provider.Name()`.
2. **Config struct.** A plain struct with `BaseURL`, credentials, and
   any per-environment switches (sandbox vs production URL constants
   exported from the package).
3. **Constructor.** `func New(cfg Config) *Adapter` — no globals, no
   `init()` side effects.
4. **Implement `provider.Provider`.** Methods you don't support return
   `provider.ErrNotSupported`. Fill `Capabilities()` honestly — the API
   layer uses it.
5. **Idempotency.** Map the treasury `reference` field onto the
   provider's idempotency-key header (every modern API has one). Never
   retry a write without one.
6. **Errors.** Wrap upstream errors with `fmt.Errorf("<adapter>: %w", err)`
   so callers can `errors.Is` against `provider.ErrNotSupported` and the
   adapter name shows up in logs.
7. **Tests.** Round-trip the happy path and at least one rejection path
   against an in-process HTTP server (`httptest`). No network in CI.
8. **Conditional registration.** In `cmd/treasuryd/main.go` add a block
   gated on the adapter's primary credential env var:

   ```go
   if key := os.Getenv("<KEY>_API_KEY"); key != "" {
       registry.Register(<key>.New(<key>.Config{ ... }))
       log.Info().Msg("<key> provider registered")
   }
   ```

   This is the *only* registration site. No build tags, no per-adapter
   binaries — one `treasuryd`, configured by env.

## 4. Reconciliation engine — `pkg/reconciliation`

The ledger is the system of record; providers are the rails. They
drift. The reconciliation engine catches drift early.

A `Policy` pairs a ledger account-address pattern (e.g.,
`providers:<name>:*`) with a provider name. A run produces a `Result`:
`StatusOK` (matched), `StatusDrift` (per-asset `Drift` rows showing
ledger balance, provider balance, and the difference), or `StatusError`
(upstream failure). Drift is human-investigated; the engine does not
auto-correct.

Run policies on a cron — at minimum daily for low-volume rails, hourly
or per-transaction for high-volume rails.

## 5. Compliance — `pkg/compliance`

`Engine.Check(*CreatePaymentRequest) CheckResult` runs every enabled
rule before a payment leaves the system. Rules have a `Type`, an
`Action` (`block` / `flag` / `notify`), and rule-specific fields.

Default rules (see `defaultRules()`):

- **Max single payment** — `amount > $1M` → flag.
- **Block excessive payment** — `amount > $10M` → block.
- **OFAC sanctioned countries** — `counterparty.BankCountry ∈ {CU, IR, KP, SY, RU}` → block.

Velocity-window rules (`24h`, `30d` etc.) are supported by the schema;
extend by adding cases in `Check` and implementing the window query.
Every check result is recorded as an `Event` for audit.

This is the policy floor. Regulated deployments layer KYC, beneficial-
ownership, PEP, and adverse-media screening on top via additional rules
and external screening providers — but no payment leaves the system
without the default rules passing first.

## 6. Idempotency

The `Reference` field on `Transaction` and `CreatePaymentRequest` is
the contract for safe retries. Behaviour:

- Same `reference` + same payload → returns the original result; no new
  ledger transaction, no new provider call.
- Same `reference` + different payload → conflict error.
- Empty `reference` → not idempotent; caller assumes all risk.

Provider adapters MUST forward the reference to the upstream
idempotency-key header. Treasury never retries a write without one.

## 7. Operational notes

- One ledger per regulated entity / tenant. No cross-tenant postings.
- Reversal by reversing transaction; never by `UPDATE`.
- The ledger is the source of truth; if a provider says otherwise,
  reconciliation flags drift for a human — it does not silently mutate
  ledger state.
- `compose.yml` provisions a local Postgres for development. Production
  uses the deployment's managed Postgres.
