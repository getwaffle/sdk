<?php

declare(strict_types=1);

namespace Paybridge\Dto;

/** Response body for POST /v1/bank-accounts (201). */
final class BankAccount implements \JsonSerializable
{
    public function __construct(
        public readonly string $id,
        public readonly string $bankCode,
        public readonly string $accountNumber,
        public readonly string $accountHolderName,
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
        ];
    }
}
