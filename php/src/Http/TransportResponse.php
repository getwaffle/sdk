<?php

declare(strict_types=1);

namespace Waffle\Http;

/** Raw HTTP response, as returned by a {@see Transport}. */
final class TransportResponse
{
    public function __construct(
        public readonly int $statusCode,
        public readonly string $body,
    ) {
    }
}
