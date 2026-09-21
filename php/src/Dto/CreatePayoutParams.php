<?php

declare(strict_types=1);

namespace Waffle\Dto;

/**
 * Request body for {@see \Waffle\Client::createPayout()}
 * (POST /v1/payouts).
 *
 * There is no `provider` field — like {@see CreateChargeParams}, this
 * auto-routes to the merchant's highest-priority connected PSP.
 */
final class CreatePayoutParams implements \JsonSerializable
{
    /** @param int $amount integer minor-unit amount — never a float */
    public function __construct(
        public readonly string $bankAccountId,
        public readonly int $amount,
        public readonly string $currency,
    ) {
    }

    /** @return array<string,mixed> */
    public function toArray(): array
    {
        return [
            'bank_account_id' => $this->bankAccountId,
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
