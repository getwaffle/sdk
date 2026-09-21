<?php

declare(strict_types=1);

namespace Waffle\Dto;

/** Response body for POST /v1/bank-accounts (201) and GET /v1/bank-accounts (200). */
final class BankAccount implements \JsonSerializable
{
    public function __construct(
        public readonly string $id,
        public readonly string $bankCode,
        public readonly string $accountNumber,
        public readonly string $accountHolderName,
        /**
         * When this became the active withdrawal account for the
         * caller's mode. Present from getActiveBankAccount(); null on
         * registerBankAccount()'s own response. A payout drawn against
         * an account within 6 hours of this timestamp is held rather
         * than dispatched — see PayoutStatus::Held.
         */
        public readonly ?string $createdAt = null,
    ) {
    }

    /** @param array<string,mixed> $data */
    public static function fromArray(array $data): self
    {
        return new self(
            id: (string) $data['id'],
            bankCode: (string) $data['bank_code'],
            accountNumber: (string) $data['account_number'],
            accountHolderName: (string) $data['account_holder_name'],
            createdAt: isset($data['created_at']) ? (string) $data['created_at'] : null,
        );
    }

    /** @return array<string,mixed> */
    public function jsonSerialize(): array
    {
        return [
            'id' => $this->id,
            'bank_code' => $this->bankCode,
            'account_number' => $this->accountNumber,
            'account_holder_name' => $this->accountHolderName,
            'created_at' => $this->createdAt,
        ];
    }
}
