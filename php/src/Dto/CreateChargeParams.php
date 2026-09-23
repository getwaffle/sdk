<?php

declare(strict_types=1);

namespace Waffle\Dto;

/**
 * Request body for {@see \Waffle\Client::createCharge()}
 * (POST /v1/charges).
 *
 * There is no `provider` field: the server always auto-routes to the
 * merchant's highest-priority connected PSP — which PSPs are connected,
 * and their priority order, is exclusively an admin-controlled decision,
 * never something a merchant names.
 *
 * `channel` selects a specific payment channel instead of the default
 * redirect-based checkout flow. Accepted values are `"qris"` and
 * `"virtual_account"`; leave it `null` for the original redirect-only
 * behavior (a `checkoutUrl` is returned). `vaBank` is required only when
 * `channel` is `"virtual_account"`.
 *
 * `checkoutChannelSelection` defaults to `"merchant"` server-side (the
 * one-phase flow: Waffle prices and dispatches the charge immediately).
 * Pass `"payer"` to defer both pricing and provider dispatch until the
 * payer picks a method on Waffle's own hosted checkout page
 * (`/pay/{id}`) — this requires `channel` and `vaBank` to be omitted (a
 * merchant cannot both defer the choice and pre-pick it).
 */
final class CreateChargeParams implements \JsonSerializable
{
    /**
     * @param int $amount integer minor-unit amount — never a float
     * @param array<string,string>|null $metadata
     * @param string|null $checkoutChannelSelection `"merchant"` (default)
     *   or `"payer"`
     */
    public function __construct(
        public readonly int $amount,
        public readonly string $currency,
        public readonly ?string $description = null,
        public readonly ?string $customerRef = null,
        public readonly ?string $returnUrl = null,
        public readonly ?int $expiresInMinutes = null,
        public readonly ?array $metadata = null,
        public readonly ?string $channel = null,
        public readonly ?string $vaBank = null,
        public readonly ?string $checkoutChannelSelection = null,
    ) {
    }

    /** @return array<string,mixed> */
    public function toArray(): array
    {
        $body = [
            'amount' => $this->amount,
            'currency' => $this->currency,
        ];
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
        if ($this->checkoutChannelSelection !== null) {
            $body['checkout_channel_selection'] = $this->checkoutChannelSelection;
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
