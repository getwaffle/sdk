<?php

declare(strict_types=1);

namespace Paybridge\Dto;

/**
 * Request body for {@see \Paybridge\Client::calculateFee()}
 * (POST /v1/fees/calculate).
 *
 * Unlike {@see CreateChargeParams}, `provider` is required here — this
 * endpoint quotes a specific PSP, it does not auto-route. Do not assume
 * symmetry with `createCharge`.
 */
final class CalculateFeeParams implements \JsonSerializable
{
    /** @param int $amount integer minor-unit amount — never a float */
    public function __construct(
        public readonly string $provider,
        public readonly int $amount,
        public readonly string $currency,
    ) {
    }

    /** @return array<string,mixed> */
    public function toArray(): array
    {
        return [
            'provider' => $this->provider,
            'amount' => $this->amount,
            'currency' => $this->currency,
        ];
    }

    /** @return array<string,mixed> */
    public function jsonSerialize(): array
    {
        return $this->toArray();
    }
}
