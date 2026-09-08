<?php

declare(strict_types=1);

namespace Paybridge\Exception;

/**
 * Thrown for every non-2xx response from the Paybridge merchant API.
 *
 * Per the frozen API contract, every non-2xx body is `{"error": "message"}`
 * with the HTTP status code as the only machine-readable signal — there is
 * no error-code field. Callers should branch on {@see self::$statusCode}:
 *
 *   - 400: malformed request body / missing required field or header.
 *   - 401: missing or invalid API key.
 *   - 422: well-formed request rejected by business logic (e.g. no fee
 *          rule configured, insufficient balance, unknown provider).
 *   - 429: rate limit (per-IP/per-merchant) or fraud velocity limit on
 *          POST /v1/charges. No Retry-After header exists today — retry
 *          with backoff.
 *   - 500: internal error.
 */
final class PaybridgeApiException extends PaybridgeException
{
    public function __construct(
        public readonly int $statusCode,
        string $message,
    ) {
        parent::__construct($message);
    }
}
