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
        public readonly ?string $checkoutUrl,
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
            checkoutUrl: isset($data['checkout_url']) ? (string) $data['checkout_url'] : null,
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
            'checkout_url' => $this->checkoutUrl,
            'created_at' => $this->createdAt,
            'metadata' => $this->metadata,
        ];
    }
}
