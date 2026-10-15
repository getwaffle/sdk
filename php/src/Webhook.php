<?php

declare(strict_types=1);

namespace Waffle;

use Waffle\Dto\WebhookEvent;
use Waffle\Enum\WebhookErrorReason;
use Waffle\Exception\WebhookVerificationException;

/**
 * Verifies Waffle webhook deliveries.
 *
 * ```php
 * $event = Webhook::verify(
 *     file_get_contents('php://input'),   // the exact raw body
 *     getallheaders(),
 *     getenv('WAFFLE_WEBHOOK_SECRET'),
 * );
 * ```
 */
final class Webhook
{
    /** Default replay tolerance in seconds, applied in both directions. */
    public const DEFAULT_TOLERANCE_SECONDS = 300;

    private const HEX_SHA256 = '/^[0-9a-fA-F]{64}$/D';

    private function __construct()
    {
    }

    /**
     * Authenticates a delivery and returns the typed event.
     *
     * `$rawBody` MUST be the exact request body (read `php://input`; a
     * re-encoded body will not verify). Header names are matched
     * case-insensitively.
     *
     * Verifies `X-Waffle-Signature-V2` (`t=<unix>,v1=<hex>`, HMAC-SHA256
     * over `"<t>.<body>"`) with `hash_equals` and a timestamp tolerance in
     * both directions. Only when that header is absent and `$allowLegacy`
     * is true is the legacy `X-Waffle-Signature` (`sha256=<hex>` over the
     * body) accepted — it has NO replay protection. A present-but-invalid
     * V2 header never falls back to legacy.
     *
     * @param array<string,string|list<string>> $headers
     * @param (callable(): int)|null $now clock returning unix seconds (tests); default `time()`
     *
     * @throws WebhookVerificationException with a machine-readable `reason`
     * @throws \InvalidArgumentException when `$secret` is empty
     */
    public static function verify(
        string $rawBody,
        array $headers,
        string $secret,
        int $toleranceSeconds = self::DEFAULT_TOLERANCE_SECONDS,
        ?callable $now = null,
        bool $allowLegacy = true,
    ): WebhookEvent {
        if ($secret === '') {
            throw new \InvalidArgumentException('Webhook::verify requires a non-empty signing secret');
        }

        $v2 = self::header($headers, 'X-Waffle-Signature-V2');
        if ($v2 !== null) {
            [$t, $sigs] = self::parseV2($v2);
            $expected = hash_hmac('sha256', $t . '.' . $rawBody, $secret);
            $matched = false;
            foreach ($sigs as $sig) {
                // No early exit on match.
                if (hash_equals($expected, $sig)) {
                    $matched = true;
                }
            }
            if (!$matched) {
                throw new WebhookVerificationException(
                    WebhookErrorReason::SignatureMismatch,
                    'signature does not match payload and secret',
                );
            }
            $current = $now !== null ? $now() : time();
            $skew = abs($current - $t);
            if ($skew > $toleranceSeconds) {
                throw new WebhookVerificationException(
                    WebhookErrorReason::TimestampOutOfTolerance,
                    "timestamp is {$skew}s from now, tolerance is {$toleranceSeconds}s",
                );
            }
        } else {
            $legacy = self::header($headers, 'X-Waffle-Signature');
            if ($legacy === null || !$allowLegacy) {
                throw new WebhookVerificationException(
                    WebhookErrorReason::MissingSignature,
                    $legacy === null
                        ? 'no X-Waffle-Signature-V2 header'
                        : 'no X-Waffle-Signature-V2 header (legacy X-Waffle-Signature present but allowLegacy is false)',
                );
            }
            if (!str_starts_with($legacy, 'sha256=')
                || preg_match(self::HEX_SHA256, $hex = trim(substr($legacy, 7))) !== 1) {
                throw new WebhookVerificationException(
                    WebhookErrorReason::MalformedHeader,
                    'X-Waffle-Signature: expected sha256=<64 hex chars>',
                );
            }
            if (!hash_equals(hash_hmac('sha256', $rawBody, $secret), strtolower($hex))) {
                throw new WebhookVerificationException(
                    WebhookErrorReason::SignatureMismatch,
                    'signature does not match payload and secret',
                );
            }
        }

        try {
            $decoded = json_decode($rawBody, true, 512, JSON_THROW_ON_ERROR);
        } catch (\JsonException) {
            throw new WebhookVerificationException(WebhookErrorReason::InvalidJson, 'body is not valid JSON');
        }
        if (!is_array($decoded) || ($decoded !== [] && array_is_list($decoded))) {
            throw new WebhookVerificationException(WebhookErrorReason::InvalidJson, 'body is not a JSON object');
        }

        return WebhookEvent::fromArray($decoded, self::header($headers, 'X-Waffle-Delivery'));
    }

    /**
     * Case-insensitive lookup; a present-but-empty header returns ''.
     *
     * @param array<string,string|list<string>> $headers
     */
    private static function header(array $headers, string $name): ?string
    {
        foreach ($headers as $k => $v) {
            if (strcasecmp((string) $k, $name) === 0) {
                return trim(is_array($v) ? implode(',', $v) : (string) $v);
            }
        }

        return null;
    }

    /**
     * @return array{0:int,1:list<string>} timestamp and lowercase hex v1 values
     */
    private static function parseV2(string $value): array
    {
        $bad = static fn (string $msg): WebhookVerificationException => new WebhookVerificationException(
            WebhookErrorReason::MalformedHeader,
            'X-Waffle-Signature-V2: ' . $msg,
        );

        $t = null;
        $sigs = [];
        foreach (explode(',', $value) as $part) {
            $part = trim($part);
            $eq = strpos($part, '=');
            $k = $eq === false ? '' : trim(substr($part, 0, $eq));
            $v = $eq === false ? '' : trim(substr($part, $eq + 1));
            if ($k === '' || $v === '') {
                throw $bad('expected comma-separated key=value pairs');
            }
            if ($k === 't') {
                if ($t !== null) {
                    throw $bad('duplicate t');
                }
                if (!ctype_digit($v) || strlen($v) > 18) {
                    throw $bad('t is not a unix timestamp');
                }
                $t = (int) $v;
            } elseif ($k === 'v1') {
                if (preg_match(self::HEX_SHA256, $v) !== 1) {
                    throw $bad('v1 is not a hex SHA-256 digest');
                }
                $sigs[] = strtolower($v);
            }
            // unknown keys are ignored for forward compatibility
        }
        if ($t === null || $sigs === []) {
            throw $bad('requires t and at least one v1');
        }

        return [$t, $sigs];
    }
}
