<?php

declare(strict_types=1);

namespace Paybridge\Exception;

/**
 * Base exception for every error raised by this SDK. Catch this broadly,
 * or {@see PaybridgeApiException} narrowly for HTTP-level failures.
 */
class PaybridgeException extends \RuntimeException
{
}
