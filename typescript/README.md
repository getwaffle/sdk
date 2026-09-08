# @paybridge/sdk

TypeScript SDK for the Paybridge merchant API. Works in Node (>=18),
browsers, and edge runtimes — it uses the global `fetch`, not a
Node-only HTTP client.

## Install

```bash
npm install @paybridge/sdk
```

## Field naming

The Paybridge wire format is snake_case JSON. This SDK exposes
**camelCase** fields on every request and response type and converts to
and from snake_case internally — you never write `gross_amount` or
`bank_account_id` yourself. All money amounts are integers in the
currency's minor unit; never treat an amount field as a float.

## Quick start

```ts
import { PaybridgeClient, generateIdempotencyKey } from "@paybridge/sdk";

const client = new PaybridgeClient({
  apiKey: process.env.PAYBRIDGE_API_KEY!,
  // Defaults to http://localhost:8080 (the local docker-compose dev
  // stack). No public production hostname is defined in the API
  // contract yet — pass your deployment's URL once one exists.
  baseUrl: "http://localhost:8080",
});
```

## Errors

Every non-2xx response throws a `PaybridgeError` carrying the HTTP
`status` and the server's `error` message (there is no machine-readable
error code — status is the only signal):

```ts
import { PaybridgeError } from "@paybridge/sdk";

try {
  await client.createCharge({ amount: 100000, currency: "IDR" }, generateIdempotencyKey());
} catch (err) {
  if (err instanceof PaybridgeError) {
    if (err.status === 401) {
      // bad/missing API key
    } else if (err.status === 422) {
      // well-formed request rejected by business logic
      // (e.g. no fee rule configured, insufficient balance, unknown provider)
    } else if (err.status === 429) {
      // rate limited or fraud velocity limit hit on POST /v1/charges — retry with backoff
    }
    console.error(err.status, err.message);
  }
  throw err;
}
```

## Idempotency keys

`createCharge` and `createPayout` require an explicit `Idempotency-Key`
as their second argument — the SDK never generates one silently, since
retry control belongs to the caller. Retrying the exact same key returns
the original charge/payout instead of creating a duplicate. Use the
exported `generateIdempotencyKey()` helper (backed by
`crypto.randomUUID()`) for convenience:

```ts
import { generateIdempotencyKey } from "@paybridge/sdk";

const key = generateIdempotencyKey();
```

## Methods

### `createCharge(params, idempotencyKey)`

`POST /v1/charges`. `params.provider` is **optional** — omit it to let
the server auto-route to the merchant's highest-priority connected PSP
(`xendit > doku > sandbox`). The response reports which provider was
picked. If the merchant has zero connected PSPs this is a `422`.

```ts
const charge = await client.createCharge(
  {
    provider: "xendit", // optional — omit to auto-route
    amount: 100000,
    currency: "IDR",
    description: "Order #42",
    customerRef: "cust-42",
    returnUrl: "https://shop.example/return",
    expiresInMinutes: 60,
    metadata: { orderId: "42" },
  },
  generateIdempotencyKey(),
);

// charge.id, charge.status ("pending" | "paid" | "failed" | "expired"),
// charge.checkoutUrl, charge.grossAmount, charge.feeAmount, charge.netAmount, ...
```

### `calculateFee(params)`

`POST /v1/fees/calculate`. Preview-only — no charge, no ledger write, no
provider call, no idempotency key needed. Unlike `createCharge`,
**`provider` is required** here: this quotes a specific PSP rather than
auto-routing. Do not assume symmetry between the two endpoints.

```ts
const quote = await client.calculateFee({
  provider: "xendit",
  amount: 100000,
  currency: "IDR",
});
// quote.grossAmount, quote.feeAmount, quote.netAmount
```

### `registerBankAccount(params)`

`POST /v1/bank-accounts`. All three fields are required.

```ts
const account = await client.registerBankAccount({
  bankCode: "BCA",
  accountNumber: "1234567890",
  accountHolderName: "Budi Santoso",
});
// account.id
```

### `createPayout(params, idempotencyKey)`

`POST /v1/payouts`. **`provider` is required** — payout auto-routing
does not exist (charge auto-routing does; do not assume symmetry). A
`422` with message `"insufficient available balance"` means the
merchant's withdrawable balance can't cover the payout.

```ts
const payout = await client.createPayout(
  {
    bankAccountId: account.id,
    provider: "xendit",
    amount: 40000,
    currency: "IDR",
  },
  generateIdempotencyKey(),
);
// payout.status ("pending" | "processing" | "completed" | "failed")
// payout.failureReason (present when status is "failed")
```

### `getBalance(currency?)`

`GET /v1/balance`. `currency` is optional and defaults server-side to
`IDR`. This is **withdrawable** balance — settled paid charges minus
non-failed payouts. A charge inside its settlement hold window does not
count yet even if its status is `"paid"`.

```ts
const balance = await client.getBalance("IDR");
// balance.currency, balance.amount
```

### `healthz()`

`GET /healthz`. No auth. Resolves `true` when the server returns `200
"ok"`; throws `PaybridgeError` otherwise.

```ts
const healthy = await client.healthz();
```

## Custom `fetch`

Pass `fetch` in the constructor options to use a custom implementation
(useful for testing or non-standard runtimes):

```ts
const client = new PaybridgeClient({
  apiKey: "sk_live_...",
  fetch: myFetchImplementation,
});
```
