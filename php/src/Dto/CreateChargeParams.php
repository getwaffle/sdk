<?php

declare(strict_types=1);

namespace Paybridge\Dto;

/**
 * Request body for {@see \Paybridge\Client::createCharge()}
 * (POST /v1/charges).
 *
 * `provider` is optional: leave it `null` to let the server auto-route to
 * the merchant's highest-priority connected PSP (xendit > doku > gdc > sandbox);
 * the response reports which one was picked. This is NOT symmetric with
 * {@see CalculateFeeParams} or {@see CreatePayoutParams}, where `provider`
 * is required — do not assume the two endpoints behave alike.
 *
 * `channel` selects a specific payment channel instead of the default
 * redirect-based checkout flow. Accepted values are `"qris"` and
 * `"virtual_account"`; leave it `null` for the original redirect-only
 * behavior (a `checkoutUrl` is returned). `vaBank` is required only when
 * `channel` is `"virtual_account"`.
 */
final class CreateChargeParams implements \JsonSerializable
{
    /**
     * @param int $amount integer minor-unit amount — never a float
     * @param array<string,string>|null $metadata
     */
    public function __construct(
        public readonly int $amount,
        public readonly string $currency,
        public readonly ?string $provider = null,
        public readonly ?string $description = null,
        public readonly ?string $customerRef = null,
        public readonly ?string $returnUrl = null,
        public readonly ?int $expiresInMinutes = null,
        public readonly ?array $metadata = null,
        public readonly ?string $channel = null,
        public readonly ?string $vaBank = null,
    ) {
    }

    /** @return array<string,mixed> */
    public function toArray(): array
    {
        $body = [
            'amount' => $this->amount,
            'currency' => $this->currency,
        ];
        if ($this->provider !== null) {
            $body['provider'] = $this->provider;
        }
        if ($this->description !== null) {
            $body['description'] = $this->description;
        }
        if ($this->customerRef !== null) {
            $body['customer_ref'] = $this->customerRef;
        }
        if ($this->returnUrl !== null) {
            $body['return_url'] = $this->returnUrl;
        }
        if ($this->expiresInMinutes !== null) {
            $body['expires_in_minutes'] = $this->expiresInMinutes;
        }
        if ($this->channel !== null) {
            $body['channel'] = $this->channel;
        }
        if ($this->vaBank !== null) {
            $body['va_bank'] = $this->vaBank;
        }
        if ($this->metadata !== null) {
            $body['metadata'] = $this->metadata;
        }

        return $body;
    }

    /** @return array<string,mixed> */
    public function jsonSerialize(): array
    {
        return $this->toArray();
    }
}
