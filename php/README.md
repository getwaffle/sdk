# waffle/sdk (PHP)

PHP SDK for the Waffle merchant API. Requires
PHP 8.1+ and the `curl`/`json` extensions (both bundled with virtually
every PHP install). No heavy HTTP framework dependency — requests go
through PHP's built-in curl extension via a small injectable `Transport`
interface (useful for tests).

PHP 8.1 is end-of-life upstream. The package still installs on `>=8.1`,
but **PHP 8.2 or newer is recommended** — among other things, the
`#[\SensitiveParameter]` attributes this SDK uses to keep your API key
out of exception stack traces only take effect on PHP 8.2+.

## Install

```bash
composer require waffle/sdk
```

## Secure defaults

- **Base URL.** The API key is sent to the base URL on every call, so the
  `Client` constructor validates it: `https://` is required (plain
  `http://` is accepted only for `localhost`, `127.0.0.1` and `[::1]`),
  it must have a host, and it must not contain credentials
  (`user:pass@`), a query string, a fragment or a path (only `""` or
  `/` is accepted). It is normalised to `scheme://host[:port]`. Anything else throws `InvalidArgumentException`.
- **API key.** Must be visible ASCII: whitespace, CR/LF/NUL and other
  control characters are rejected. Keep it out of source control and
  load it from the environment or a secret manager.
- **No leaks via debugging.** `var_dump()`/`print_r()` of a `Client` show
  only a redacted key (`sk_test_****`), and a `Client` cannot be
  serialized (`serialize()` throws `LogicException`), so it cannot end up
  in a session or cache by accident. `$apiKey` and the transport's
  `$headers` are marked `#[\SensitiveParameter]`; on PHP 8.2+ they are
  replaced by `SensitiveParameterValue` in stack traces. If you set
  `zend.exception_ignore_args=On` in production you get the same on 8.1.
- **Transport.** The default curl transport verifies TLS (peer and host),
  never follows redirects, allows only HTTPS (HTTP only for a loopback
  base URL), keeps the 30 s timeouts, and aborts any response larger than
  8 MiB (16 MiB for receipt PDFs) with a `WaffleException`.
- **Idempotency keys** you supply must match `[A-Za-z0-9._:-]{1,255}`
  (`IdempotencyKey::generate()` output always does); anything else throws
  `InvalidArgumentException` before a request is made.
- **Error messages** built from a server response are made valid UTF-8,
  have control/format characters (bidi overrides, zero-width, BOM,
  U+2028/2029) replaced with spaces, and are capped at 512 bytes.

### Compatibility notes

- Caller-supplied idempotency keys are restricted to
  `/^[A-Za-z0-9._:-]{1,255}$/`. Order ids containing `/`, `#`, `@`, `+`,
  spaces or non-ASCII characters must be mapped or hashed first.
- A `Client` can no longer be serialized. Queued jobs or sessions that
  serialize a client will break; construct the client inside the job.
- A base URL with a path prefix is no longer accepted.

## Field naming

The Waffle wire format is snake_case JSON. This SDK exposes
**camelCase** properties on every request/response DTO (matching the
sibling TypeScript SDK) and converts to/from snake_case internally via
each DTO's `toArray()`/`fromArray()` — you never write `gross_amount` or
`bank_account_id` yourself. All money amounts are `int` in the
currency's minor unit; **never** a `float`.

## Quick start

```php
<?php

use Waffle\Client;

$client = new Client(
    apiKey: getenv('WAFFLE_API_KEY'),
    // Uses https://api.getwaffle.id (production) by default.
);
```

## Scopes

Every API key carries **scopes**, the entire vocabulary is `charges:read`,
`charges:write`, `payouts:read`, `payouts:write`, `balance:read`. A
`*:write` scope does **not** imply the matching `*:read` — a route
needing read access 403s a write-only key. Scopes are set at key
creation as either a named preset or a hand-picked custom list (dashboard
only — there is no API-key route for key management):

| Preset | Scopes |
|---|---|
| `read_only` | `charges:read`, `payouts:read`, `balance:read` |
| `accept_payments` | `charges:read`, `charges:write`, `balance:read` |
| `full` | all five |
| `custom` | whatever was hand-picked |

Which scope each method needs:

| Method | Scope |
|---|---|
| `createCharge` | `charges:write` |
| `getCharge`, `listCharges`, `listAllCharges`, `getChargeReceipt`, `calculateFee` | `charges:read` |
| `getBankAccount` | `payouts:read` |
| `createPayout` | `payouts:write` |
| `getPayout`, `listPayouts`, `listAllPayouts`, `getPayoutReceipt` | `payouts:read` |
| `getBalance` | `balance:read` |
| `whoAmI`, `listBanks`, `healthz` | none |

A `read_only` key (`charges:read`, `payouts:read`, `balance:read`) can
call every read/list/receipt/preview method above plus `getBankAccount`
— but not `createCharge` or `createPayout`, which need the matching
`*:write` scope. Call `whoAmI()` (no scope required) to discover a key's
scopes up front rather than guessing from a `403`.

```php
use Waffle\Client;
use Waffle\Exception\WafflePermissionException;

// wf_sandbox_... issued with the "read_only" preset (create it from the dashboard).
$client = new Client(getenv('WAFFLE_READ_ONLY_KEY'));

$me = $client->whoAmI();
// $me->scopes === ['charges:read', 'payouts:read', 'balance:read']

$balance = $client->getBalance('IDR');           // fine — balance:read
foreach ($client->listAllCharges() as $charge) { // fine — charges:read
    // ...
}

try {
    $client->createCharge($params, IdempotencyKey::generate());
} catch (WafflePermissionException $e) {
    // $e->scope === 'charges:write' — a read_only key can never write.
}
```

KYC, team management, settings, branding, webhook configuration, and
registering/changing a merchant's withdrawal bank account are all
**dashboard-only** — there is deliberately no API-key route, and no SDK
method, for any of them. `getBankAccount()` only reads the current
active (masked) destination.

## Errors

Every non-2xx response throws `Waffle\Exception\WaffleApiException`
(extending `Waffle\Exception\WaffleException`, so callers can catch
broadly or narrowly), carrying the HTTP `statusCode` and the server's
`error` message — there is no machine-readable error code, status is the
only signal.

A `403` caused specifically by a missing scope throws the narrower
`Waffle\Exception\WafflePermissionException` (a `WaffleApiException`
subclass), carrying the missing scope parsed out of the message:

```php
use Waffle\Exception\WaffleApiException;
use Waffle\Exception\WafflePermissionException;

try {
    $client->createCharge($params, IdempotencyKey::generate());
} catch (WafflePermissionException $e) {
    // $e->scope === 'charges:write' — this key needs that scope.
    error_log("missing scope: {$e->scope}");
    throw $e;
} catch (WaffleApiException $e) {
    match (true) {
        $e->statusCode === 401 => /* bad/missing API key */ null,
        $e->statusCode === 422 => /* well-formed request rejected by business logic
                                      (e.g. no fee rule configured, insufficient
                                      balance, unknown provider) */ null,
        $e->statusCode === 429 => /* rate limited or fraud velocity limit hit on
                                      POST /v1/charges — retry with backoff */ null,
        default => null,
    };
    error_log("{$e->statusCode}: {$e->getMessage()}");
    throw $e;
}
```

## Verifying webhooks

`Waffle\Webhook::verify()` authenticates a delivery and returns a typed
`WebhookEvent`. Pass the **raw** body (`php://input`; a re-encoded body
will not verify):

```php
use Waffle\Exception\WebhookVerificationException;
use Waffle\Webhook;

try {
    $event = Webhook::verify(
        file_get_contents('php://input'),
        getallheaders(),
        getenv('WAFFLE_WEBHOOK_SECRET'),
    );
} catch (WebhookVerificationException $e) {
    // $e->reason: WebhookErrorReason::MissingSignature | MalformedHeader
    //   | TimestampOutOfTolerance | SignatureMismatch | InvalidJson
    http_response_code(400);
    exit;
}

// Dedupe on the SIGNED event id in the body ($event->eventId): it is covered
// by the signature. $event->deliveryId (the X-Waffle-Delivery header) is NOT
// signed, so a replayer or intermediary can change it; use it for logging only.
if ($event->event === 'charge.paid') {
    fulfil($event->chargeId, $event->amount, $event->currency); // $amount is int minor units
}
http_response_code(200);
```

`X-Waffle-Signature-V2` (`t=<unix>,v1=<hex>`, HMAC-SHA256 over
`"<t>.<body>"`) is verified with `hash_equals` and a 300 s tolerance in
both directions. The server sends **both** `X-Waffle-Signature-V2` and the
legacy `X-Waffle-Signature` on every delivery, so a delivery without V2
is rejected by default (`MissingSignature`). `allowLegacy` defaults to
`false`; pass `allowLegacy: true` only if you must accept legacy-only
senders — doing so **disables replay protection**, because an attacker
can strip V2 from a captured delivery and replay the legacy-only copy
forever. A present-but-invalid V2 header never falls back to legacy.
Other named arguments:
`toleranceSeconds`, `now` (a callable returning unix seconds).
`$event->raw` holds the full decoded snake_case payload. Respond 2xx
only after the event is durably recorded; anything else is retried with
the same delivery id.

## Idempotency keys

`createCharge` and `createPayout` require an explicit `Idempotency-Key`
as their second argument — the SDK never generates one silently, since
retry control belongs to the caller. Retrying the exact same key returns
the original charge/payout instead of creating a duplicate. Use the
`IdempotencyKey::generate()` helper for convenience (backed by the
`uuid` PECL extension when available, otherwise a manual UUIDv4 built
from `random_bytes()` — no external dependency required):

```php
use Waffle\IdempotencyKey;

$key = IdempotencyKey::generate();
```

## Methods

### `createCharge(CreateChargeParams $params, string $idempotencyKey): Charge`

`POST /v1/charges` (scope `charges:write`). There is no `provider` field
on the request or response — the server always auto-routes to the
merchant's highest-priority connected PSP; which PSPs are connected, and
their priority order, is exclusively an admin-controlled decision, never
something a merchant names or is told. If the merchant has zero connected
PSPs this is a `422`.

```php
use Waffle\Dto\CreateChargeParams;
use Waffle\IdempotencyKey;

$charge = $client->createCharge(
    new CreateChargeParams(
        amount: 100000,
        currency: 'IDR',
        description: 'Order #42',
        customerRef: 'cust-42',
        returnUrl: 'https://shop.example/return',
        returnLabel: 'Back to Example Shop', // optional, max 40 chars, needs returnUrl
        expiresInMinutes: 60,
        metadata: ['orderId' => '42'],
        // channel: 'qris',                          // or 'virtual_account' + vaBank
        // checkoutChannelSelection: 'payer',         // defer the choice to the payer instead
    ),
    IdempotencyKey::generate(),
);

// $charge->id, $charge->status (ChargeStatus::Pending|Paid|Failed|Expired),
// $charge->checkoutUrl, $charge->grossAmount, $charge->feeAmount, $charge->netAmount,
// $charge->breakdown (Dto\ChargeBreakdown|null — base/fee/bearer/payer_paid/merchant_receives), ...
```

#### Redirect after payment

Pass `returnUrl` (and optionally `returnLabel`, at most 40 characters, only allowed together with `returnUrl`; otherwise the API answers `400`) to send the payer back to your site from the hosted checkout (`/pay/{id}`). Once the charge is final (paid, failed or expired) the page shows a button to that URL, labelled with your label, else "Kembali ke {merchant name}". If the charge has no return URL, the merchant's dashboard default return URL is used.

- Waffle adds two query params to the link: `waffle_charge_id` (the charge id) and `waffle_status` (`paid`, `failed` or `expired`). Params with those names are replaced; your own params are kept.
- On **live** charges only, if the page watched the charge go from pending to final, the URL is `https` on your own domain (the same registrable domain as the website in your Waffle business profile, or as your dashboard default return URL) and the page is not embedded, it counts down 5 seconds and redirects automatically (the payer can cancel). Sandbox charges only show the button.
- The redirect can arrive **before** the `charge.paid` webhook. `waffle_status` is a hint for which page to show, **never proof of payment**: confirm via the webhook or by fetching the charge (`GET /v1/charges/{id}`) before fulfilling the order.

The charge echoes `$charge->returnUrl` and `$charge->returnLabel` (null when not given).

### `getCharge(string $id): Charge`

`GET /v1/charges/{id}` (scope `charges:read`). Same shape as
`createCharge`'s response, plus `$charge->paidAt`/`$charge->expiresAt`/
`$charge->settledAt`. Throws `WaffleApiException` (404, `"charge not
found"`) for an unknown id, another merchant's charge, or the wrong mode
— all three look identical, so a probing caller learns nothing.

### `listCharges(...): ChargeList` / `listAllCharges(...): Generator`

`GET /v1/charges` (scope `charges:read`). `listCharges()` returns one
cursor-paginated page (`$page->data`, an array of `Charge`, and
`$page->hasMore`); `listAllCharges()` wraps it in a PHP generator that
pages forward automatically:

```php
use Waffle\Enum\ChargeStatus;

foreach ($client->listAllCharges(status: ChargeStatus::Paid, createdGte: '2026-01-01') as $charge) {
    // one Charge at a time, fetching more pages lazily as needed
}

// Or page by hand:
$page = $client->listCharges(limit: 20, status: ChargeStatus::Paid);
// $page->data, $page->hasMore
```

### `getChargeReceipt(string $id): string`

`GET /v1/charges/{id}/receipt.pdf` (scope `charges:read`). Returns the
raw PDF bytes (`"bukti pembayaran"`). Throws `WaffleApiException` (409)
if the charge isn't `paid` yet.

```php
file_put_contents('receipt.pdf', $client->getChargeReceipt($charge->id));
```

### `calculateFee(CalculateFeeParams $params): FeeQuote`

`POST /v1/fees/calculate` (scope `charges:read` — this is a read-only
preview, the write scope is not required). Preview-only — no charge, no
ledger write, no provider call, no idempotency key needed. There is no
`provider` field on the request — the quote resolves against the same
auto-routed provider `createCharge` would actually use, so a previewed
fee always matches what a real charge would be billed.

```php
use Waffle\Dto\CalculateFeeParams;

$quote = $client->calculateFee(new CalculateFeeParams(
    amount: 100000,
    currency: 'IDR',
    // channel: 'virtual_account', vaBank: 'BCA',  // optional, narrows the quote
));
// $quote->grossAmount, $quote->feeAmount, $quote->netAmount
```

### `getBankAccount(): BankAccount`

`GET /v1/bank-accounts` (scope `payouts:read`). Returns the caller's
current active withdrawal account for their mode, `accountNumber` masked
to the last 4 digits. Throws `WaffleApiException` (404) if the merchant
has never registered one for this mode.

**Registering or changing a withdrawal bank account is dashboard-only**
(there is no API-key route, and no method here, for it — ledger item 48:
where a merchant's money leaves to is a dashboard-session-only decision,
never delegable to a bearer credential).

```php
$account = $client->getBankAccount();
// $account->id, $account->accountNumber ('••••7890'), $account->createdAt
```

### `createPayout(CreatePayoutParams $params, string $idempotencyKey): Payout`

`POST /v1/payouts` (scope `payouts:write`). There is no `provider` field
on the request or response — like `createCharge`, this auto-routes to
the merchant's highest-priority connected PSP. A `422` with message
`"insufficient available balance"` means the merchant's withdrawable
balance can't cover the payout.

```php
use Waffle\Dto\CreatePayoutParams;
use Waffle\IdempotencyKey;

$payout = $client->createPayout(
    new CreatePayoutParams(
        bankAccountId: $account->id,
        amount: 40000,
        currency: 'IDR',
    ),
    IdempotencyKey::generate(),
);
// $payout->status (PayoutStatus::Pending|Held|Processing|Completed|Failed)
// $payout->failureReason (non-null when status is Failed)
```

### `getPayout(string $id): Payout` / `listPayouts(...): PayoutList` / `listAllPayouts(...): Generator` / `getPayoutReceipt(string $id): string`

`GET /v1/payouts/{id}`, `GET /v1/payouts`, and
`GET /v1/payouts/{id}/receipt.pdf` (all scope `payouts:read`) — the
payout counterparts to `getCharge`/`listCharges`/`listAllCharges`/
`getChargeReceipt` above, same pagination/filter/receipt shape.
`getPayout`'s response additionally carries `$payout->bankCode`/
`$payout->accountNumber` (**unmasked** — the destination detail on this
specific payout, distinct from `getBankAccount`'s masked "current active
destination" view)/`$payout->accountHolderName`/`$payout->completedAt`.
Payouts carry no fee, so there's no receipt breakdown table (unlike the
charge receipt).

### `getBalance(?string $currency = null): Balance`

`GET /v1/balance` (scope `balance:read`). `$currency` is optional; when `null` the query param
is omitted entirely and the **server** defaults it to `IDR` (this SDK
deliberately does not second-guess that default client-side, so a
future server-side change is inherited automatically — pass `'IDR'`
explicitly if you want the call site to be unambiguous regardless).
This is **withdrawable** balance — settled paid charges minus
non-failed payouts. A charge inside its settlement hold window does not
count yet even if its status is `"paid"`.

```php
$balance = $client->getBalance('IDR');
// $balance->currency, $balance->amount
```

### `whoAmI(): WhoAmI`

`GET /v1/whoami`. No scope required — any valid key may call it, of any
mode/scope set. Use this to discover what a key is even allowed to do
before trying a scoped route and getting a `WafflePermissionException`.

```php
$me = $client->whoAmI();
// $me->merchantId, $me->businessName, $me->mode (Mode::Live|Sandbox),
// $me->preset (ApiKeyPreset::ReadOnly|AcceptPayments|Full|Custom), $me->scopes (string[])
```

### `listBanks(): array`

`GET /v1/banks`. No auth required (public reference data) — the
active-only bank directory backing virtual-account bank pickers, ordered
by `sortOrder`. Returns `array<Dto\Bank>`.

```php
foreach ($client->listBanks() as $bank) {
    // $bank->code, $bank->name, $bank->logoUrl, $bank->sortOrder
}
```

### `healthz(): bool`

`GET /healthz`. No auth. Returns `true` when the server returns `200
"ok"`; throws `WaffleApiException` on a non-2xx response, `false` for
any other 2xx body.

```php
$healthy = $client->healthz();
```

## Custom transport

Pass a `Waffle\Http\Transport` implementation as the third
`Client` constructor argument to use a custom HTTP layer (useful for
tests, or to route through a PSR-18 client of your own):

```php
use Waffle\Client;
use Waffle\Http\Transport;
use Waffle\Http\TransportResponse;

final class MyTransport implements Transport
{
    public function send(
        string $method,
        string $url,
        #[\SensitiveParameter] array $headers, // carries the Authorization bearer key
        ?string $body,
    ): TransportResponse {
        // ... never log $headers; do not follow redirects (they would forward the key)
    }
}

$client = new Client('wf_live_...', transport: new MyTransport());
```

## Development

```bash
composer install
composer test        # runs vendor/bin/phpunit
php -l src/**/*.php   # lint
```
