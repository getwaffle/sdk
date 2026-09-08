<?php

declare(strict_types=1);

namespace Paybridge\Enum;

/** Lifecycle status of a Payout (POST /v1/payouts). */
enum PayoutStatus: string
{
    case Pending = 'pending';
    case Processing = 'processing';
    case Completed = 'completed';
    case Failed = 'failed';
}
