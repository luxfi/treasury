# Lux Treasury

Institutional treasury management platform with 13 banking/payment providers, double-entry ledger, and OFAC compliance.

## Providers

### Banking
| Provider | Capabilities |
|----------|-------------|
| **Mercury** | Business banking, ACH, wires |
| **Column** | Direct Fed member, ACH/wire/RTP/FedNow |
| **Bridge** (Stripe) | Stablecoin orchestration, on/off ramp |
| **Increase** | Banking infrastructure, ACH/wire/RTP/checks |
| **Modern Treasury** | Payment operations, ledger, virtual accounts |
| **Unit** | BaaS, white-label checking/savings |

### Payments
| Provider | Capabilities |
|----------|-------------|
| **Stripe** | Treasury BaaS, financial accounts |
| **Square** | Payments, commerce, invoicing |
| **Wise** | International transfers, multi-currency, FX |
| **CurrencyCloud** (Visa) | Institutional FX, cross-border payments |

### Cards
| Provider | Capabilities |
|----------|-------------|
| **Lithic** | Virtual + physical cards, spend controls, real-time auth |
| **Marqeta** | Card issuing, JIT funding, spend controls |

### Compliance
| Provider | Capabilities |
|----------|-------------|
| **Plaid** | Bank account linking, identity verification |

## Features

- **Multi-Provider Banking** — Unified API across 13 providers
- **Double-Entry Ledger** — Journal entries with automatic balancing
- **OFAC Sanctions Screening** — SDN list checks on all counterparties
- **FX Conversions** — Cross-border payments with competitive FX rates
- **Card Issuing** — Virtual and physical cards with spend controls
- **Payment Rails** — ACH, wire, RTP, FedNow, SEPA, SWIFT, checks

## Architecture

```
Client Request
      |
  [treasuryd :8091]
      |
  +---+---+---+
  |   |   |   |
 API Ledger Compliance
  |              |
  +------+-------+
  |      |       |
Banking Payments Cards
  |      |       |
 6 prov  4 prov  2 prov + Plaid
```

## Quick Start

```bash
# Build
go build -o treasuryd ./cmd/treasuryd/

# Run (configure at least one provider)
MERCURY_API_KEY=... ./treasuryd

# Or with multiple providers
STRIPE_SECRET_KEY=... COLUMN_API_KEY=... WISE_API_TOKEN=... ./treasuryd
```

## API

All endpoints under `/api/v1/` on port `:8091` (configurable via `TREASURY_LISTEN`).

### Accounts
| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/{provider}/accounts` | Create bank account |
| `GET` | `/api/v1/{provider}/accounts` | List accounts |
| `GET` | `/api/v1/{provider}/accounts/{id}` | Get account |
| `GET` | `/api/v1/{provider}/accounts/{id}/balance` | Get balance |

### Payments
| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/{provider}/payments` | Create payment |
| `GET` | `/api/v1/{provider}/payments` | List payments |
| `GET` | `/api/v1/{provider}/payments/{id}` | Get payment |
| `DELETE` | `/api/v1/{provider}/payments/{id}` | Cancel payment |

### FX
| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/{provider}/fx/quote` | Get FX quote |
| `POST` | `/api/v1/{provider}/fx/convert` | Execute FX conversion |
| `GET` | `/api/v1/{provider}/fx/conversions` | List conversions |

### Counterparties
| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/{provider}/counterparties` | Create counterparty |
| `GET` | `/api/v1/{provider}/counterparties` | List counterparties |

### Ledger
| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/ledger/entries` | Create journal entry |
| `GET` | `/api/v1/ledger/entries` | List entries |
| `GET` | `/api/v1/ledger/accounts/{id}/balance` | Account balance |

### Compliance
| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/compliance/screen` | OFAC sanctions check |
| `GET` | `/api/v1/compliance/status/{entity}` | Screening status |

## Configuration

| Env Var | Description |
|---------|-------------|
| `TREASURY_LISTEN` | Listen address (default `:8091`) |
| `MERCURY_API_KEY` | Mercury |
| `COLUMN_API_KEY` | Column |
| `BRIDGE_API_KEY` | Bridge |
| `INCREASE_API_KEY` | Increase |
| `MODERN_TREASURY_ORG_ID` / `MODERN_TREASURY_API_KEY` | Modern Treasury |
| `UNIT_TOKEN` | Unit |
| `STRIPE_SECRET_KEY` | Stripe Treasury |
| `SQUARE_ACCESS_TOKEN` | Square |
| `WISE_API_TOKEN` / `WISE_PROFILE_ID` | Wise |
| `CURRENCYCLOUD_LOGIN_ID` / `CURRENCYCLOUD_API_KEY` | CurrencyCloud |
| `LITHIC_API_KEY` | Lithic |
| `MARQETA_APP_TOKEN` / `MARQETA_ACCESS_TOKEN` | Marqeta |
| `PLAID_CLIENT_ID` / `PLAID_SECRET` | Plaid |

## Docker

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o treasuryd ./cmd/treasuryd/
docker build --platform linux/amd64 -t ghcr.io/luxfi/treasury:latest .
```

## License

Copyright 2024-2026, Lux Partners Limited. All rights reserved.
