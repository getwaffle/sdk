<?php

declare(strict_types=1);

namespace Paybridge\Dto;

/**
 * Response body for POST /v1/fees/calculate (200). Preview-only: no
 * charge, no ledger write, no provider call was made to produce this.
 */
final class FeeQuote implements \JsonSerializable
{
    /**
     * @param int $grossAmount integer minor-unit amount — never a float
     * @param int $feeAmount integer minor-unit amount — never a float
     * @param int $netAmount integer minor-unit amount — never a float
     */
    public function __construct(
        public readonly string $provider,
        public readonly int $grossAmount,
        public readonly int $feeAmount,
        public readonly int $netAmount,
        public readonly string $currency,
    ) {
    }

    /** @param array<string,mixed> $data */
    public static function fromArray(array $data): self
    {
        return new self(
            provider: (string) $data['provider'],
            grossAmount: (int) $data['gross_amount'],
            feeAmount: (int) $data['fee_amount'],
            netAmount: (int) $data['net_amount'],
            currency: (string) $data['currency'],
        );
    }

    /** @return array<string,mixed> */
    public function jsonSerialize(): array
    {
        return [
            'provider' => $this->provider,
            'gross_amount' => $this->grossAmount,
            'fee_amount' => $this->feeAmount,
            'net_amount' => $this->netAmount,
            'currency' => $this->currency,
        ];
    }
}
