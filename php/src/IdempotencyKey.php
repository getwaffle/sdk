<?php

declare(strict_types=1);

namespace Waffle;

/**
 * Generates idempotency keys for {@see Client::createCharge} and
 * {@see Client::createPayout}. Never generated automatically by the SDK —
 * every method that requires one takes it as an explicit caller-supplied
 * parameter, so retry control belongs to the caller. Call
 * {@see self::generate()} yourself when you don't have your own key scheme
 * (e.g. an internal order id) to reuse.
 */
final class IdempotencyKey
{
    private function __construct()
    {
    }

    /**
     * Returns a fresh random UUIDv4 string. Uses the `uuid` PECL extension
     * when available; otherwise falls back to a manual UUIDv4 built from
     * `random_bytes()`, keeping this package dependency-free.
     */
    public static function generate(): string
    {
        if (function_exists('uuid_create')) {
            /** @phpstan-ignore-next-line function.notFound (only defined when ext-uuid is loaded) */
            return uuid_create(UUID_TYPE_RANDOM);
        }

        return self::uuidV4();
    }

    private static function uuidV4(): string
    {
        $data = random_bytes(16);
        $data[6] = chr((ord($data[6]) & 0x0f) | 0x40); // version 4
        $data[8] = chr((ord($data[8]) & 0x3f) | 0x80); // variant 10

        $hex = bin2hex($data);

        return sprintf(
            '%s-%s-%s-%s-%s',
            substr($hex, 0, 8),
            substr($hex, 8, 4),
            substr($hex, 12, 4),
            substr($hex, 16, 4),
            substr($hex, 20, 12),
        );
    }
}
