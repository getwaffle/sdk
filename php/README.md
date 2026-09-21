# waffle/sdk (PHP)

PHP SDK for the Waffle merchant API (`:8080` by default). Requires
PHP 8.1+ and the `curl`/`json` extensions (both bundled with virtually
every PHP install). No heavy HTTP framework dependency — requests go
through PHP's built-in curl extension via a small injectable `Transport`
interface (useful for tests).

## Install

```bash
composer require waffle/sdk
```

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
    // Defaults to http://localhost:8080 (the local docker-compose dev
    // stack). No public production hostname is defined in the API
    // contract yet — pass your deployment's URL once one exists.
    baseUrl: 'http://localhost:8080',
);
```

## Errors

Every non-2xx response throws `Waffle\Exception\WaffleApiException`
(extending `Waffle\Exception\WaffleException`, so callers can catch
broadly or narrowly), carrying the HTTP `statusCode` and the server's
`error` message — there is no machine-readable error code, status is the
only signal:

```php
use Waffle\Exception\WaffleApiException;

try {
    $client->createCharge($params, IdempotencyKey::generate());
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

`POST /v1/charges`. There is no `provider` field on the request or
response — the server always auto-routes to the merchant's
highest-priority connected PSP (`xendit > doku > gdc > sandbox`); which
PSPs are connected, and their priority order, is exclusively an
admin-controlled decision, never something a merchant names or is told.
If the merchant has zero connected PSPs this is a `422`.

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
        expiresInMinutes: 60,
        metadata: ['orderId' => '42'],
    ),
    IdempotencyKey::generate(),
);

// $charge->id, $charge->status (ChargeStatus::Pending|Paid|Failed|Expired),
// $charge->checkoutUrl, $charge->grossAmount, $charge->feeAmount, $charge->netAmount, ...
```

### `calculateFee(CalculateFeeParams $params): FeeQuote`

`POST /v1/fees/calculate`. Preview-only — no charge, no ledger write, no
provider call, no idempotency key needed. There is no `provider` field
on the request — the quote resolves against the same auto-routed
provider `createCharge` would actually use, so a previewed fee always
matches what a real charge would be billed.

```php
use Waffle\Dto\CalculateFeeParams;

$quote = $client->calculateFee(new CalculateFeeParams(
    amount: 100000,
    currency: 'IDR',
));
// $quote->grossAmount, $quote->feeAmount, $quote->netAmount
```

### `registerBankAccount(RegisterBankAccountParams $params): BankAccount`

`POST /v1/bank-accounts`. All three fields are required.

```php
use Waffle\Dto\RegisterBankAccountParams;

$account = $client->registerBankAccount(new RegisterBankAccountParams(
    bankCode: 'BCA',
    accountNumber: '1234567890',
    accountHolderName: 'Budi Santoso',
));
// $account->id
```

### `createPayout(CreatePayoutParams $params, string $idempotencyKey): Payout`

`POST /v1/payouts`. There is no `provider` field on the request or
response — like `createCharge`, this auto-routes to the merchant's
highest-priority connected PSP now (payouts used to require naming one
explicitly; that asymmetry with charges is gone). A `422` with message
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
// $payout->status (PayoutStatus::Pending|Processing|Completed|Failed)
// $payout->failureReason (non-null when status is Failed)
```

### `getBalance(?string $currency = null): Balance`

`GET /v1/balance`. `$currency` is optional; when `null` the query param
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
    public function send(string $method, string $url, array $headers, ?string $body): TransportResponse
    {
        // ...
    }
}

$client = new Client('sk_live_...', transport: new MyTransport());
```

## Development

```bash
composer install
composer test        # runs vendor/bin/phpunit
php -l src/**/*.php   # lint
```
