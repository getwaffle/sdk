<?php

declare(strict_types=1);

namespace Waffle\Dto;

/**
 * Response body for GET /v1/bank-accounts (200) — the caller's current
 * active withdrawal account for their mode. `$accountNumber` is masked
 * to the last 4 digits (an API key may confirm a destination is
 * registered, never read it in full — distinct from the unmasked
 * destination detail on {@see \Waffle\Dto\Payout}). Registering a new
 * withdrawal destination is dashboard-session-only now (ledger item 48)
 * — there is no API-key route for it, see
 * {@see \Waffle\Client::getBankAccount()}.
 */
final class BankAccount implements \JsonSerializable
{
    /**
     * @param string $accountNumber masked, e.g. `"••••7890"`
     * @param string $createdAt when this became the active account for
     *   the caller's mode — a payout drawn against it within 6 hours of
     *   this timestamp is held rather than dispatched
     *   (see {@see \Waffle\Enum\PayoutStatus::Held}).
     */
    public function __construct(
        public readonly string $id,
        public readonly string $bankCode,
        public readonly string $accountNumber,
        public readonly string $accountHolderName,
        public readonly string $createdAt,
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
            createdAt: (string) $data['created_at'],
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
