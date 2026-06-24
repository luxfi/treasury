# Lux first-party adapter audit — 2026-05-23

Scope: 14 treasury adapters under `~/work/lux/treasury/pkg/provider/` and
4 forex adapters under `~/work/lux/forex/pkg/provider/`. Read-only pass.
No code changed.

Interfaces inspected:
- `treasury/pkg/provider/provider.go:13` — `Provider` (Name, Create/Get/List Account,
  GetBalance, Create/Get/List/Cancel Payment, GetFXQuote, Create/List FXConversion,
  Create/List Counterparty, Capabilities)
- `forex/pkg/provider/provider.go:37` — `FXProvider` (Name, GetFXPairs, GetFXQuote,
  CreateFXOrder, GetFXPositions, CloseFXPosition, GetFXRate) + optional
  `WebhookHandler` (`forex/pkg/provider/provider.go:32`)

Conventions used in this table: **Y** = correct/complete; **partial** = present
but with one or more named gaps; **N** = absent/stub/wrong; **N/A** = not
applicable for this provider type. Webhook column refers to inbound provider
callback handling with signature verification; for treasury adapters there is no
`WebhookHandler` interface at all (the treasury Provider interface omits it —
see `treasury/pkg/provider/provider.go:13`), so all treasury rows are N unless
the adapter ships its own callback parser (only mpesa does).

## Summary (1 row per adapter, 18 rows)

| Adapter | Coverage | Auth | Pagination | Webhook | Errors | Idempotency | Tests | Capabilities | Registered | Doc.go | Overall |
|---|---|---|---|---|---|---|---|---|---|---|---|
| treasury/bridge | partial | Y | N | N | partial | N | N | Y | Y | N | needs work |
| treasury/column | partial | Y | partial | N | partial | N | N | Y | Y | N | needs work |
| treasury/currencycloud | Y | Y (OAuth + cache) | N | N | partial | N | N | Y | Y | N | one-day fix |
| treasury/increase | partial | Y | N | N | partial | N | N | Y | Y | N | needs work |
| treasury/lithic | partial | Y | N | N | partial | N | N | Y | Y | N | needs work |
| treasury/marqeta | N (stub) | Y | N | N | partial | N | N | Y | Y | N | rewrite |
| treasury/mercury | partial | Y | N | N | partial | N | N | Y | Y | N | needs work |
| treasury/moderntreasury | partial | Y (basic) | N | N | partial | N | N | Y | Y | N | needs work |
| treasury/mpesa | Y | Y (OAuth + RSA-OAEP) | N/A | partial (no sig verify) | Y | partial | Y | Y | Y | Y | one-day fix |
| treasury/plaid | N (stub) | partial | N | N | partial | N | N | Y | Y | N | rewrite |
| treasury/square | partial | Y | N | N | partial | partial (1 site only) | N | Y | Y | N | needs work |
| treasury/unit | partial | Y | N | N | partial | N | N | Y | Y | N | needs work |
| treasury/wise | partial | Y | N | N | partial | N | N | Y | Y | N | needs work |
| forex/circle | partial | Y | N/A | N | partial | Y (1 site) | partial | N/A | Y | Y | one-day fix |
| forex/currencycloud | Y | Y (OAuth + cache, race) | N | Y (parse only, no sig) | Y | N | Y | N/A | Y | Y | one-day fix |
| forex/lmax | partial | partial (no session login) | N/A | N | partial | N | partial | N/A | Y | Y | needs work |
| forex/openpay | N (stub) | N | N | N | N | N | N | N/A | Y | Y | rewrite |

`N/A` in Capabilities for forex rows: FXProvider does not have a
`Capabilities()` method; capability data is implicit in `GetFXPairs`.

## Per-adapter findings

### treasury/bridge — `treasury/pkg/provider/bridge/bridge.go`
- **Coverage**: Implements Payment + GetFXQuote. CreateAccount / GetAccount / ListAccounts / GetBalance / CreateFXConversion / ListFXConversions / Counterparties / CancelPayment all `ErrNotSupported` (`bridge.go:69-83,171-220`). For a stablecoin orchestrator that is a defensible choice, but it means "list balances" is unavailable.
- **Auth**: Static `Api-Key` header — correct per Bridge spec (`bridge.go:50`).
- **Pagination**: `ListPayments` reads `/v0/transfers` with no cursor/limit (`bridge.go:144`).
- **Webhook**: None. Bridge does emit transfer.updated webhooks; not handled.
- **Errors**: `bridge API error %d: %s` (`bridge.go:64`) — status + raw body; no parsed code/request-id.
- **Idempotency**: No `Idempotency-Key` header on POST (`bridge.go:46-67`). Bridge requires it for transfer creation per their docs.
- **Tests**: None. No `bridge_test.go`.
- **Capabilities**: Populated (`bridge.go:222`).
- **Registration**: `BRIDGE_API_KEY` gate (`cmd/treasuryd/main.go:107`).
- **Doc.go**: Missing.
- **Recommended action**: Add idempotency key + webhook handler + tests before relying for production stablecoin orchestration.
- **Effort**: M.

### treasury/column — `treasury/pkg/provider/column/column.go`
- **Coverage**: Accounts + ACH/Wire/Book payments + Counterparties. FX, Cancel, FedNow / RTP / check unsupported despite Capabilities advertising them (`column.go:298-310,362`).
- **Auth**: Bearer token (`column.go:51`) — matches Column spec.
- **Pagination**: `ListAccounts` and `ListPayments` ignore Column's `limit` / `starting_after` cursor (`column.go:143-177,263-296`). `GetPayment` does a try-ACH-then-wire dance that swallows the first error (`column.go:237-243`).
- **Webhook**: None. Column publishes signed webhooks for transfer state changes — unhandled.
- **Errors**: Raw body in error string (`column.go:65`). No parsed Column error envelope.
- **Idempotency**: No `Idempotency-Key` header. Column accepts and recommends it on every POST.
- **Tests**: None.
- **Capabilities**: Returns rtp/fednow/check as advertised features but the code doesn't route them (`column.go:362`).
- **Registration**: `COLUMN_API_KEY` (`cmd/treasuryd/main.go:78`).
- **Doc.go**: Missing.
- **Recommended action**: Add webhook + idempotency + tests + RTP/FedNow/check routing OR trim Capabilities to truth.
- **Effort**: M.

### treasury/currencycloud — `treasury/pkg/provider/currencycloud/currencycloud.go`
- **Coverage**: Full surface — Accounts, Payments (Cancel = delete), FX quote/conversion/list, Counterparties (beneficiaries). Most-complete adapter.
- **Auth**: Form login → bearer token, 25-min cache, mutex-protected (`currencycloud.go:47-74`). Correct per CurrencyCloud docs.
- **Pagination**: `ListAccounts` and `ListPayments` and `ListFXConversions` ignore `per_page` / `page` (`currencycloud.go:174-206,299-329,418-454`). CurrencyCloud paginates 25/page; default returns just page 1.
- **Webhook**: None on the treasury side (forex side has it — different file).
- **Errors**: Raw body in error (`currencycloud.go:108`). CurrencyCloud returns `error_code` / `error_messages[]` envelope — unparsed.
- **Idempotency**: No `unique_request_id` on payment creation; CurrencyCloud supports it on `/v2/payments/create`.
- **Tests**: None (only forex/currencycloud has tests).
- **Capabilities**: Populated, accurate (`currencycloud.go:517`).
- **Registration**: `CURRENCYCLOUD_LOGIN_ID` (`cmd/treasuryd/main.go:39`).
- **Doc.go**: Missing.
- **Recommended action**: Add pagination + parsed error envelope + `unique_request_id` + tests; lift webhook handler from `forex/pkg/provider/currencycloud/currencycloud.go:244`.
- **Effort**: S.

### treasury/increase — `treasury/pkg/provider/increase/increase.go`
- **Coverage**: Accounts, ACH/Wire payments, balance. No RTP / check (despite Capabilities `increase.go:64`), no FX (correct), no counterparties (Increase has external_accounts — unimplemented), no cancel.
- **Auth**: Bearer (`increase.go:47`). Correct.
- **Pagination**: `ListAccounts` / `ListPayments` no cursor (`increase.go:111-133,249-275`). Increase uses `cursor` / `limit`.
- **Webhook**: None. Increase signs webhooks with HMAC-SHA256 — unhandled.
- **Errors**: `increase error %d: %s` (`increase.go:56`); ignores Increase's typed `errors[]` envelope. Also swallows JSON unmarshal errors throughout (`increase.go:86,104,124,144,175,201,242,265`).
- **Idempotency**: No `Idempotency-Key`. Increase strongly recommends it.
- **Tests**: None.
- **Capabilities**: Populated.
- **Registration**: `INCREASE_API_KEY` (`cmd/treasuryd/main.go:143`).
- **Doc.go**: Doc comment on package line (`increase.go:16-17`) but no separate doc.go.
- **Recommended action**: Add idempotency, fix unmarshal error handling, add RTP + check routes + counterparties + webhook + tests.
- **Effort**: M.

### treasury/lithic — `treasury/pkg/provider/lithic/lithic.go`
- **Coverage**: Financial accounts, balance, card transactions, void. CreatePayment hits `/simulate/authorize` — that is the **sandbox-only** endpoint (`lithic.go:157`) and will fail in production.
- **Auth**: `api-key <KEY>` header (`lithic.go:47`). Correct for Lithic.
- **Pagination**: `ListAccounts` / `ListPayments` ignore cursor.
- **Webhook**: None. Lithic signs webhooks (Svix-style) — unhandled.
- **Errors**: Raw body. JSON unmarshal errors swallowed throughout (`lithic.go:80,96,114,138,162,180,204`).
- **Idempotency**: No `Idempotency-Key`.
- **Tests**: None.
- **Capabilities**: Populated.
- **Registration**: `LITHIC_API_KEY` (`cmd/treasuryd/main.go:174`).
- **Doc.go**: Comment on package line, no separate file.
- **Recommended action**: Rewrite CreatePayment to use real `/cards` + `/external_payments` flow (not `/simulate/`); add webhook + tests + idempotency. The simulate endpoint is the smoking gun — this adapter cannot run in prod as-is.
- **Effort**: L.

### treasury/marqeta — `treasury/pkg/provider/marqeta/marqeta.go`
- **Coverage**: 11 of 13 interface methods return `ErrNotSupported` (`marqeta.go:71-112`). Only `Name()` and `Capabilities()` and the `doRequest` plumbing exist. The Capabilities block claims `card_issuing`, `virtual_cards`, `physical_cards`, `spend_controls`, `tokenization` — none of which the code implements (`marqeta.go:114`).
- **Auth**: Basic auth with app_token:access_token (`marqeta.go:52`) — correct shape, never exercised.
- **Pagination**: N/A (no list methods implemented).
- **Webhook**: None.
- **Errors**: Raw body (`marqeta.go:66`); will never fire.
- **Idempotency**: N/A.
- **Tests**: None.
- **Capabilities**: Lies — advertises features the code does not implement.
- **Registration**: `MARQETA_APP_TOKEN` (`cmd/treasuryd/main.go:97`).
- **Doc.go**: Missing.
- **Recommended action**: Rewrite from scratch. Implement at least the user, card-product, card, GPA-order, and transaction surfaces. Until then it should not be registered at all — registering a stub that satisfies `Provider` but answers every method with `ErrNotSupported` is a foot-gun for routing logic.
- **Effort**: L.

### treasury/mercury — `treasury/pkg/provider/mercury/mercury.go`
- **Coverage**: GetAccount, ListAccounts, GetBalance, ListPayments (transactions). CreatePayment / GetPayment / CancelPayment / counterparties / FX all unsupported (`mercury.go:67-69,162-168,212-230`). Note: Mercury **does** have a `POST /transactions` API for ACH transfers — claim that "Mercury uses dashboard for transfers" (`mercury.go:163` comment) is partially wrong; recipient-payment APIs exist.
- **Auth**: Bearer (`mercury.go:48`). Correct.
- **Pagination**: `ListAccounts` and `ListPayments` no `limit`/`offset` (`mercury.go:109-152,170-210`).
- **Webhook**: None. Mercury has webhook subscriptions for transaction.created.
- **Errors**: Raw body (`mercury.go:62`).
- **Idempotency**: N/A (no creation calls implemented).
- **Tests**: None.
- **Capabilities**: Advertises `ach`, `wire` though CreatePayment is unsupported (`mercury.go:235`).
- **Registration**: `MERCURY_API_KEY` (`cmd/treasuryd/main.go:125`).
- **Doc.go**: Missing.
- **Recommended action**: Implement CreatePayment via `/api/v1/account/{id}/transactions`, add webhook, fix the misleading comment.
- **Effort**: M.

### treasury/moderntreasury — `treasury/pkg/provider/moderntreasury/moderntreasury.go`
- **Coverage**: Accounts, payment_orders, counterparties (basic). GetBalance unsupported (`moderntreasury.go:164`). No expected_payments, no ledger_transactions, no virtual_accounts — and Capabilities advertises `ledger`, `virtual_accounts` (`moderntreasury.go:339`).
- **Auth**: BasicAuth(OrgID, APIKey) (`moderntreasury.go:49`). Correct per MT docs.
- **Pagination**: `ListAccounts` / `ListPayments` / `ListCounterparties` ignore `per_page` cursor (`moderntreasury.go:134-162,240-270,313-332`). MT paginates 25/page.
- **Webhook**: None. MT signs webhooks with HMAC — unhandled.
- **Errors**: Raw body (`moderntreasury.go:63`); doesn't parse MT's `errors[].code` / `request_id` envelope.
- **Idempotency**: No `Idempotency-Key`. MT requires it on POST.
- **Tests**: None.
- **Capabilities**: Overpromises ledger + virtual_accounts.
- **Registration**: `MODERN_TREASURY_ORG_ID` (`cmd/treasuryd/main.go:59`).
- **Doc.go**: Missing.
- **Recommended action**: Add idempotency, parsed errors, pagination, webhook, virtual_accounts + ledger endpoints OR trim Capabilities.
- **Effort**: M.

### treasury/mpesa — `treasury/pkg/provider/mpesa/{mpesa,webhook,doc}.go`
- **Coverage**: OAuth, AccountBalance, B2C, STK Push, TransactionStatus, Reversal (privileged surface), ParseCallback. Counterparties / FX / CreateAccount / CancelPayment / ListPayments all `ErrNotSupported` with comments explaining why (`mpesa.go:498-560`). Defensible — matches Daraja's model.
- **Auth**: OAuth client_credentials with token cache + 60-second refresh window (`mpesa.go:67-110`). RSA-OAEP of initiator password under Daraja cert for SecurityCredential (`mpesa.go:189-199`). Correct per Daraja spec.
- **Pagination**: N/A — Daraja has no list endpoint.
- **Webhook**: `webhook.go:28` `ParseCallback` discriminates `Body.stkCallback` vs `Result` envelopes; extracts amount, msisdn, result code. **Signature verification: none.** Daraja does not sign callbacks but does require IP-allowlisting Safaricom egress IPs — that enforcement belongs in the HTTP edge, not the parser. Acceptable, but should be documented in `doc.go`.
- **Errors**: `mpesa API error %d: %s` (`mpesa.go:145`); B2C / STK rejection codes additionally parsed and surfaced (`mpesa.go:355,424`). Daraja error envelope is exercised by `TestDarajaErrorEnvelope` (`mpesa_test.go:329`).
- **Idempotency**: `OriginatorConversationID` is sourced from `req.Reference` (`mpesa.go:330`) — caller-supplied, not auto-generated. Daraja dedupes on this. STK Push uses Daraja-generated `CheckoutRequestID` — not idempotent.
- **Tests**: `mpesa_test.go` — 583 lines, hermetic httptest, covers OAuth caching+refresh, msisdn normalization (positive + negative), security credential roundtrip, B2C (with non-integer KES rejection, non-KES rejection), STK Push (paybill + till variants, password derivation), GetPayment, Daraja error envelope, unsupported surface, capabilities. Best test coverage of any adapter in the audit.
- **Capabilities**: Accurate (`mpesa.go:564`).
- **Registration**: `MPESA_CONSUMER_KEY` (`cmd/treasuryd/main.go:152`).
- **Doc.go**: Present (`mpesa/doc.go:1`) with Daraja link.
- **Recommended action**: Ship-ready. Optional improvements: caller-side `OriginatorConversationID` deterministic generation when caller doesn't supply, document IP-allowlist requirement in doc.go.
- **Effort**: S.

### treasury/plaid — `treasury/pkg/provider/plaid/plaid.go`
- **Coverage**: 12 of 13 interface methods return `ErrNotSupported` (`plaid.go:84-141`). Only `doRequest` (which posts client_id+secret in body — correct Plaid shape) is real. No `/link/token/create`, no `/item/public_token/exchange`, no `/accounts/balance/get`, no `/auth/get`, no `/identity/get`, no `/transactions/sync`. Plaid is the bank-linking + identity + auth + balance API for the SF Private Bank integration's KYC and account-verification path; an empty adapter blocks the entire onboarding flow.
- **Auth**: Body-embedded client_id/secret (`plaid.go:42-53`). Correct shape, never exercised.
- **Pagination**: N/A.
- **Webhook**: None. Plaid sends signed JWT webhooks (`PLAID-VERIFICATION` header, JWKS-resolved key).
- **Errors**: Raw body (`plaid.go:77`).
- **Idempotency**: N/A.
- **Tests**: None.
- **Capabilities**: Lies — advertises `bank_linking, identity, transactions, balance, kyc, auth` and implements none (`plaid.go:143`).
- **Registration**: `PLAID_CLIENT_ID` (`cmd/treasuryd/main.go:49`).
- **Doc.go**: Missing.
- **Recommended action**: Rewrite from scratch. This adapter is a stub and the Capabilities block actively misleads any routing layer. Priority for the SF Private Bank onboarding lane.
- **Effort**: L.

### treasury/square — `treasury/pkg/provider/square/square.go`
- **Coverage**: ListAccounts (bank), card payment create/get/list/cancel. CreateAccount / GetAccount / GetBalance / CreateCounterparty / FX / counterparties all unsupported (`square.go:71-78,119,237-252`).
- **Auth**: Bearer + `Square-Version: 2024-12-18` (`square.go:51-53`). Correct.
- **Pagination**: `ListPayments` uses `/v2/payments/search` with empty body — no cursor (`square.go:196-230`). `ListAccounts` no cursor either (`square.go:79-117`).
- **Webhook**: None. Square signs webhooks with HMAC-SHA256 over body — unhandled.
- **Errors**: Raw body (`square.go:66`). Square returns rich `errors[].category/code/detail/field` — unparsed.
- **Idempotency**: ✅ Only adapter that does it: `idempotency_key: lux_<unix-nano>` on CreatePayment (`square.go:130`). But the key is time-based not request-content-based, so a retry of the same logical request gets a new key — defeats the purpose. Should be caller-supplied.
- **Tests**: None.
- **Capabilities**: Populated, mostly accurate (`square.go:254`); claims `ach` though no ACH route is implemented.
- **Registration**: `SQUARE_ACCESS_TOKEN` (`cmd/treasuryd/main.go:134`).
- **Doc.go**: Missing.
- **Recommended action**: Take idempotency key from request, add webhook + parsed errors + ACH (Square has it via `/v2/payments` with `source_id` from bank-account flow) + tests.
- **Effort**: M.

### treasury/unit — `treasury/pkg/provider/unit/unit.go`
- **Coverage**: deposit accounts + balance + achPayment create/get/list. No wire (despite Capabilities), no book, no card, no cancel, no counterparty (Unit has `counterparty` resource).
- **Auth**: Bearer (`unit.go:53`) + correct `application/vnd.api+json` content type (`unit.go:54`).
- **Pagination**: `ListAccounts` / `ListPayments` ignore `page[limit]` / `page[offset]` (`unit.go:158-191,279-313`).
- **Webhook**: None. Unit signs webhooks with `X-Unit-Signature` — unhandled.
- **Errors**: Raw body (`unit.go:67`); ignores Unit's JSON:API `errors[].code/title/detail/source` envelope.
- **Idempotency**: No `Idempotency-Key`. Unit supports it.
- **Tests**: None.
- **Capabilities**: Advertises `wire, book, card, lending` though none implemented (`unit.go:335`).
- **Registration**: `UNIT_TOKEN` (`cmd/treasuryd/main.go:116`).
- **Doc.go**: Missing (comment on package line `unit.go:16`).
- **Recommended action**: Add wire/book/card routes, counterparty, webhook, parsed errors, idempotency, tests.
- **Effort**: M.

### treasury/wise — `treasury/pkg/provider/wise/wise.go`
- **Coverage**: balances → accounts, quotes + transfers + cancel, counterparties (recipient accounts), FX quote + conversion (synthetic — see below). Decent breadth.
- **Auth**: Bearer (`wise.go:52`). Wise also requires SCA (strong customer auth) — signed-with-private-key headers on certain endpoints (transfer fund, balance transfer); not implemented. Means create-transfer will work but `POST /v3/profiles/{id}/transfers/{id}/payments` (the fund step) will fail in prod without SCA signing.
- **Pagination**: `ListPayments` no `limit` / `offset` (`wise.go:247-275`). Wise paginates.
- **Webhook**: None. Wise signs webhooks (`X-Signature-SHA256` over body, RSA public key from `/v1/webhook-public-keys` endpoint) — unhandled.
- **Errors**: Raw body (`wise.go:66`). Wise returns `errors[].code/message/path` envelope — unparsed.
- **Idempotency**: `CustomerTransactionId` is the recommended idempotency token on Wise transfers; not set (`wise.go:184`).
- **CreateFXConversion bug**: returns a quote disguised as a conversion with `Status: "quoted"` (`wise.go:321-338`) — never actually executes the FX. Misleading; should at minimum return `ErrNotSupported`.
- **Tests**: None.
- **Capabilities**: Populated, ambitious currency list (`wise.go:404`).
- **Registration**: `WISE_API_TOKEN` (`cmd/treasuryd/main.go:87`).
- **Doc.go**: Missing.
- **Recommended action**: Add SCA signing (deal-breaker for funding transfers in prod), fix `CreateFXConversion` to actually execute or return ErrNotSupported, add webhook + idempotency + parsed errors + pagination + tests.
- **Effort**: L.

### forex/circle — `forex/pkg/provider/circle/circle.go`
- **Coverage**: All 6 FXProvider methods. `GetFXQuote` and `GetFXRate` are hardcoded 1.0 for USD/USDC and EUR/EURC (correct for stablecoin peg). `CreateFXOrder` calls Circle wallet deposit / payout endpoints. `CloseFXPosition` returns explanatory error.
- **Auth**: Bearer (`circle.go:55`). Correct. Note: Circle's newer Web3 Services API requires PEM-key signing for some endpoints; Treasury / Mint API uses bearer.
- **Pagination**: `GetFXPositions` reads `/v1/businessAccount/balances` — no list pagination needed (single endpoint).
- **Webhook**: None. Circle sends signed notifications (`X-Circle-Signature` ECDSA) for payouts/transfers — unhandled.
- **Errors**: `circle error %d: %s` (`circle.go:64`). Doesn't parse Circle's `code` / `message` envelope.
- **Idempotency**: ✅ `idempotencyKey: lux_<unix-nano>` on CreateFXOrder (`circle.go:114`). Same critique as Square — not request-content-derived, so retries get new keys.
- **Tests**: `circle_test.go` — 83 lines, tests `GetFXPairs`, `GetFXQuote` (USD/USDC + unsupported), `GetFXRate`, `CloseFXPosition` error. Does not exercise `CreateFXOrder` or `GetFXPositions` against an httptest server.
- **Capabilities**: N/A (FXProvider has no Capabilities method).
- **Registration**: `CIRCLE_API_KEY` (`envconfig/envconfig.go:42`).
- **Doc.go**: Comment on package line (`circle.go:1-4`) — counts.
- **Recommended action**: Add httptest coverage for CreateFXOrder + GetFXPositions, add webhook handler (it implements forex/provider.WebhookHandler interface), parsed errors, deterministic idempotency keys.
- **Effort**: S.

### forex/currencycloud — `forex/pkg/provider/currencycloud/currencycloud.go`
- **Coverage**: All 6 FXProvider methods + `ParseWebhook` for `payment` and `conversion` event types (`currencycloud.go:244`). Most complete adapter in the audit.
- **Auth**: Form login → bearer, 25-min cache (`currencycloud.go:51-79`). **Bug**: token cache check is not mutex-protected (`currencycloud.go:51-78`) — race under concurrent first-request load.
- **Pagination**: `GetFXPositions` reads balances/find with no cursor.
- **Webhook**: `ParseWebhook` (`currencycloud.go:244`) — recognises `payment` and `conversion`, maps status into shared vocab via `mapCCStatus` (`currencycloud.go:306`). **Signature verification: none.** CurrencyCloud supports HMAC-SHA256 signing on webhook payloads via `X-CC-Signature`; the parser ignores headers (the `http.Header` param is unused — `currencycloud.go:244`). The forexd outer handler (`cmd/forexd/main.go:217`) also doesn't verify provider-side signature — only forwards to bankd with its own outbound HMAC. **This means any unauthenticated POST to `/v1/currencycloud/webhooks/payment` will be accepted, normalised, and forwarded to bankd as if it were real.** Production-blocking.
- **Errors**: `currencycloud error %d: %s` (`currencycloud.go:107`). Auth errors parsed separately.
- **Idempotency**: No `unique_request_id` on `/v2/conversions/create`.
- **Tests**: `currencycloud_test.go` (49 lines, pairs + name + close-not-supported) + `webhook_test.go` (93 lines, payment + conversion + missing-id + unknown-event + invalid-json). Both hermetic, good coverage of the webhook parser.
- **Capabilities**: N/A.
- **Registration**: `CURRENCYCLOUD_LOGIN_ID` (`envconfig/envconfig.go:21`).
- **Doc.go**: Comment on package line.
- **Recommended action**: **Add webhook signature verification** (this is the biggest single security gap in either repo). Fix token-cache race with sync.Mutex. Add idempotency + pagination + tests against httptest.
- **Effort**: S.

### forex/lmax — `forex/pkg/provider/lmax/lmax.go`
- **Coverage**: All 6 FXProvider methods. CreateFXOrder routes LIMIT/MARKET; CloseFXPosition uses dedicated endpoint.
- **Auth**: Optional Bearer + optional JSESSIONID cookie (`lmax.go:60-65`). **Missing**: actual session login. LMAX REST requires POST to `/api/login` with username/password to obtain `JSESSIONID`; the field `p.sessionID` is never populated — it will always send empty cookie and rely on bearer (which LMAX REST doesn't accept by default). Provider will not authenticate in prod.
- **Pagination**: GetFXPositions returns full list — N/A.
- **Webhook**: None. LMAX streams trade updates over FIX / WebSocket, not REST callbacks — webhook genuinely N/A for the REST surface.
- **Errors**: Raw body (`lmax.go:75`).
- **Idempotency**: LMAX uses `instructionId` for order dedup; not set.
- **Tests**: `lmax_test.go` — 41 lines, only pairs + name. No httptest, no auth/order coverage.
- **Capabilities**: N/A.
- **Registration**: `LMAX_API_KEY` (`envconfig/envconfig.go:31`).
- **Doc.go**: Comment on package line.
- **Recommended action**: Implement session login (`POST /api/login` → capture `JSESSIONID` from `Set-Cookie`); add `instructionId` for orders; expand tests against httptest. Genuinely WebSocket-based market data is out of scope for this REST adapter.
- **Effort**: M.

### forex/openpay — `forex/pkg/provider/openpay/openpay.go`
- **Coverage**: All 6 methods return `"not yet implemented"` (`openpay.go:29-50`). Zero working code.
- **Auth**: Field stored, never used.
- **Pagination/Webhook/Errors/Idempotency**: N/A.
- **Tests**: None.
- **Capabilities**: N/A.
- **Registration**: `OPENPAY_API_KEY` (`envconfig/envconfig.go:51`). **Will register a stub provider that errors on every call.** Worse than not registering.
- **Doc.go**: Package comment (`openpay.go:1-3`).
- **Recommended action**: Rewrite from scratch. Until then, remove from envconfig so it doesn't get registered as a working FX provider that always errors.
- **Effort**: L.

## Prioritization

### Production-ready today (no work)
- **treasury/mpesa** — tested, OAuth+RSA, callback parser, doc.go. Ship.

### One-day fix to ship-ready
- **forex/currencycloud** — add webhook HMAC verification + token-cache mutex. Webhook is the only blocker; rest is polish.
- **forex/circle** — add httptest coverage for order/positions + webhook handler + parsed errors.
- **treasury/currencycloud** — add pagination + parsed errors + `unique_request_id` + lift webhook handler from forex copy.

### Multi-day rewrite needed
- **treasury/lithic** — `/simulate/authorize` is sandbox-only; prod CreatePayment will fail. Needs real card-issuing + external_payments path.
- **treasury/wise** — SCA signing is a deal-breaker for funding transfers; CreateFXConversion is a lie.
- **treasury/plaid** — stub blocking SF Private Bank KYC/auth/balance lane.
- **treasury/marqeta** — stub blocking card-issuing lane.
- **forex/openpay** — six "not yet implemented" methods blocking the EU SEPA/FPS payment lane.

### Likely thin stubs — verify before relying on
- **treasury/bridge** — no idempotency, no tests, no webhook; assume not battle-tested.
- **treasury/mercury** — comment says "dashboard only" but the recipient/payment API exists; under-implemented.
- **treasury/column** — Capabilities claims rtp/fednow/check that the code doesn't route.
- **treasury/unit** — Capabilities claims wire/book/card/lending that the code doesn't route.
- **treasury/moderntreasury** — no idempotency on Modern Treasury POSTs is a real correctness bug (their dedup window is your protection); Capabilities advertises ledger + virtual_accounts not implemented.
- **treasury/increase** — same idempotency + webhook gap; swallows unmarshal errors.
- **treasury/square** — idempotency is wall-clock-derived, not request-content-derived — defeats the purpose.
- **forex/lmax** — REST session login not implemented; will not authenticate in prod.

## Recommended batch plan

Three batches of ~3 adapters each, prioritised by the SF Private Bank
(OG-SP-2026-SFPB) integration's unblocking needs. SF Private Bank front-office
needs: (1) US bank rails for ACH/wire/RTP/FedNow + chartered banking; (2) IBAN
issuance + multi-currency for cross-border; (3) card issuance for SF Bank cards;
(4) PSP card acceptance for the consumer flow; (5) FX for cross-border treasury.

**Batch 1 — "the things SF Private Bank can actually run on" (1 week).**
Unblocks: US bank rails (ACH/wire/RTP/check), institutional FX, real
webhook safety on the FX path.
- treasury/column — finish RTP/FedNow/check routes + webhook + idempotency + tests. The Fed-member adapter is the cleanest US-rails substrate.
- treasury/increase — finish RTP/check + counterparties + idempotency + webhook signature verification + tests.
- forex/currencycloud — webhook HMAC verification + token-cache mutex + pagination. (Single largest *security* delta in the audit lives here.)

**Batch 2 — "the things SF Private Bank's onboarding depends on" (1 week).**
Unblocks: KYC/auth/identity, IBAN + cross-border, multi-currency.
- treasury/plaid — full rewrite: Link token, public-token exchange, /accounts/balance, /auth/get, /identity/get, /transactions/sync, webhook (JWT-signed). Needed for any SF Private Bank consumer onboarding.
- treasury/wise — SCA signing for funding step + real CreateFXConversion + webhook (RSA-public-key-rotation) + idempotency. The IBAN/SWIFT cross-border path leans on this.
- treasury/currencycloud — pagination + parsed errors + unique_request_id + lift webhook from forex copy. The 35-currency FX path for SF Private Bank cross-border.

**Batch 3 — "the things that issue and accept cards" (1 week).**
Unblocks: SF Private Bank card issuance + PSP card acceptance.
- treasury/lithic — rewrite CreatePayment off `/simulate/*` onto real card / external_payments flow + webhook + tests.
- treasury/marqeta — full rewrite of users, card-products, cards, GPA orders, transactions + webhook + tests. (Or: remove from registry until staffed.)
- treasury/square — caller-supplied idempotency + webhook (HMAC-SHA256) + parsed errors + real ACH route + tests. Card acceptance for SF Bank consumer side.

Adapters not in any batch (defer): **forex/openpay** (rewrite when EU SEPA
lane lights up — not on the SF Private Bank critical path), **treasury/mercury**
(neobank, not on the SF Private Bank front-office critical path),
**treasury/unit** (BaaS alternative to Column — pick one),
**treasury/bridge** (stablecoin orchestration — separate Q3 workstream),
**treasury/moderntreasury** (orchestration layer — overlap with what
treasuryd itself is becoming), **treasury/mpesa** (already done),
**forex/circle** (small fix, slot anywhere), **forex/lmax** (institutional
spot FX — needed only if SF Private Bank wants a CLOB venue, otherwise
CurrencyCloud covers spot).
