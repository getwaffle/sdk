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
  // Defaults to https://api.getwaffle.id (production). Pass
  // "http://localhost:8080" for local dev against docker-compose.yml
  // instead.
});
```

## Scopes and presets

API keys are scoped. A `*:write` scope does **not** imply the matching
`*:read` — a key with `charges:write` but not `charges:read` can create
charges but can't look them up.

| Scope | Grants |
| --- | --- |
| `charges:read` | `getCharge`, `listCharges`/`listChargesAutoPaging`, `getChargeReceipt`, `calculateFee` |
| `charges:write` | `createCharge` |
| `payouts:read` | `getBankAccount`, `getPayout`, `listPayouts`/`listPayoutsAutoPaging`, `getPayoutReceipt` |
| `payouts:write` | `createPayout` |
| `balance:read` | `getBalance` |

`whoami()` and `listBanks()`/`healthz()` need no scope at all.

Keys are issued from one of four presets:

| Preset | Scopes |
| --- | --- |
| `read_only` | `charges:read`, `payouts:read`, `balance:read` |
| `accept_payments` | `charges:read`, `charges:write`, `balance:read` |
| `full` | all five scopes |
| `custom` | any other combination |

Call `whoami()` once at startup to discover a key's `preset` and
`scopes` rather than hardcoding assumptions about what it can do —
useful for tools (an MCP server, an internal dashboard) built on top of
this SDK that should only offer operations the configured key actually
has:

```ts
const who = await client.whoami();
// who.merchantId, who.businessName, who.mode, who.preset, who.scopes
if (who.scopes.includes("payouts:write")) {
  // safe to offer payout creation
}
```

A `read_only`-preset key example — safe for a reporting job or a
read-only dashboard integration:

```ts
const readOnlyClient = new WaffleClient({ apiKey: process.env.WAFFLE_READ_ONLY_KEY! });

const { data: charges } = await readOnlyClient.listCharges({ status: "paid", limit: 50 });
const balance = await readOnlyClient.getBalance("IDR");
// readOnlyClient.createCharge(...) would throw WafflePermissionError — the key has no charges:write
```

KYC, bank-account registration, team/member management, and other
account settings are **dashboard-only by design** — there is intentionally
no SDK method for any of them; the merchant dashboard is the only place
to perform onboarding/KYC and manage the account.

## Errors

Every non-2xx response throws a `WaffleError` carrying the HTTP
`status` and the server's `error` message (there is no machine-readable
error code — status is the only signal). A 403 caused by a missing
scope is thrown as the more specific `WafflePermissionError` subclass
(`instanceof WaffleError` still matches), which carries the missing
`scope` parsed out of the message:

```ts
import { WaffleError, WafflePermissionError } from "@waffle/sdk";

try {
  await client.createCharge({ amount: 100000, currency: "IDR" });
} catch (err) {
  if (err instanceof WafflePermissionError) {
    console.error(`missing scope: ${err.scope}`);
  } else if (err instanceof WaffleError) {
    if (err.status === 401) {
      // bad/missing API key
    } else if (err.status === 404) {
      // resource doesn't exist / doesn't belong to this merchant
    } else if (err.status === 409) {
      // resource exists but isn't in a state that allows this (e.g. receipt not ready)
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

`createCharge` and `createPayout` take an optional `Idempotency-Key` as
their second argument. If omitted, the SDK generates one automatically.
Retrying the exact same key returns the original charge/payout instead
of creating a duplicate — pass one explicitly only if you need to
control retry semantics yourself (e.g. persisting the key before a
request so a retry after a crash reuses it). Use the exported
`generateIdempotencyKey()` helper (backed by `crypto.randomUUID()`) for
that case:

```ts
import { generateIdempotencyKey } from "@waffle/sdk";

const key = generateIdempotencyKey();
const charge = await client.createCharge({ amount: 100000, currency: "IDR" }, key);
```

## Methods

### `createCharge(params, idempotencyKey?)`

Scope: `charges:write`. `POST /v1/charges`. There is no `provider` field
— the server always auto-routes to the merchant's highest-priority
connected PSP. Which PSPs are connected is exclusively an admin-controlled
decision — a merchant never names or is told which PSP handled a charge.
If the merchant has zero connected PSPs this is a `422`.

```ts
const charge = await client.createCharge({
  amount: 100000,
  currency: "IDR",
  description: "Order #42",
  customerRef: "cust-42",
  returnUrl: "https://shop.example/return",
  expiresInMinutes: 60,
  checkoutChannelSelection: "merchant", // or "payer" to let the payer pick a channel
  metadata: { orderId: "42" },
});

// charge.id, charge.status ("pending" | "paid" | "failed" | "expired"),
// charge.checkoutUrl, charge.grossAmount, charge.feeAmount, charge.netAmount,
// charge.breakdown (null for an unpriced "payer"-selection charge), ...
```

### `getCharge(id)`

Scope: `charges:read`. `GET /v1/charges/{id}`. Throws `WaffleError` with
status 404 if the charge doesn't exist. Adds `paidAt`, `expiresAt`, and
`settledAt` (each optional) on top of the `createCharge` response shape.

### `listCharges(params?)` / `listChargesAutoPaging(params?)`

Scope: `charges:read`. `GET /v1/charges`, cursor-paginated by
`startingAfter`/`hasMore`. `listCharges` returns one page
(`{ data, hasMore }`); `listChargesAutoPaging` is an async iterator that
pages through everything automatically:

```ts
const page = await client.listCharges({ status: "paid", limit: 50 });

for await (const charge of client.listChargesAutoPaging({ status: "paid" })) {
  // one charge at a time, across as many pages as needed
}
```

### `getChargeReceipt(id)`

Scope: `charges:read`. `GET /v1/charges/{id}/receipt.pdf`. Returns raw
PDF bytes as a `Uint8Array`. Throws `WaffleError` with status 409 if the
charge isn't `"paid"` yet.

```ts
const pdfBytes = await client.getChargeReceipt(charge.id);
```

### `calculateFee(params)`

Scope: `charges:read`. `POST /v1/fees/calculate`. Preview-only — no
charge, no ledger write, no provider call, no idempotency key needed.
There is no `provider` field: the quote resolves against the same
auto-routed PSP a real charge would use, so a previewed fee always
matches what a real charge would be billed.

```ts
const quote = await client.calculateFee({
  amount: 100000,
  currency: "IDR",
  channel: "virtual_account",
  vaBank: "BCA",
});
// quote.grossAmount, quote.feeAmount, quote.netAmount
```

### `getBankAccount()`

Scope: `payouts:read`. `GET /v1/bank-accounts`. Returns the merchant's
currently registered withdrawal account for their mode, with
`accountNumber` masked. Throws `WaffleError` with status 404 if none is
registered. **There is no SDK method to register a bank account** — that
is dashboard-only.

```ts
const account = await client.getBankAccount();
// account.id, account.bankCode, account.accountNumber (masked), account.accountHolderName
```

### `createPayout(params, idempotencyKey?)`

Scope: `payouts:write`. `POST /v1/payouts`. There is no `provider` field
— like `createCharge`, this auto-routes to the merchant's
highest-priority connected PSP. A `422` with message `"insufficient
available balance"` means the merchant's withdrawable balance can't
cover the payout.

```ts
const payout = await client.createPayout({
  bankAccountId: account.id,
  amount: 40000,
  currency: "IDR",
});
// payout.status ("pending" | "held" | "processing" | "completed" | "failed")
// payout.failureReason (present when status is "failed")
```

### `getPayout(id)`

Scope: `payouts:read`. `GET /v1/payouts/{id}`. Throws `WaffleError` with
status 404 if the payout doesn't exist. Adds `bankCode`, `accountNumber`,
`accountHolderName`, `createdAt`, `completedAt` (each optional) on top of
the `createPayout` response shape.

### `listPayouts(params?)` / `listPayoutsAutoPaging(params?)`

Scope: `payouts:read`. `GET /v1/payouts`, same cursor-pagination pattern
as `listCharges`/`listChargesAutoPaging`, filterable by `status`
(`pending | held | processing | completed | failed`).

### `getPayoutReceipt(id)`

Scope: `payouts:read`. `GET /v1/payouts/{id}/receipt.pdf`. Returns raw
PDF bytes as a `Uint8Array`. Throws `WaffleError` with status 409 if the
payout isn't `"completed"` yet.

### `getBalance(currency?)`

Scope: `balance:read`. `GET /v1/balance`. `currency` is optional and
defaults server-side to `IDR`. This is **withdrawable** balance —
settled paid charges minus non-failed payouts. A charge inside its
settlement hold window does not count yet even if its status is
`"paid"`.

```ts
const balance = await client.getBalance("IDR");
// balance.currency, balance.amount
```

### `whoami()`

No scope required. `GET /v1/whoami`. See [Scopes and presets](#scopes-and-presets).

### `listBanks()`

No scope required (public, unauthenticated route). `GET /v1/banks`.
Active-only, ordered by `sortOrder`.

```ts
const banks = await client.listBanks();
// banks[0].code, banks[0].name, banks[0].logoUrl (omitted if unset), banks[0].sortOrder
```

### `healthz()`

No auth. `GET /healthz`. Resolves `true` when the server returns `200
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
