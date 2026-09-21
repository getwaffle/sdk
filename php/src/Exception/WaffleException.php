<?php

declare(strict_types=1);

namespace Waffle\Exception;

/**
 * Base exception for every error raised by this SDK. Catch this broadly,
 * or {@see WaffleApiException} narrowly for HTTP-level failures.
 */
class WaffleException extends \RuntimeException
{
}
