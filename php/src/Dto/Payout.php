<?php

declare(strict_types=1);

namespace Waffle\Dto;

use Waffle\Enum\Mode;
use Waffle\Enum\PayoutStatus;

/** Response body for POST /v1/payouts (201). */
final class Payout implements \JsonSerializable
{
    /** @param int $amount integer minor-unit amount — never a float */
    public function __construct(
        public readonly string $id,
        public readonly string $bankAccountId,
        public readonly Mode $mode,
        public readonly PayoutStatus $status,
        public readonly int $amount,
        public readonly string $currency,
        public readonly ?string $failureReason,
    ) {
    }

    /** @param array<string,mixed> $data */
    public static function fromArray(array $data): self
    {
        return new self(
            id: (string) $data['id'],
            bankAccountId: (string) $data['bank_account_id'],
            mode: Mode::from((string) $data['mode']),
            status: PayoutStatus::from((string) $data['status']),
            amount: (int) $data['amount'],
            currency: (string) $data['currency'],
            failureReason: isset($data['failure_reason']) ? (string) $data['failure_reason'] : null,
        );
    }

    /** @return array<string,mixed> */
    public function jsonSerialize(): array
    {
        return [
            'id' => $this->id,
            'bank_account_id' => $this->bankAccountId,
            'mode' => $this->mode->value,
            'status' => $this->status->value,
            'amount' => $this->amount,
            'currency' => $this->currency,
            'failure_reason' => $this->failureReason,
        ];
    }
}
