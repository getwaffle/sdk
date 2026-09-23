<?php

declare(strict_types=1);

namespace Waffle\Dto;

use Waffle\Enum\ApiKeyPreset;
use Waffle\Enum\Mode;

/**
 * Response body for GET /v1/whoami (200) — no scope required, any valid
 * API key may call it. This is how an integration discovers what a key
 * it was handed is even allowed to do, before trying a scoped route and
 * getting a `403` ({@see \Waffle\Exception\WafflePermissionException}).
 */
final class WhoAmI implements \JsonSerializable
{
    /**
     * @param array<string> $scopes the full set of scopes this key
     *   carries, e.g. `["charges:read", "charges:write", "balance:read"]`
     *   — the fixed vocabulary is `charges:read`, `charges:write`,
     *   `payouts:read`, `payouts:write`, `balance:read`
     */
    public function __construct(
        public readonly string $merchantId,
        public readonly string $businessName,
        public readonly Mode $mode,
        public readonly ApiKeyPreset $preset,
        public readonly array $scopes,
    ) {
    }

    /** @param array<string,mixed> $data */
    public static function fromArray(array $data): self
    {
        return new self(
            merchantId: (string) $data['merchant_id'],
            businessName: (string) $data['business_name'],
            mode: Mode::from((string) $data['mode']),
            preset: ApiKeyPreset::from((string) $data['preset']),
            scopes: array_map(static fn (mixed $scope): string => (string) $scope, (array) ($data['scopes'] ?? [])),
        );
    }

    /** @return array<string,mixed> */
    public function jsonSerialize(): array
    {
        return [
            'merchant_id' => $this->merchantId,
            'business_name' => $this->businessName,
            'mode' => $this->mode->value,
            'preset' => $this->preset->value,
            'scopes' => $this->scopes,
        ];
    }
}
