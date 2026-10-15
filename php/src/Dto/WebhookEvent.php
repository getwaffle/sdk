<?php

declare(strict_types=1);

namespace Waffle\Dto;

/**
 * Verified webhook event envelope returned by {@see \Waffle\Webhook::verify()}.
 * Fields missing from the payload are null.
 */
final class WebhookEvent
{
    /**
     * @param int|null $amount integer minor units — never a float
     * @param string|null $deliveryId the X-Waffle-Delivery header: stable across retries of
     *                                the same event, so deduplicate on it
     * @param array<string,mixed> $raw the complete decoded JSON object (snake_case)
     */
    public function __construct(
        public readonly ?string $event,
        public readonly ?string $eventId,
        public readonly ?string $createdAt,
        public readonly ?string $chargeId,
        public readonly ?string $payoutId,
        public readonly ?string $status,
        public readonly ?int $amount,
        public readonly ?string $currency,
        public readonly ?string $mode,
        public readonly ?string $deliveryId,
        public readonly array $raw,
    ) {
    }

    /**
     * @param array<string,mixed> $data
     */
    public static function fromArray(array $data, ?string $deliveryId): self
    {
        $str = static fn (string $k): ?string => isset($data[$k]) && is_string($data[$k]) ? $data[$k] : null;

        return new self(
            event: $str('event'),
            eventId: $str('event_id'),
            createdAt: $str('created_at'),
            chargeId: $str('charge_id'),
            payoutId: $str('payout_id'),
            status: $str('status'),
            amount: isset($data['amount']) && is_int($data['amount']) ? $data['amount'] : null,
            currency: $str('currency'),
            mode: $str('mode'),
            deliveryId: $deliveryId,
            raw: $data,
        );
    }
}
