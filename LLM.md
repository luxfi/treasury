# Lux Treasury — Financial Infrastructure

Go module: `github.com/luxfi/treasury`

## What it does

Institutional treasury management: double-entry ledger, 13+ payment providers, virtual wallets with hold/confirm/void, automated reconciliation, OFAC compliance.

Mirrors the Lux backend pattern (like broker/cex/dex): standalone Go binary with `pkg/` packages, chi router, zerolog.

## Architecture

```
treasuryd (Go binary, :8091)
  ├── pkg/ledger/           — Postgres-backed double-entry ledger
  │   ├── types.go          — Account, Transaction, Posting, Move, Volumes
  │   ├── store.go          — Store interface (repository pattern)
  │   ├── pgstore.go        — Postgres implementation (pgx, NUMERIC amounts)
  │   ├── memstore.go       — In-memory for tests
  │   └── service.go        — Business logic (atomic multi-posting, revert, idempotency)
  ├── pkg/wallet/           — Virtual wallets on top of ledger
  │   ├── types.go          — Wallet, Hold, WalletBalance
  │   └── service.go        — Credit/debit, hold/confirm/void
  ├── pkg/reconciliation/   — Ledger vs provider auto-matching
  │   ├── types.go          — Policy, Result, Drift
  │   └── engine.go         — Compare & detect drift
  ├── pkg/provider/         — 13 payment provider adapters (unified interface)
  ├── pkg/compliance/       — OFAC sanctions, velocity limits, amount thresholds
  ├── pkg/api/              — REST API (chi router)
  └── pkg/types/            — Shared types (BankAccount, Payment, FXQuote, etc.)
```

## Key Design Decisions

- **Amounts**: Strings in Go, `NUMERIC` in Postgres — arbitrary precision, no floats
- **Ledger accounts**: Address-based with colon segments (`wallets:alice:main`, `platform:fees`)
- **Multi-ledger**: Tenant isolation via `ledger` column (like multi-tenant database)
- **Idempotency**: Transaction `reference` field — duplicate references return existing tx
- **Immutable moves**: Append-only moves table with post-commit volumes for balance computation
- **Wallet holds**: Funds moved to `wallets:{id}:hold:{hold_id}` sub-accounts in the ledger

## Build & Run

```bash
go build ./...                    # Build
go test ./...                     # Test (22 tests, all in-memory)
go build -o treasuryd ./cmd/treasuryd/  # Binary

# Run with Postgres
DATABASE_URL=postgres://treasury:treasury@localhost:5433/treasury ./treasuryd

# Run with in-memory (dev)
./treasuryd

# Docker Compose (hanzoai/sql + hanzoai/kv)
docker compose up
```

## API Endpoints

```
GET    /healthz
GET    /api/v1/providers
GET    /api/v1/providers/capabilities

# Ledger
POST   /api/v1/ledger/accounts              — Create account
GET    /api/v1/ledger/accounts              — List accounts
GET    /api/v1/ledger/accounts/{addr}/balances  — All balances for account
GET    /api/v1/ledger/accounts/{addr}/balance   — Single asset balance (?asset=USD/2)
POST   /api/v1/ledger/transactions          — Post transaction (multi-posting, atomic)
GET    /api/v1/ledger/transactions          — List transactions
GET    /api/v1/ledger/transactions/{id}     — Get transaction
POST   /api/v1/ledger/transactions/{id}/revert  — Revert transaction

# Bank Accounts & Payments (provider passthrough)
POST   /api/v1/accounts
GET    /api/v1/accounts
POST   /api/v1/payments
GET    /api/v1/payments

# FX
GET    /api/v1/fx/quote
POST   /api/v1/fx/convert
GET    /api/v1/fx/rates

# Compliance
POST   /api/v1/compliance/check
GET    /api/v1/compliance/rules
```

## Providers (13)

Mercury, Column, Increase, Modern Treasury, Stripe, Wise, CurrencyCloud, Square, Bridge, Unit, Lithic, Marqeta, Plaid

## Dependencies

- `pgx/v5` — Postgres driver
- `chi/v5` — HTTP router
- `zerolog` — Structured logging
- `cors` — CORS middleware

## Infrastructure

- `compose.yml` uses `ghcr.io/hanzoai/sql` (not upstream postgres) and `ghcr.io/hanzoai/kv`
- Port 8091 (same as CEX — coordinate if running both)

## How it fits in the stack

```
liquidity/ats imports:
  luxfi/broker     → trade execution (16 brokerage providers)
  luxfi/cex        → matching + compliance
  luxfi/dex        → orderbook engine
  luxfi/treasury   → cash ledger + wallets + reconciliation
  luxfi/captable   → equity ownership + securities lifecycle
```
