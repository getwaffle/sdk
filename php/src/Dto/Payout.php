<?php

declare(strict_types=1);

namespace Waffle\Dto;

use Waffle\Enum\Mode;
use Waffle\Enum\PayoutStatus;

/**
 * Response body for POST /v1/payouts (201) and GET /v1/payouts/{id} (and
 * each item of GET /v1/payouts's `data` array). The latter two add the
 * payout's own destination detail ($bankCode/$accountNumber/
 * $accountHolderName — **unmasked**, distinct from
 * {@see \Waffle\Client::getBankAccount()}'s masked "current active
 * destination" view) plus the request/complete timeline.
 */
final class Payout implements \JsonSerializable
{
    /**
     * @param int $amount integer minor-unit amount — never a float
     * @param string|null $accountNumber unmasked — this is the
     *   destination detail on the payout itself, not the masked view
     *   {@see \Waffle\Client::getBankAccount()} returns
     * @param string|null $createdAt absent on the POST /v1/payouts (201)
     *   response, present on GET /v1/payouts/{id} and GET /v1/payouts
     * @param string|null $completedAt present once the payout reaches
     *   `PayoutStatus::Completed`
     */
    public function __construct(
        public readonly string $id,
        public readonly string $bankAccountId,
        public readonly Mode $mode,
        public readonly PayoutStatus $status,
        public readonly int $amount,
        public readonly string $currency,
        public readonly ?string $failureReason,
        public readonly ?string $bankCode = null,
        public readonly ?string $accountNumber = null,
        public readonly ?string $accountHolderName = null,
        public readonly ?string $createdAt = null,
        public readonly ?string $completedAt = null,
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
            bankCode: isset($data['bank_code']) ? (string) $data['bank_code'] : null,
            accountNumber: isset($data['account_number']) ? (string) $data['account_number'] : null,
            accountHolderName: isset($data['account_holder_name']) ? (string) $data['account_holder_name'] : null,
            createdAt: isset($data['created_at']) ? (string) $data['created_at'] : null,
            completedAt: isset($data['completed_at']) ? (string) $data['completed_at'] : null,
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
            'bank_code' => $this->bankCode,
            'account_number' => $this->accountNumber,
            'account_holder_name' => $this->accountHolderName,
            'created_at' => $this->createdAt,
            'completed_at' => $this->completedAt,
        ];
    }
}
