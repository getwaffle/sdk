<?php

declare(strict_types=1);

namespace Paybridge\Dto;

use Paybridge\Enum\ChargeStatus;
use Paybridge\Enum\Mode;

/** Response body for POST /v1/charges (201). */
final class Charge implements \JsonSerializable
{
    /**
     * @param int $grossAmount integer minor-unit amount — never a float
     * @param int $feeAmount integer minor-unit amount — never a float
     * @param int $netAmount integer minor-unit amount — never a float
     * @param array<string,string>|null $metadata
     */
    public function __construct(
        public readonly string $id,
        public readonly string $provider,
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
    ) {
    }

    /** @param array<string,mixed> $data */
    public static function fromArray(array $data): self
    {
        return new self(
            id: (string) $data['id'],
            provider: (string) $data['provider'],
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
        );
    }

    /** @return array<string,mixed> */
    public function jsonSerialize(): array
    {
        return [
            'id' => $this->id,
            'provider' => $this->provider,
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
        ];
    }
}
