<?php

declare(strict_types=1);

namespace Waffle;

use Waffle\Dto\Balance;
use Waffle\Dto\Bank;
use Waffle\Dto\BankAccount;
use Waffle\Dto\CalculateFeeParams;
use Waffle\Dto\Charge;
use Waffle\Dto\ChargeList;
use Waffle\Dto\CreateChargeParams;
use Waffle\Dto\CreatePayoutParams;
use Waffle\Dto\FeeQuote;
use Waffle\Dto\Payout;
use Waffle\Dto\PayoutList;
use Waffle\Dto\WhoAmI;
use Waffle\Enum\ChargeStatus;
use Waffle\Enum\PayoutStatus;
use Waffle\Exception\WaffleApiException;
use Waffle\Exception\WafflePermissionException;
use Waffle\Http\CurlTransport;
use Waffle\Http\Transport;
use Waffle\Http\TransportResponse;

/**
 * Client for the Waffle merchant API.
 *
 * The wire format is snake_case JSON; this SDK exposes camelCase
 * properties on every DTO and converts to/from snake_case internally —
 * you never write `gross_amount` or `bank_account_id` yourself. Every
 * money amount is an `int` in the currency's minor unit — never a float.
 *
 * Every key carries scopes (`charges:read`, `charges:write`,
 * `payouts:read`, `payouts:write`, `balance:read`); a `*:write` scope
 * does NOT imply the matching `*:read`. A route requiring a scope the
 * key lacks 403s as {@see \Waffle\Exception\WafflePermissionException}
 * (a {@see WaffleApiException} subclass) — see each method's doc comment
 * for the scope it needs, or call {@see self::whoAmI()} (no scope
 * required) to discover a key's scopes up front. Registering or changing
 * a merchant's withdrawal bank account, and everything KYC/team/
 * settings/branding, is dashboard-session-only — never an API-key route,
 * see {@see self::getBankAccount()}.
 *
 * A Client is safe for concurrent/repeated use; it holds no mutable state
 * after construction.
 */
final class Client
{
    /**
     * Default merchant API base URL — Waffle's production API.
     */
    public const DEFAULT_BASE_URL = 'https://api.getwaffle.id';

    private readonly string $apiKey;
    private readonly string $baseUrl;
    private readonly Transport $transport;

    /** Allowed shape of a caller-supplied `Idempotency-Key` (prevents header injection). */
    private const IDEMPOTENCY_KEY_PATTERN = '/^[A-Za-z0-9._:-]{1,255}$/D';

    /** Maximum length of an exception message built from a server response. */
    private const MAX_ERROR_MESSAGE_LENGTH = 512;

    /**
     * @param string $apiKey sent as `Authorization: Bearer <apiKey>` on
     *   every request except GET /healthz and GET /v1/banks (neither
     *   requires auth, but the header is harmless to send there too).
     *   Must be visible ASCII only (no whitespace, CR/LF/NUL or other
     *   control characters).
     * @param string|null $baseUrl defaults to {@see self::DEFAULT_BASE_URL}.
     *   The API key is sent to this URL on every call, so it must be
     *   `https://` (plain `http://` is accepted only for `localhost`,
     *   `127.0.0.1` and `[::1]`), must have a host, and must not carry
     *   userinfo, a query string, a fragment or a path (only an empty path
     *   or a single `/` is accepted). It is normalised to
     *   `scheme://host[:port]`.
     * @param Transport|null $transport override the HTTP transport
     *   (defaults to {@see CurlTransport}); primarily useful for tests.
     * @throws \InvalidArgumentException when `$apiKey` or `$baseUrl` is invalid.
     */
    public function __construct(
        #[\SensitiveParameter] string $apiKey,
        ?string $baseUrl = null,
        ?Transport $transport = null,
    ) {
        if ($apiKey === '') {
            throw new \InvalidArgumentException('Waffle\\Client: apiKey is required');
        }
        if (preg_match('/[\x00-\x20\x7F-\xFF]/', $apiKey) === 1) {
            throw new \InvalidArgumentException(
                'Waffle\\Client: apiKey must not contain whitespace, control characters or non-ASCII bytes',
            );
        }

        $this->apiKey = $apiKey;
        $this->baseUrl = self::validateBaseUrl($baseUrl ?? self::DEFAULT_BASE_URL);
        $this->transport = $transport ?? new CurlTransport();
    }

    /**
     * Never reveal the API key in var_dump()/print_r()/debug output: only
     * the mode prefix (when recognisable) is shown.
     *
     * @return array<string,string>
     */
    public function __debugInfo(): array
    {
        $prefix = '';
        foreach (['sk_live_', 'sk_test_'] as $candidate) {
            if (str_starts_with($this->apiKey, $candidate)) {
                $prefix = $candidate;
                break;
            }
        }

        return [
            'apiKey' => $prefix . '****',
            'baseUrl' => $this->baseUrl,
        ];
    }

    /**
     * A Client holds a credential and must never be serialized (session
     * storage, caches, queues).
     *
     * @return array<string,mixed>
     */
    public function __serialize(): array
    {
        throw new \LogicException('Waffle\\Client holds an API key and cannot be serialized');
    }

    /** @param array<string,mixed> $data */
    public function __unserialize(array $data): void
    {
        throw new \LogicException('Waffle\\Client cannot be unserialized');
    }

    /**
     * POST /v1/charges. Scope: `charges:write`. There is no `provider`
     * field on the request or response: the server always auto-routes to
     * the merchant's highest-priority connected PSP — which PSPs are
     * connected, and their priority order, is exclusively an
     * admin-controlled decision, never something a merchant names or is
     * told. If the merchant has zero connected PSPs this is a 422, not a
     * silent guess.
     *
     * `$idempotencyKey` is required and sent as the `Idempotency-Key`
     * header — retrying the exact same key returns the original charge
     * rather than creating a duplicate. Use {@see IdempotencyKey::generate()}
     * or your own scheme (e.g. an internal order id); this SDK never
     * generates one silently.
     *
     * @throws WaffleApiException 400 malformed amount/currency, or this
     *   payment channel is disabled for the merchant; 422 no gateway/fee
     *   rule for the resolved provider, `vaBank` missing for
     *   `virtual_account`, or provider call failed; 429 fraud velocity
     *   limit exceeded; 503 this payment channel is currently disabled
     *   platform-wide.
     * @throws WafflePermissionException the key lacks `charges:write`.
     */
    public function createCharge(CreateChargeParams $params, string $idempotencyKey): Charge
    {
        self::assertIdempotencyKey($idempotencyKey, 'createCharge');

        $data = $this->request('POST', '/v1/charges', $params->toArray(), [
            'Idempotency-Key' => $idempotencyKey,
        ]);

        return Charge::fromArray($data);
    }

    /**
     * GET /v1/charges/{id}. Scope: `charges:read`. Same shape as
     * {@see self::createCharge()}'s response, plus the full
     * paid/settlement timeline ($paidAt/$expiresAt/$settledAt on
     * {@see Charge}).
     *
     * @throws WaffleApiException 404 `{"error":"charge not found"}` for
     *   an unknown id, a charge belonging to a different merchant, or a
     *   charge in the wrong mode for this key — all three cases return
     *   the identical message, so a probing caller learns nothing from
     *   the response.
     * @throws WafflePermissionException the key lacks `charges:read`.
     */
    public function getCharge(string $id): Charge
    {
        $data = $this->request('GET', '/v1/charges/' . rawurlencode($id));

        return Charge::fromArray($data);
    }

    /**
     * GET /v1/charges. Scope: `charges:read`. Cursor-paginated list,
     * scoped to the calling key's own merchant and mode. Page forward by
     * passing the last item's id in the returned {@see ChargeList}'s
     * `data` as the next call's `$startingAfter`. Prefer
     * {@see self::listAllCharges()} when you just want every matching
     * charge rather than paging by hand.
     *
     * @param int|null $limit default 20, max 100 — a value above 100 is
     *   silently clamped server-side, not rejected
     * @param string|null $createdGte RFC 3339 or `YYYY-MM-DD`, inclusive
     *   lower bound on `created_at`
     * @param string|null $createdLte RFC 3339 or `YYYY-MM-DD`, inclusive
     *   upper bound on `created_at`
     * @throws WaffleApiException 400 malformed `$createdGte`/`$createdLte`,
     *   or `$startingAfter` doesn't resolve to a charge this key can see.
     * @throws WafflePermissionException the key lacks `charges:read`.
     */
    public function listCharges(
        ?int $limit = null,
        ?string $startingAfter = null,
        ?ChargeStatus $status = null,
        ?string $createdGte = null,
        ?string $createdLte = null,
    ): ChargeList {
        $query = $this->buildQuery([
            'limit' => $limit,
            'starting_after' => $startingAfter,
            'status' => $status?->value,
            'created[gte]' => $createdGte,
            'created[lte]' => $createdLte,
        ]);

        $data = $this->request('GET', '/v1/charges' . $query);

        return ChargeList::fromArray($data);
    }

    /**
     * Auto-paginating convenience wrapper around {@see self::listCharges()}
     * — yields every {@see Charge} matching the filters, fetching
     * additional pages lazily as the caller iterates:
     *
     * ```php
     * foreach ($client->listAllCharges(status: ChargeStatus::Paid) as $charge) {
     *     // ...
     * }
     * ```
     *
     * @param int $pageSize page size per underlying request (max 100,
     *   clamped server-side)
     * @return \Generator<int,Charge>
     * @throws WaffleApiException see {@see self::listCharges()}.
     * @throws WafflePermissionException the key lacks `charges:read`.
     */
    public function listAllCharges(
        ?ChargeStatus $status = null,
        ?string $createdGte = null,
        ?string $createdLte = null,
        int $pageSize = 100,
    ): \Generator {
        $startingAfter = null;

        while (true) {
            $page = $this->listCharges(
                limit: $pageSize,
                startingAfter: $startingAfter,
                status: $status,
                createdGte: $createdGte,
                createdLte: $createdLte,
            );

            foreach ($page->data as $charge) {
                yield $charge;
            }

            if (!$page->hasMore || $page->data === []) {
                return;
            }

            $startingAfter = $page->data[array_key_last($page->data)]->id;
        }
    }

    /**
     * GET /v1/charges/{id}/receipt.pdf. Scope: `charges:read`. Same
     * merchant+mode 404 rule as {@see self::getCharge()}. Returns the raw
     * PDF bytes — write them to a file or stream them back to your own
     * caller as-is (`Content-Type: application/pdf`).
     *
     * @return string raw PDF bytes
     * @throws WaffleApiException 404 charge not found; 409 the charge
     *   isn't `paid` yet — there is no "pending receipt."
     * @throws WafflePermissionException the key lacks `charges:read`.
     */
    public function getChargeReceipt(string $id): string
    {
        return $this->requestRaw('GET', '/v1/charges/' . rawurlencode($id) . '/receipt.pdf');
    }

    /**
     * POST /v1/fees/calculate. Scope: `charges:read` (this is a
     * read-only preview, not a charge — the write scope is not required).
     * Preview-only: no charge, no ledger write, no provider call, no
     * `Idempotency-Key` needed. There is no `provider` field on the
     * request — the quote resolves against the same auto-routed provider
     * {@see self::createCharge()} would actually use, so a previewed fee
     * always matches what a real charge would be billed.
     *
     * @throws WafflePermissionException the key lacks `charges:read`.
     */
    public function calculateFee(CalculateFeeParams $params): FeeQuote
    {
        $data = $this->request('POST', '/v1/fees/calculate', $params->toArray());

        return FeeQuote::fromArray($data);
    }

    /**
     * GET /v1/bank-accounts. Scope: `payouts:read`. Returns the caller's
     * current active withdrawal account for their mode, account number
     * masked to the last 4 digits.
     *
     * Registering a withdrawal destination is dashboard-session-only
     * (ledger item 48) — there is no API-key route for it, and this SDK
     * deliberately has no method for it either.
     *
     * @throws WaffleApiException 404 if the merchant has never
     *   registered one for this mode.
     * @throws WafflePermissionException the key lacks `payouts:read`.
     */
    public function getBankAccount(): BankAccount
    {
        $data = $this->request('GET', '/v1/bank-accounts');

        return BankAccount::fromArray($data);
    }

    /**
     * POST /v1/payouts. Scope: `payouts:write`. There is no `provider`
     * field on the request or response — like {@see self::createCharge()},
     * this auto-routes to the merchant's highest-priority connected PSP.
     * `$idempotencyKey` is required, same semantics as
     * {@see self::createCharge()}.
     *
     * @throws WaffleApiException 422 with message
     *   "insufficient available balance" when the merchant's withdrawable
     *   balance can't cover the payout.
     * @throws WafflePermissionException the key lacks `payouts:write`.
     */
    public function createPayout(CreatePayoutParams $params, string $idempotencyKey): Payout
    {
        self::assertIdempotencyKey($idempotencyKey, 'createPayout');

        $data = $this->request('POST', '/v1/payouts', $params->toArray(), [
            'Idempotency-Key' => $idempotencyKey,
        ]);

        return Payout::fromArray($data);
    }

    /**
     * GET /v1/payouts/{id}. Scope: `payouts:read`. Same merchant+mode
     * 404 rule as {@see self::getCharge()} —
     * `{"error":"payout not found"}` for an unknown id, another
     * merchant's payout, or the wrong mode.
     *
     * @throws WaffleApiException 404 payout not found.
     * @throws WafflePermissionException the key lacks `payouts:read`.
     */
    public function getPayout(string $id): Payout
    {
        $data = $this->request('GET', '/v1/payouts/' . rawurlencode($id));

        return Payout::fromArray($data);
    }

    /**
     * GET /v1/payouts. Scope: `payouts:read`. Same cursor pagination as
     * {@see self::listCharges()}. Prefer {@see self::listAllPayouts()}
     * when you just want every matching payout rather than paging by
     * hand.
     *
     * @param int|null $limit default 20, max 100 — clamped server-side
     * @param string|null $createdGte RFC 3339 or `YYYY-MM-DD`, inclusive
     *   lower bound on `created_at`
     * @param string|null $createdLte RFC 3339 or `YYYY-MM-DD`, inclusive
     *   upper bound on `created_at`
     * @throws WafflePermissionException the key lacks `payouts:read`.
     */
    public function listPayouts(
        ?int $limit = null,
        ?string $startingAfter = null,
        ?PayoutStatus $status = null,
        ?string $createdGte = null,
        ?string $createdLte = null,
    ): PayoutList {
        $query = $this->buildQuery([
            'limit' => $limit,
            'starting_after' => $startingAfter,
            'status' => $status?->value,
            'created[gte]' => $createdGte,
            'created[lte]' => $createdLte,
        ]);

        $data = $this->request('GET', '/v1/payouts' . $query);

        return PayoutList::fromArray($data);
    }

    /**
     * Auto-paginating convenience wrapper around {@see self::listPayouts()}
     * — yields every {@see Payout} matching the filters, fetching
     * additional pages lazily as the caller iterates.
     *
     * @param int $pageSize page size per underlying request (max 100,
     *   clamped server-side)
     * @return \Generator<int,Payout>
     * @throws WafflePermissionException the key lacks `payouts:read`.
     */
    public function listAllPayouts(
        ?PayoutStatus $status = null,
        ?string $createdGte = null,
        ?string $createdLte = null,
        int $pageSize = 100,
    ): \Generator {
        $startingAfter = null;

        while (true) {
            $page = $this->listPayouts(
                limit: $pageSize,
                startingAfter: $startingAfter,
                status: $status,
                createdGte: $createdGte,
                createdLte: $createdLte,
            );

            foreach ($page->data as $payout) {
                yield $payout;
            }

            if (!$page->hasMore || $page->data === []) {
                return;
            }

            $startingAfter = $page->data[array_key_last($page->data)]->id;
        }
    }

    /**
     * GET /v1/payouts/{id}/receipt.pdf. Scope: `payouts:read`. Same
     * merchant+mode 404 rule as {@see self::getPayout()}. Returns the raw
     * PDF bytes, same as {@see self::getChargeReceipt()}.
     *
     * @return string raw PDF bytes
     * @throws WaffleApiException 404 payout not found; 409 the payout
     *   isn't `completed` yet.
     * @throws WafflePermissionException the key lacks `payouts:read`.
     */
    public function getPayoutReceipt(string $id): string
    {
        return $this->requestRaw('GET', '/v1/payouts/' . rawurlencode($id) . '/receipt.pdf');
    }

    /**
     * GET /v1/balance. Scope: `balance:read`. This is withdrawable
     * balance — settled paid charges minus non-failed payouts. A charge
     * inside its settlement hold window does not count yet even if its
     * status is "paid".
     *
     * The server itself defaults `currency` to "IDR" when the query
     * param is omitted; passing `null` here omits the query param
     * entirely rather than second-guessing the server's default, so a
     * future change to the server default is inherited automatically.
     * Pass `"IDR"` explicitly to be unambiguous at the call site.
     *
     * @throws WafflePermissionException the key lacks `balance:read`.
     */
    public function getBalance(?string $currency = null): Balance
    {
        $query = $this->buildQuery(['currency' => $currency]);

        $data = $this->request('GET', '/v1/balance' . $query);

        return Balance::fromArray($data);
    }

    /**
     * GET /v1/whoami. No scope required — any valid API key may call it,
     * of any mode/scope set. This is how an integration discovers what a
     * key it was handed is even allowed to do, before trying a scoped
     * route and getting a {@see WafflePermissionException}.
     */
    public function whoAmI(): WhoAmI
    {
        $data = $this->request('GET', '/v1/whoami');

        return WhoAmI::fromArray($data);
    }

    /**
     * GET /v1/banks. No auth required (public reference data) — the
     * active-only bank directory backing virtual-account bank pickers,
     * ordered by {@see Bank::$sortOrder}.
     *
     * @return array<Bank>
     */
    public function listBanks(): array
    {
        $rows = $this->request('GET', '/v1/banks');

        $banks = [];
        foreach ($rows as $row) {
            /** @var array<string,mixed> $row */
            $banks[] = Bank::fromArray($row);
        }

        return $banks;
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
            throw new WaffleApiException($response->statusCode, 'unexpected non-object response body');
        }

        /** @var array<string,mixed> $decoded */
        return $decoded;
    }

    /** A GET request whose successful response body is raw bytes, not JSON (e.g. a PDF). */
    private function requestRaw(string $method, string $path): string
    {
        $response = $this->transport->send(
            $method,
            $this->baseUrl . $path,
            $this->headersFor(hasBody: false),
            null,
        );

        if (!$this->isSuccess($response->statusCode)) {
            $this->throwApiException($response);
        }

        return $response->body;
    }

    /**
     * Builds a `?k=v&...` query string, skipping any entry whose value is
     * `null`. Keys are sent rawurlencoded too (so a bracketed key like
     * `"created[gte]"` round-trips correctly) — Go's `url.Values`, which
     * the server parses query strings with, percent-decodes keys just
     * like values.
     *
     * @param array<string,string|int|null> $params
     */
    private function buildQuery(array $params): string
    {
        $parts = [];
        foreach ($params as $key => $value) {
            if ($value === null) {
                continue;
            }
            $parts[] = rawurlencode($key) . '=' . rawurlencode((string) $value);
        }

        return $parts === [] ? '' : '?' . implode('&', $parts);
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

    /**
     * @throws \InvalidArgumentException
     */
    private static function assertIdempotencyKey(string $idempotencyKey, string $method): void
    {
        if ($idempotencyKey === '') {
            throw new \InvalidArgumentException("idempotencyKey is required for {$method}()");
        }
        if (preg_match(self::IDEMPOTENCY_KEY_PATTERN, $idempotencyKey) !== 1) {
            throw new \InvalidArgumentException(
                "idempotencyKey for {$method}() must be 1-255 characters of A-Z a-z 0-9 . _ : -",
            );
        }
    }

    /**
     * Validates the base URL (the API key is sent to it on every call) and
     * returns it normalised to `scheme://host[:port]`.
     *
     * @throws \InvalidArgumentException
     */
    private static function validateBaseUrl(string $baseUrl): string
    {
        $fail = static fn (string $why): \InvalidArgumentException => new \InvalidArgumentException(
            'Waffle\\Client: invalid baseUrl: ' . $why,
        );

        if (preg_match('/[\x00-\x20\x7F-\xFF]/', $baseUrl) === 1) {
            throw $fail('must not contain whitespace, control characters or non-ASCII bytes');
        }
        if (str_contains($baseUrl, '\\')) {
            throw $fail('must not contain a backslash');
        }
        if (str_contains($baseUrl, '?')) {
            throw $fail('must not contain a query string');
        }
        if (str_contains($baseUrl, '#')) {
            throw $fail('must not contain a fragment');
        }

        $parts = parse_url($baseUrl);
        if ($parts === false) {
            throw $fail('is not a valid URL');
        }
        if (isset($parts['user']) || isset($parts['pass'])) {
            throw $fail('must not contain credentials (userinfo)');
        }

        $scheme = strtolower((string) ($parts['scheme'] ?? ''));
        $host = strtolower((string) ($parts['host'] ?? ''));
        if ($host === '') {
            throw $fail('must have a host');
        }
        if ($scheme !== 'https' && $scheme !== 'http') {
            throw $fail('must use https');
        }
        if ($scheme === 'http' && !in_array($host, ['localhost', '127.0.0.1', '[::1]'], true)) {
            throw $fail('must use https (plain http is allowed only for localhost, 127.0.0.1 and [::1])');
        }

        $path = (string) ($parts['path'] ?? '');
        if ($path !== '' && $path !== '/') {
            throw $fail('must not contain a path');
        }

        return $scheme . '://' . $host . (isset($parts['port']) ? ':' . $parts['port'] : '');
    }

    /**
     * Makes server-controlled text safe to put in an exception message:
     * invalid UTF-8 is replaced, control/format characters (incl. bidi
     * overrides, zero-width, BOM) and U+2028/2029 become spaces, and the
     * result is capped at 512 bytes on a character boundary.
     */
    private static function sanitizeMessage(string $message): string
    {
        if (function_exists('mb_convert_encoding')) {
            $message = mb_convert_encoding($message, 'UTF-8', 'UTF-8');
        } else {
            $message = (string) preg_replace('/[^\x09\x0A\x0D\x20-\x7E]+/', '?', $message);
        }
        $clean = preg_replace('/[\p{Cc}\p{Cf}\x{2028}\x{2029}]/u', ' ', $message);
        $message = $clean ?? '';

        if (strlen($message) > self::MAX_ERROR_MESSAGE_LENGTH) {
            $message = function_exists('mb_strcut')
                ? mb_strcut($message, 0, self::MAX_ERROR_MESSAGE_LENGTH, 'UTF-8')
                : substr($message, 0, self::MAX_ERROR_MESSAGE_LENGTH);
        }

        return $message;
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

        $message = self::sanitizeMessage($message);

        if ($response->statusCode === 403 && WafflePermissionException::matches($message)) {
            throw new WafflePermissionException($response->statusCode, $message);
        }

        throw new WaffleApiException($response->statusCode, $message);
    }
}
