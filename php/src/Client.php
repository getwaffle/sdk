<?php

declare(strict_types=1);

namespace Paybridge;

use Paybridge\Dto\BankAccount;
use Paybridge\Dto\Balance;
use Paybridge\Dto\CalculateFeeParams;
use Paybridge\Dto\Charge;
use Paybridge\Dto\CreateChargeParams;
use Paybridge\Dto\CreatePayoutParams;
use Paybridge\Dto\FeeQuote;
use Paybridge\Dto\Payout;
use Paybridge\Dto\RegisterBankAccountParams;
use Paybridge\Exception\PaybridgeApiException;
use Paybridge\Http\CurlTransport;
use Paybridge\Http\Transport;
use Paybridge\Http\TransportResponse;

/**
 * Client for the Paybridge merchant API (`:8080` by default).
 *
 * The wire format is snake_case JSON; this SDK exposes camelCase
 * properties on every DTO and converts to/from snake_case internally —
 * you never write `gross_amount` or `bank_account_id` yourself. Every
 * money amount is an `int` in the currency's minor unit — never a float.
 *
 * A Client is safe for concurrent/repeated use; it holds no mutable state
 * after construction.
 */
final class Client
{
    /**
     * Default merchant API base URL, matching local dev via
     * docker-compose.yml (see docs/api-contract.md). Override via the
     * constructor's `$baseUrl` argument for staging/production.
     */
    public const DEFAULT_BASE_URL = 'http://localhost:8080';

    private readonly string $apiKey;
    private readonly string $baseUrl;
    private readonly Transport $transport;

    /**
     * @param string $apiKey sent as `Authorization: Bearer <apiKey>` on
     *   every request except GET /healthz (which requires no auth, but the
     *   header is harmless to send there too).
     * @param string|null $baseUrl defaults to {@see self::DEFAULT_BASE_URL}.
     * @param Transport|null $transport override the HTTP transport
     *   (defaults to {@see CurlTransport}); primarily useful for tests.
     */
    public function __construct(string $apiKey, ?string $baseUrl = null, ?Transport $transport = null)
    {
        if ($apiKey === '') {
            throw new \InvalidArgumentException('Paybridge\\Client: apiKey is required');
        }

        $this->apiKey = $apiKey;
        $this->baseUrl = rtrim($baseUrl ?? self::DEFAULT_BASE_URL, '/');
        $this->transport = $transport ?? new CurlTransport();
    }

    /**
     * POST /v1/charges. There is no `provider` field on the request or
     * response: the server always auto-routes to the merchant's
     * highest-priority connected PSP (xendit > doku > gdc > sandbox) —
     * which PSPs are connected, and their priority order, is exclusively
     * an admin-controlled decision, never something a merchant names or
     * is told. If the merchant has zero connected PSPs this is a 422,
     * not a silent guess.
     *
     * `$idempotencyKey` is required and sent as the `Idempotency-Key`
     * header — retrying the exact same key returns the original charge
     * rather than creating a duplicate. Use {@see IdempotencyKey::generate()}
     * or your own scheme (e.g. an internal order id); this SDK never
     * generates one silently.
     *
     * @throws PaybridgeApiException 400 malformed amount/currency; 422 no
     *   gateway/fee rule for the resolved provider, or provider call
     *   failed; 429 fraud velocity limit exceeded.
     */
    public function createCharge(CreateChargeParams $params, string $idempotencyKey): Charge
    {
        if ($idempotencyKey === '') {
            throw new \InvalidArgumentException('idempotencyKey is required for createCharge()');
        }

        $data = $this->request('POST', '/v1/charges', $params->toArray(), [
            'Idempotency-Key' => $idempotencyKey,
        ]);

        return Charge::fromArray($data);
    }

    /**
     * POST /v1/fees/calculate. Preview-only: no charge, no ledger write,
     * no provider call, no `Idempotency-Key` needed. There is no
     * `provider` field on the request — the quote resolves against the
     * same auto-routed provider {@see self::createCharge()} would
     * actually use, so a previewed fee always matches what a real charge
     * would be billed.
     */
    public function calculateFee(CalculateFeeParams $params): FeeQuote
    {
        $data = $this->request('POST', '/v1/fees/calculate', $params->toArray());

        return FeeQuote::fromArray($data);
    }

    /**
     * POST /v1/bank-accounts. All three fields on `$params` are required.
     * Registering a new account disables any prior active one for the
     * caller's mode — only one active withdrawal destination per mode at
     * a time.
     */
    public function registerBankAccount(RegisterBankAccountParams $params): BankAccount
    {
        $data = $this->request('POST', '/v1/bank-accounts', $params->toArray());

        return BankAccount::fromArray($data);
    }

    /**
     * GET /v1/bank-accounts. Returns the caller's current active
     * withdrawal account for their mode.
     *
     * @throws PaybridgeApiException 404 if the merchant has never
     *   registered one for this mode.
     */
    public function getActiveBankAccount(): BankAccount
    {
        $data = $this->request('GET', '/v1/bank-accounts');

        return BankAccount::fromArray($data);
    }

    /**
     * POST /v1/payouts. There is no `provider` field on the request or
     * response — like {@see self::createCharge()}, this auto-routes to
     * the merchant's highest-priority connected PSP. `$idempotencyKey`
     * is required, same semantics as {@see self::createCharge()}.
     *
     * @throws PaybridgeApiException 422 with message
     *   "insufficient available balance" when the merchant's withdrawable
     *   balance can't cover the payout.
     */
    public function createPayout(CreatePayoutParams $params, string $idempotencyKey): Payout
    {
        if ($idempotencyKey === '') {
            throw new \InvalidArgumentException('idempotencyKey is required for createPayout()');
        }

        $data = $this->request('POST', '/v1/payouts', $params->toArray(), [
            'Idempotency-Key' => $idempotencyKey,
        ]);

        return Payout::fromArray($data);
    }

    /**
     * GET /v1/balance. This is withdrawable balance — settled paid
     * charges minus non-failed payouts. A charge inside its settlement
     * hold window does not count yet even if its status is "paid".
     *
     * The server itself defaults `currency` to "IDR" when the query
     * param is omitted; passing `null` here omits the query param
     * entirely rather than second-guessing the server's default, so a
     * future change to the server default is inherited automatically.
     * Pass `"IDR"` explicitly to be unambiguous at the call site.
     */
    public function getBalance(?string $currency = null): Balance
    {
        $path = '/v1/balance';
        if ($currency !== null) {
            $path .= '?currency=' . rawurlencode($currency);
        }

        $data = $this->request('GET', $path);

        return Balance::fromArray($data);
    }

    /**
     * GET /healthz. Requires no auth and returns plain text "ok" (not
     * JSON) on success. Returns `true` on a 2xx response whose trimmed
     * body is exactly "ok", `false` for any other 2xx body.
     */
    public function healthz(): bool
    {
        $response = $this->transport->send(
            'GET',
            $this->baseUrl . '/healthz',
            $this->headersFor(hasBody: false),
            null,
        );

        if (!$this->isSuccess($response->statusCode)) {
            $this->throwApiException($response);
        }

        return trim($response->body) === 'ok';
    }

    /**
     * @param array<string,mixed>|null $body
     * @param array<string,string> $extraHeaders
     * @return array<string,mixed>
     */
    private function request(string $method, string $path, ?array $body = null, array $extraHeaders = []): array
    {
        $headers = $this->headersFor(hasBody: $body !== null) + $extraHeaders;
        $encoded = $body !== null ? json_encode($body, JSON_THROW_ON_ERROR) : null;

        $response = $this->transport->send($method, $this->baseUrl . $path, $headers, $encoded);

        if (!$this->isSuccess($response->statusCode)) {
            $this->throwApiException($response);
        }

        $decoded = json_decode($response->body, associative: true, flags: JSON_THROW_ON_ERROR);
        if (!is_array($decoded)) {
            throw new PaybridgeApiException($response->statusCode, 'unexpected non-object response body');
        }

        /** @var array<string,mixed> $decoded */
        return $decoded;
    }

    /** @return array<string,string> */
    private function headersFor(bool $hasBody): array
    {
        $headers = ['Authorization' => 'Bearer ' . $this->apiKey];
        if ($hasBody) {
            $headers['Content-Type'] = 'application/json';
        }

        return $headers;
    }

    private function isSuccess(int $statusCode): bool
    {
        return $statusCode >= 200 && $statusCode < 300;
    }

    private function throwApiException(TransportResponse $response): never
    {
        $message = $response->body !== '' ? $response->body : "HTTP {$response->statusCode}";

        try {
            $decoded = json_decode($response->body, associative: true, flags: JSON_THROW_ON_ERROR);
            if (is_array($decoded) && isset($decoded['error']) && is_string($decoded['error'])) {
                $message = $decoded['error'];
            }
        } catch (\JsonException) {
            // Body wasn't JSON — fall back to the raw body/status above.
        }

        throw new PaybridgeApiException($response->statusCode, $message);
    }
}
