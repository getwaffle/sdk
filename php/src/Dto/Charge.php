<?php

declare(strict_types=1);

namespace Waffle\Dto;

use Waffle\Enum\ChargeStatus;
use Waffle\Enum\Mode;

/**
 * Response body for POST /v1/charges (201), GET /v1/charges/{id}, and
 * each item of GET /v1/charges's `data` array. The latter two add the
 * full paid/settlement timeline ($paidAt/$expiresAt/$settledAt) on top
 * of what a freshly created charge carries.
 */
final class Charge implements \JsonSerializable
{
    /**
     * @param int $grossAmount integer minor-unit amount — never a float
     * @param int $feeAmount integer minor-unit amount — never a float
     * @param int $netAmount integer minor-unit amount — never a float
     * @param array<string,string>|null $metadata
     * @param string|null $checkoutChannelSelection present only for a
     *   charge created with `checkoutChannelSelection: "payer"`
     *   ({@see CreateChargeParams}) that is still awaiting the payer's
     *   channel choice on Waffle's hosted checkout page.
     * @param string|null $paidAt present once the PSP confirmed payment
     * @param string|null $expiresAt present while a pending charge is
     *   still payable
     * @param string|null $settledAt present once the net amount becomes
     *   withdrawable
     */
    public function __construct(
        public readonly string $id,
        public readonly Mode $mode,
        public readonly ChargeStatus $status,
        public readonly int $grossAmount,
        public readonly int $feeAmount,
        public readonly int $netAmount,
        public readonly string $currency,
        public readonly ?string $channel,
        public readonly ?string $checkoutUrl,
        public readonly ?string $qrString,
        public readonly ?string $vaBank,
        public readonly ?string $vaNumber,
        public readonly string $createdAt,
        public readonly ?array $metadata,
        public readonly ?string $checkoutChannelSelection = null,
        public readonly ?ChargeBreakdown $breakdown = null,
        public readonly ?string $paidAt = null,
        public readonly ?string $expiresAt = null,
        public readonly ?string $settledAt = null,
    ) {
    }

    /** @param array<string,mixed> $data */
    public static function fromArray(array $data): self
    {
        return new self(
            id: (string) $data['id'],
            mode: Mode::from((string) $data['mode']),
            status: ChargeStatus::from((string) $data['status']),
            grossAmount: (int) $data['gross_amount'],
            feeAmount: (int) $data['fee_amount'],
            netAmount: (int) $data['net_amount'],
            currency: (string) $data['currency'],
            channel: isset($data['channel']) ? (string) $data['channel'] : null,
            checkoutUrl: isset($data['checkout_url']) ? (string) $data['checkout_url'] : null,
            qrString: isset($data['qr_string']) ? (string) $data['qr_string'] : null,
            vaBank: isset($data['va_bank']) ? (string) $data['va_bank'] : null,
            vaNumber: isset($data['va_number']) ? (string) $data['va_number'] : null,
            createdAt: (string) $data['created_at'],
            metadata: $data['metadata'] ?? null,
            checkoutChannelSelection: isset($data['checkout_channel_selection'])
                ? (string) $data['checkout_channel_selection']
                : null,
            breakdown: isset($data['breakdown']) && is_array($data['breakdown'])
                ? ChargeBreakdown::fromArray($data['breakdown'])
                : null,
            paidAt: isset($data['paid_at']) ? (string) $data['paid_at'] : null,
            expiresAt: isset($data['expires_at']) ? (string) $data['expires_at'] : null,
            settledAt: isset($data['settled_at']) ? (string) $data['settled_at'] : null,
        );
    }

    /** @return array<string,mixed> */
    public function jsonSerialize(): array
    {
        return [
            'id' => $this->id,
            'mode' => $this->mode->value,
            'status' => $this->status->value,
            'gross_amount' => $this->grossAmount,
            'fee_amount' => $this->feeAmount,
            'net_amount' => $this->netAmount,
            'currency' => $this->currency,
            'channel' => $this->channel,
            'checkout_url' => $this->checkoutUrl,
            'qr_string' => $this->qrString,
            'va_bank' => $this->vaBank,
            'va_number' => $this->vaNumber,
            'created_at' => $this->createdAt,
            'metadata' => $this->metadata,
            'checkout_channel_selection' => $this->checkoutChannelSelection,
            'breakdown' => $this->breakdown?->jsonSerialize(),
            'paid_at' => $this->paidAt,
            'expires_at' => $this->expiresAt,
            'settled_at' => $this->settledAt,
        ];
    }
}
