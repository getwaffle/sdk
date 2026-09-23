<?php

declare(strict_types=1);

namespace Waffle\Enum;

/**
 * How an API key's scopes were assigned (`GET /v1/whoami`'s `preset`
 * field). The scope vocabulary itself is fixed:
 * `charges:read`, `charges:write`, `payouts:read`, `payouts:write`,
 * `balance:read` — a `*:write` scope does NOT imply the matching
 * `*:read`.
 */
enum ApiKeyPreset: string
{
    /** `charges:read`, `payouts:read`, `balance:read`. */
    case ReadOnly = 'read_only';
    /** `charges:read`, `charges:write`, `balance:read`. */
    case AcceptPayments = 'accept_payments';
    /** All five scopes. */
    case Full = 'full';
    /** A hand-picked scope list that doesn't match any named preset. */
    case Custom = 'custom';
}
