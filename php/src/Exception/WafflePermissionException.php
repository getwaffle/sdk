<?php

declare(strict_types=1);

namespace Waffle\Exception;

/**
 * Thrown instead of the plain {@see WaffleApiException} for a `403`
 * response whose body matches the server's frozen missing-scope shape:
 * `{"error": "this API key lacks the <scope> permission"}` (see
 * `docs/api-contract.md`'s Scopes section — the entire scope vocabulary
 * is `charges:read`, `charges:write`, `payouts:read`, `payouts:write`,
 * `balance:read`, and a `*:write` scope does NOT imply the matching
 * `*:read`).
 *
 * Lets a caller branch on the missing scope without parsing the message
 * itself, e.g.:
 *
 * ```php
 * try {
 *     $client->createPayout($params, $key);
 * } catch (WafflePermissionException $e) {
 *     // $e->scope === 'payouts:write'
 * } catch (WaffleApiException $e) {
 *     // any other non-2xx
 * }
 * ```
 */
final class WafflePermissionException extends WaffleApiException
{
    private const PATTERN = '/lacks the (\S+) permission/';

    /**
     * The scope named in the 403 body (e.g. `"payouts:write"`), or
     * `null` if the message didn't match the expected pattern — a
     * defensive fallback so a future wording change on the server
     * degrades to an unparsed scope rather than a broken exception.
     */
    public readonly ?string $scope;

    public function __construct(int $statusCode, string $message)
    {
        parent::__construct($statusCode, $message);
        $this->scope = self::parseScope($message);
    }

    /**
     * True when `$message` matches the missing-scope 403 shape this
     * exception is for. {@see \Waffle\Client} uses this to decide
     * whether to throw this class or the plain {@see WaffleApiException}
     * for a 403.
     */
    public static function matches(string $message): bool
    {
        return preg_match(self::PATTERN, $message) === 1;
    }

    private static function parseScope(string $message): ?string
    {
        if (preg_match(self::PATTERN, $message, $matches) === 1) {
            return $matches[1];
        }

        return null;
    }
}
