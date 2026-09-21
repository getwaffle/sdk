# @waffle/sdk

TypeScript SDK for the Waffle merchant API. Works in Node (>=18),
browsers, and edge runtimes — it uses the global `fetch`, not a
Node-only HTTP client.

## Install

```bash
npm install @waffle/sdk
```

## Field naming

The Waffle wire format is snake_case JSON. This SDK exposes
**camelCase** fields on every request and response type and converts to
and from snake_case internally — you never write `gross_amount` or
`bank_account_id` yourself. All money amounts are integers in the
currency's minor unit; never treat an amount field as a float.

## Quick start

```ts
import { WaffleClient, generateIdempotencyKey } from "@waffle/sdk";

const client = new WaffleClient({
  apiKey: process.env.WAFFLE_API_KEY!,
  // Defaults to http://localhost:8080 (the local docker-compose dev
  // stack). No public production hostname is defined in the API
  // contract yet — pass your deployment's URL once one exists.
  baseUrl: "http://localhost:8080",
});
```

## Errors

Every non-2xx response throws a `WaffleError` carrying the HTTP
`status` and the server's `error` message (there is no machine-readable
error code — status is the only signal):

```ts
import { WaffleError } from "@waffle/sdk";

try {
  await client.createCharge({ amount: 100000, currency: "IDR" }, generateIdempotencyKey());
} catch (err) {
  if (err instanceof WaffleError) {
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
import { generateIdempotencyKey } from "@waffle/sdk";

const key = generateIdempotencyKey();
```

## Methods

### `createCharge(params, idempotencyKey)`

`POST /v1/charges`. There is no `provider` field — the server always
auto-routes to the merchant's highest-priority connected PSP
(`xendit > doku > gdc > sandbox`). Which PSPs are connected, and their
priority order, is exclusively an admin-controlled decision — a merchant
never names or is told which PSP handled a charge. If the merchant has
zero connected PSPs this is a `422`.

```ts
const charge = await client.createCharge(
  {
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
provider call, no idempotency key needed. There is no `provider` field:
the quote resolves against the same auto-routed PSP a real charge would
use, so a previewed fee always matches what a real charge would be
billed.

```ts
const quote = await client.calculateFee({
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

`POST /v1/payouts`. There is no `provider` field — like `createCharge`,
this auto-routes to the merchant's highest-priority connected PSP
(payouts used to require naming one explicitly; that asymmetry with
charges is gone). A `422` with message `"insufficient available
balance"` means the merchant's withdrawable balance can't cover the
payout.

```ts
const payout = await client.createPayout(
  {
    bankAccountId: account.id,
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

### `listBanks()`

`GET /v1/banks`. Public and unauthenticated (no API key required, though
one is sent anyway — the route ignores it), active-only, ordered by
`sortOrder`. Use this to validate or prompt for a `bankCode` before
calling `registerBankAccount` instead of hardcoding a bank list.

```ts
const banks = await client.listBanks();
// banks[0].code, banks[0].name, banks[0].logoUrl (omitted if unset), banks[0].sortOrder
```

### `healthz()`

`GET /healthz`. No auth. Resolves `true` when the server returns `200
"ok"`; throws `WaffleError` otherwise.

```ts
const healthy = await client.healthz();
```

## Custom `fetch`

Pass `fetch` in the constructor options to use a custom implementation
(useful for testing or non-standard runtimes):

```ts
const client = new WaffleClient({
  apiKey: "sk_live_...",
  fetch: myFetchImplementation,
});
```
