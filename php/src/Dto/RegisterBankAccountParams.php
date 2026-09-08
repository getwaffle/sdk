<?php

declare(strict_types=1);

namespace Paybridge\Dto;

/**
 * Request body for {@see \Paybridge\Client::registerBankAccount()}
 * (POST /v1/bank-accounts). All three fields are required — the server
 * returns 400 if any is empty.
 */
final class RegisterBankAccountParams implements \JsonSerializable
{
    public function __construct(
        public readonly string $bankCode,
        public readonly string $accountNumber,
        public readonly string $accountHolderName,
    ) {
    }

    /** @return array<string,mixed> */
    public function toArray(): array
    {
        return [
            'bank_code' => $this->bankCode,
            'account_number' => $this->accountNumber,
            'account_holder_name' => $this->accountHolderName,
        ];
    }

    /** @return array<string,mixed> */
    public function jsonSerialize(): array
    {
        return $this->toArray();
    }
}
