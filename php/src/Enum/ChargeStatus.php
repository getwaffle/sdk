<?php

declare(strict_types=1);

namespace Paybridge\Enum;

/** Lifecycle status of a Charge (POST /v1/charges). */
enum ChargeStatus: string
{
    case Pending = 'pending';
    case Paid = 'paid';
    case Failed = 'failed';
    case Expired = 'expired';
}
