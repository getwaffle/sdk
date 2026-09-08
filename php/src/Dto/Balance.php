<?php

declare(strict_types=1);

namespace Paybridge\Dto;

/**
 * Response body for GET /v1/balance (200). This is withdrawable balance —
 * settled paid charges minus non-failed payouts. A charge inside its
 * settlement hold window does not count yet even if its status is "paid".
 */
final class Balance implements \JsonSerializable
{
    /** @param int $amount integer minor-unit amount — never a float */
    public function __construct(
        public readonly string $currency,
        public readonly int $amount,
    ) {
    }

    /** @param array<string,mixed> $data */
    public static function fromArray(array $data): self
    {
        return new self(
            currency: (string) $data['currency'],
            amount: (int) $data['amount'],
        );
    }

    /** @return array<string,mixed> */
    public function jsonSerialize(): array
    {
        return [
            'currency' => $this->currency,
            'amount' => $this->amount,
        ];
    }
}
