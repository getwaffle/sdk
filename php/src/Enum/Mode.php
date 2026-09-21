<?php

declare(strict_types=1);

namespace Waffle\Enum;

/**
 * Whether a resource was created against a live or sandbox PSP connection.
 * A sandbox API key can never produce Mode::Live, regardless of what the
 * request body claims (baked in at key issuance, server-side).
 */
enum Mode: string
{
    case Live = 'live';
    case Sandbox = 'sandbox';
}
