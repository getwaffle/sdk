<?php

declare(strict_types=1);

namespace Paybridge\Dto;

/**
 * Request body for {@see \Paybridge\Client::calculateFee()}
 * (POST /v1/fees/calculate).
 *
 * There is no `provider` field — the quote resolves against the same
 * auto-routed provider {@see CreateChargeParams} would actually use, so
 * a previewed fee always matches what a real charge would be billed.
 */
final class CalculateFeeParams implements \JsonSerializable
{
    /** @param int $amount integer minor-unit amount — never a float */
    public function __construct(
        public readonly int $amount,
        public readonly string $currency,
    ) {
    }

    /** @return array<string,mixed> */
    public function toArray(): array
    {
        return [
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
