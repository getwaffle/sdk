<?php

declare(strict_types=1);

namespace Waffle\Exception;

use Waffle\Enum\WebhookErrorReason;

/**
 * Thrown by {@see \Waffle\Webhook::verify()} when a delivery cannot be
 * authenticated. Branch on {@see self::$reason}. Neither the message nor
 * any property contains the signing secret or signature values. Respond
 * with a 4xx so Waffle does not treat the delivery as received.
 */
final class WebhookVerificationException extends WaffleException
{
    public function __construct(
        public readonly WebhookErrorReason $reason,
        string $message,
    ) {
        parent::__construct("webhook verification failed ({$reason->value}): {$message}");
    }
}
