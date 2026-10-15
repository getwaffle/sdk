<?php

declare(strict_types=1);

namespace Waffle\Enum;

/**
 * Machine-readable cause of a failed {@see \Waffle\Webhook::verify()}.
 */
enum WebhookErrorReason: string
{
    /** No usable signature header (or only the legacy one while legacy is disallowed). */
    case MissingSignature = 'missing_signature';
    /** A signature header was present but could not be parsed. */
    case MalformedHeader = 'malformed_header';
    /** The signature is valid but the signed timestamp is outside the tolerance (possible replay). */
    case TimestampOutOfTolerance = 'timestamp_out_of_tolerance';
    /** No signature matched the body and secret. */
    case SignatureMismatch = 'signature_mismatch';
    /** The signature is valid but the body is not a JSON object. */
    case InvalidJson = 'invalid_json';
}
