# paybridge-go

A Go client SDK for the Paybridge **merchant API** (the `:8080` listener;
see `docs/api-contract.md` in the main repo for the frozen contract this
SDK implements). This is a standalone Go module — `go get
github.com/very-good-labs/paybridge-go` — with no dependency on the
backend monorepo module.

All money amounts are integers in the currency's minor unit (e.g. IDR
`12500` means Rp12.500) and are always represented as `int64`, never
`float64`.

## Install

```sh
go get github.com/very-good-labs/paybridge-go
```

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"

	paybridge "github.com/very-good-labs/paybridge-go"
)

func main() {
	client := paybridge.NewClient(
		"sk_live_your_api_key",
		paybridge.WithBaseURL("https://api.example.com"), // default: http://localhost:8080
	)

	ctx := context.Background()
	if err := client.Healthz(ctx); err != nil {
		log.Fatalf("api unhealthy: %v", err)
	}
	fmt.Println("api is healthy")
}
```

### Configuring the client

```go
// Default base URL is http://localhost:8080, matching local dev via
// docker-compose.yml. Override for staging/production:
client := paybridge.NewClient("sk_live_...", paybridge.WithBaseURL("https://api.example.com"))

// Override the underlying *http.Client (timeouts, transport, tracing, ...):
client := paybridge.NewClient("sk_live_...",
	paybridge.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}),
)

// Options compose:
client := paybridge.NewClient("sk_live_...",
	paybridge.WithBaseURL("https://api.example.com"),
	paybridge.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}),
)
```

## Error handling

Every non-2xx response is surfaced as a `*paybridge.APIError` (wrapped, so
`errors.As` works), carrying the HTTP status code and the `error` message
from the response body:

```go
charge, err := client.CreateCharge(ctx, params, idemKey)
if err != nil {
	var apiErr *paybridge.APIError
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
		log.Printf("paybridge error %d: %s", apiErr.StatusCode, apiErr.Message)
	}
	return err
}
```

## Idempotency keys

`CreateCharge` and `CreatePayout` require an explicit, caller-supplied
`Idempotency-Key`. This SDK never generates one silently. Use your own
scheme (e.g. an internal order id) or the provided helper:

```go
key := paybridge.GenerateIdempotencyKey() // crypto/rand-backed, 32 hex chars
```

Retrying a request with the same key returns the original resource
instead of creating a duplicate.

## Methods

### `CreateCharge` — `POST /v1/charges`

`Provider` is optional: the zero value (empty string) omits it from the
request body, letting the server auto-route to the merchant's
highest-priority connected PSP (`xendit` > `doku` > `sandbox`). The
response's `Provider` field reports which PSP was picked. If the merchant
has zero connected PSPs, this is a `422`, not a silent guess.

```go
charge, err := client.CreateCharge(ctx, paybridge.CreateChargeParams{
	// Provider: "xendit", // optional — omit to auto-route
	Amount:           100000, // Rp100.000, integer minor units
	Currency:         "IDR",
	Description:      "Order #1234",
	CustomerRef:      "cust_42",
	ReturnURL:        "https://shop.example.com/return",
	ExpiresInMinutes: 60,
	Metadata:         map[string]string{"order_id": "1234"},
}, paybridge.GenerateIdempotencyKey())
if err != nil {
	log.Fatal(err)
}
fmt.Println(charge.ID, charge.Status, charge.CheckoutURL)
```

Errors: `400` malformed amount/currency; `422` no gateway/fee rule for the
resolved provider, or provider call failed; `429` fraud velocity limit
exceeded.

### `CalculateFee` — `POST /v1/fees/calculate`

Preview-only: no charge, no ledger write, no provider call, no
`Idempotency-Key` needed. Unlike `CreateCharge`, `Provider` is **required**
here — this endpoint does not auto-route.

```go
quote, err := client.CalculateFee(ctx, paybridge.CalculateFeeParams{
	Provider: "xendit",
	Amount:   100000,
	Currency: "IDR",
})
if err != nil {
	log.Fatal(err)
}
fmt.Println(quote.FeeAmount, quote.NetAmount)
```

### `RegisterBankAccount` — `POST /v1/bank-accounts`

All three fields are required (the server returns `400` if any is empty).

```go
account, err := client.RegisterBankAccount(ctx, paybridge.RegisterBankAccountParams{
	BankCode:          "BCA",
	AccountNumber:     "1234567890",
	AccountHolderName: "Budi Santoso",
})
if err != nil {
	log.Fatal(err)
}
fmt.Println(account.ID)
```

### `CreatePayout` — `POST /v1/payouts`

`Provider` is **required** — payout auto-routing does not exist (unlike
charges). Requires an `Idempotency-Key`, same semantics as `CreateCharge`.

```go
payout, err := client.CreatePayout(ctx, paybridge.CreatePayoutParams{
	BankAccountID: account.ID,
	Provider:      "xendit",
	Amount:        40000,
	Currency:      "IDR",
}, paybridge.GenerateIdempotencyKey())
if err != nil {
	var apiErr *paybridge.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnprocessableEntity {
		// e.g. apiErr.Message == "insufficient available balance"
	}
	log.Fatal(err)
}
fmt.Println(payout.ID, payout.Status)
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

### `Healthz` — `GET /healthz`

No auth required. Returns `nil` on a 2xx response (the endpoint returns
plain text `"ok"`, not JSON), or a wrapped `*paybridge.APIError` otherwise.

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
