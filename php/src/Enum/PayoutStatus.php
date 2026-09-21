<?php

declare(strict_types=1);

namespace Waffle\Enum;

/** Lifecycle status of a Payout (POST /v1/payouts). */
enum PayoutStatus: string
{
    case Pending = 'pending';
    /**
     * Claimed (debited) but drawn against a bank account registered
     * within the last 6 hours — a security hold on withdrawal-account
     * changes (see BankAccount::$createdAt) — so deliberately not yet
     * dispatched to a PSP. The server resumes it automatically once the
     * account has aged past the window; no caller action needed.
     */
    case Held = 'held';
    case Processing = 'processing';
    case Completed = 'completed';
    case Failed = 'failed';
}
