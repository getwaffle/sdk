# waffle-go

A Go client SDK for the Waffle **merchant API** (the `:8080` listener;
see `docs/api-contract.md` in the main repo for the frozen contract this
SDK implements). This is a standalone Go module — `go get
github.com/getwaffle/sdk/go` — with no dependency on the
backend monorepo module.

All money amounts are integers in the currency's minor unit (e.g. IDR
`12500` means Rp12.500) and are always represented as `int64`, never
`float64`.

## Install

```sh
go get github.com/getwaffle/sdk/go
```

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"

	waffle "github.com/getwaffle/sdk/go"
)

func main() {
	client := waffle.NewClient("sk_live_your_api_key") // default: https://api.getwaffle.id

	ctx := context.Background()
	if err := client.Healthz(ctx); err != nil {
		log.Fatalf("api unhealthy: %v", err)
	}
	fmt.Println("api is healthy")
}
```

### Configuring the client

```go
// Default base URL is https://api.getwaffle.id (production). Point at
// local dev against docker-compose.yml instead:
client := waffle.NewClient("sk_live_...", waffle.WithBaseURL("http://localhost:8080"))

// Override the underlying *http.Client (timeouts, transport, tracing, ...):
client := waffle.NewClient("sk_live_...",
	waffle.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}),
)

// Options compose:
client := waffle.NewClient("sk_live_...",
	waffle.WithBaseURL("https://api.example.com"),
	waffle.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}),
)
```

## Error handling

Every non-2xx response is surfaced as a `*waffle.APIError` (wrapped, so
`errors.As` works), carrying the HTTP status code and the `error` message
from the response body:

```go
charge, err := client.CreateCharge(ctx, params, idemKey)
if err != nil {
	var apiErr *waffle.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusUnprocessableEntity: // 422
			// business-logic rejection: no fee rule, no connected PSP,
			// insufficient balance, unknown provider, etc.
		case http.StatusTooManyRequests: // 429
			// rate limit or fraud-velocity limit — retry with backoff
		case http.StatusUnauthorized: // 401
			// bad/missing API key
		}
		log.Printf("waffle error %d: %s", apiErr.StatusCode, apiErr.Message)
	}
	return err
}
```

A `403` caused by an API key missing a required scope (see "Scopes and
presets" below) is additionally surfaced as a `*waffle.PermissionError`,
layered on top of the same `*APIError` — both `errors.As` targets
succeed on the same error:

```go
_, err := client.CreatePayout(ctx, params, idemKey)
var permErr *waffle.PermissionError
if errors.As(err, &permErr) {
	log.Printf("API key is missing the %q scope", permErr.Scope) // e.g. "payouts:write"
}
```

## Scopes and presets

API keys carry a scope grant: `charges:read`, `charges:write`,
`payouts:read`, `payouts:write`, `balance:read`. A `*:write` scope does
**not** imply the matching `*:read` scope — they're independent. Keys
are usually minted from a preset rather than a hand-picked scope list:

| Preset | Scopes |
|---|---|
| `read_only` | `charges:read`, `payouts:read`, `balance:read` |
| `accept_payments` | `charges:read`, `charges:write`, `balance:read` |
| `full` | all five |
| `custom` | whatever was explicitly granted |

Call `WhoAmI` (no scope required) to check a key's own preset/scopes at
runtime instead of assuming:

```go
who, err := client.WhoAmI(ctx)
// who.Preset == "read_only", who.Scopes == []string{"charges:read", "payouts:read", "balance:read"}
```

A `read_only` key can call `GetCharge`, `ListCharges`/`AllCharges`,
`GetPayout`, `ListPayouts`/`AllPayouts`, `GetBalance`, `GetBankAccount`,
and `WhoAmI` — but not `CreateCharge` or `CreatePayout` (those need the
`*:write` scopes, which `read_only` doesn't grant).

KYC and withdrawal-account management are **dashboard-only by design** —
there is no SDK method to register or change a bank account (see "Bank
accounts" below); this is a deliberate part of the credential model, not
a gap.

## Idempotency keys

`CreateCharge` and `CreatePayout` require an `Idempotency-Key` per the
contract, but this SDK auto-generates one (via `GenerateIdempotencyKey`)
when you pass the empty string — you don't have to call the helper
yourself unless you want to pin a specific key:

```go
key := waffle.GenerateIdempotencyKey() // crypto/rand-backed, 32 hex chars
charge, err := client.CreateCharge(ctx, params, key)
// or just:
charge, err := client.CreateCharge(ctx, params, "") // key auto-generated
```

Retrying a request with the same key returns the original resource
instead of creating a duplicate. Pass your own key (e.g. an internal
order id) when you want retry-safety *across* separate calls, not just
within one.

## Methods

### `CreateCharge` — `POST /v1/charges`

There is no `Provider` field: the server always auto-routes to the
merchant's highest-priority connected PSP (`xendit` > `doku` > `gdc` >
`sandbox`) — which PSPs are connected, and their priority order, is an
admin-controlled decision the merchant never names or is told. If the
merchant has zero connected PSPs, this is a `422`, not a silent guess.

```go
charge, err := client.CreateCharge(ctx, waffle.CreateChargeParams{
	Amount:           100000, // Rp100.000, integer minor units
	Currency:         "IDR",
	Description:      "Order #1234",
	CustomerRef:      "cust_42",
	ReturnURL:        "https://shop.example.com/return",
	ExpiresInMinutes: 60,
	Metadata:         map[string]string{"order_id": "1234"},
}, waffle.GenerateIdempotencyKey())
if err != nil {
	log.Fatal(err)
}
fmt.Println(charge.ID, charge.Status, charge.CheckoutURL)
```

Errors: `400` malformed amount/currency; `422` no gateway/fee rule for the
auto-routed provider, zero connected PSPs, or the provider call failed;
`429` fraud velocity limit exceeded; `403` API key lacks `charges:write`.

### `GetCharge` — `GET /v1/charges/{id}` (scope `charges:read`)

```go
charge, err := client.GetCharge(ctx, "chg_abc123")
```

`404` (message `"charge not found"`) if it doesn't exist or belongs to
another merchant.

### `ListCharges` / `AllCharges` — `GET /v1/charges` (scope `charges:read`)

`ListCharges` returns one page; `AllCharges` is an auto-paginating
`iter.Seq2` iterator (Go 1.23+ range-over-func) that walks every
matching charge via cursor pagination (`starting_after` = the last
page's last charge id) under the hood:

```go
page, err := client.ListCharges(ctx, waffle.ChargeListParams{
	Status: "paid",
	Limit:  20,
})
// page.Data, page.HasMore

for charge, err := range client.AllCharges(ctx, waffle.ChargeListParams{Status: "paid"}) {
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(charge.ID)
}
```

`ChargeListParams` also takes `StartingAfter`, `CreatedGTE`, and
`CreatedLTE` (RFC3339 or `YYYY-MM-DD`).

### `GetChargeReceipt` — `GET /v1/charges/{id}/receipt.pdf` (scope `charges:read`)

Returns raw PDF bytes (`[]byte`) — no parsing, just the bukti
pembayaran. `409` if the charge isn't `"paid"` yet.

```go
pdf, err := client.GetChargeReceipt(ctx, "chg_abc123")
if err != nil {
	log.Fatal(err)
}
os.WriteFile("receipt.pdf", pdf, 0o644)
```

### `CalculateFee` — `POST /v1/fees/calculate`

Preview-only: no charge, no ledger write, no provider call, no
`Idempotency-Key` needed. There is no `Provider` field here either — the
quote resolves against the same auto-routed provider `CreateCharge` would
actually use, so a previewed fee always matches what a real charge would
be billed.

```go
quote, err := client.CalculateFee(ctx, waffle.CalculateFeeParams{
	Amount:   100000,
	Currency: "IDR",
})
if err != nil {
	log.Fatal(err)
}
fmt.Println(quote.FeeAmount, quote.NetAmount)
```

### `GetBankAccount` — `GET /v1/bank-accounts` (scope `payouts:read`)

Read-only and masked (e.g. `AccountNumber` comes back as `"••••7890"`).
There is deliberately **no** bank-account-registration method — the
route this SDK once called, `POST /v1/bank-accounts`, was removed
server-side. Registering or changing a withdrawal destination is a
dashboard-only action (its own KYC/verification flow), never an API-key
call — the credential model never lets an API key redirect where
payouts go.

```go
account, err := client.GetBankAccount(ctx)
if err != nil {
	log.Fatal(err) // 404 if the merchant hasn't registered one for this mode
}
fmt.Println(account.ID, account.AccountNumber) // masked
```

### `CreatePayout` — `POST /v1/payouts`

There is no `Provider` field: like `CreateCharge`, this auto-routes to
the merchant's highest-priority connected PSP — the asymmetry where
payouts once required naming a provider explicitly is gone. Requires an
`Idempotency-Key`, same semantics as `CreateCharge`.

```go
payout, err := client.CreatePayout(ctx, waffle.CreatePayoutParams{
	BankAccountID: account.ID,
	Amount:        40000,
	Currency:      "IDR",
}, waffle.GenerateIdempotencyKey())
if err != nil {
	var apiErr *waffle.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnprocessableEntity {
		// e.g. apiErr.Message == "insufficient available balance"
	}
	log.Fatal(err)
}
fmt.Println(payout.ID, payout.Status)
```

Errors: `422` `"insufficient available balance"`; `403` API key lacks
`payouts:write`.

### `GetPayout` — `GET /v1/payouts/{id}` (scope `payouts:read`)

Unlike `GetBankAccount`, `AccountNumber` here is **unmasked** — it's
scoped to one payout the caller already knows about, not a general
directory lookup.

```go
payout, err := client.GetPayout(ctx, "po_abc123")
```

`404` (message `"payout not found"`) if it doesn't exist or belongs to
another merchant.

### `ListPayouts` / `AllPayouts` — `GET /v1/payouts` (scope `payouts:read`)

Same cursor-pagination shape as `ListCharges`/`AllCharges`:

```go
page, err := client.ListPayouts(ctx, waffle.PayoutListParams{Status: "completed"})

for payout, err := range client.AllPayouts(ctx, waffle.PayoutListParams{}) {
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(payout.ID)
}
```

### `GetPayoutReceipt` — `GET /v1/payouts/{id}/receipt.pdf` (scope `payouts:read`)

Raw PDF bytes (bukti pencairan dana). `409` if the payout isn't
`"completed"` yet.

```go
pdf, err := client.GetPayoutReceipt(ctx, "po_abc123")
```

### `GetBalance` — `GET /v1/balance`

Pass an empty string to omit the `currency` query param entirely and let
the server apply its own default (`IDR` today) — this SDK deliberately
does not hardcode/duplicate that default client-side, so a future
server-side change is inherited automatically. Pass `"IDR"` (or another
currency) explicitly to be unambiguous at the call site.

This is **withdrawable** balance: settled paid charges minus non-failed
payouts. A charge inside its settlement hold window does not count yet
even if its status is `"paid"`.

```go
balance, err := client.GetBalance(ctx, "IDR") // or "" for server default
if err != nil {
	log.Fatal(err)
}
fmt.Println(balance.Currency, balance.Amount)
```

### `WhoAmI` — `GET /v1/whoami`

No scope required — any valid API key, any preset, can call it. See
"Scopes and presets" above.

```go
who, err := client.WhoAmI(ctx)
if err != nil {
	log.Fatal(err)
}
fmt.Println(who.MerchantID, who.Mode, who.Preset, who.Scopes)
```

### `Healthz` — `GET /healthz`

No auth required. Returns `nil` on a 2xx response (the endpoint returns
plain text `"ok"`, not JSON), or a wrapped `*waffle.APIError` otherwise.

```go
if err := client.Healthz(ctx); err != nil {
	log.Fatal(err)
}
```

## Testing

```sh
go build ./...
go vet ./...
go test ./...
```

Tests use `net/http/httptest` against a real local test server — no mock
library — asserting method/path/headers/body per call and that non-2xx
JSON error responses produce the typed `*APIError`.
