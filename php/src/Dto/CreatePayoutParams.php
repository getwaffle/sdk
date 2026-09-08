<?php

declare(strict_types=1);

namespace Paybridge\Dto;

/**
 * Request body for {@see \Paybridge\Client::createPayout()}
 * (POST /v1/payouts).
 *
 * `provider` is required — payout auto-routing does not exist (charge
 * auto-routing does; the two endpoints are not symmetric).
 */
final class CreatePayoutParams implements \JsonSerializable
{
    /** @param int $amount integer minor-unit amount — never a float */
    public function __construct(
        public readonly string $bankAccountId,
        public readonly string $provider,
        public readonly int $amount,
        public readonly string $currency,
    ) {
    }

    /** @return array<string,mixed> */
    public function toArray(): array
    {
        return [
            'bank_account_id' => $this->bankAccountId,
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
