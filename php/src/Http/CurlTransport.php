<?php

declare(strict_types=1);

namespace Waffle\Http;

use Waffle\Exception\WaffleException;

/**
 * Default {@see Transport} implementation, backed by PHP's curl extension.
 *
 * Hardened defaults: TLS peer and host verification on, redirects never
 * followed (so the bearer key cannot be forwarded to another origin),
 * HTTPS-only (plain HTTP only for a loopback URL), and bounded response
 * sizes (8 MiB, 16 MiB for `/receipt.pdf` downloads).
 */
final class CurlTransport implements Transport
{
    /** Maximum response body for ordinary API calls. */
    public const MAX_RESPONSE_BYTES = 8 * 1024 * 1024;

    /** Maximum response body for raw receipt downloads. */
    public const MAX_RECEIPT_BYTES = 16 * 1024 * 1024;

    public function __construct(private readonly int $timeoutSeconds = 30)
    {
    }

    public function send(
        string $method,
        string $url,
        #[\SensitiveParameter] array $headers,
        ?string $body,
    ): TransportResponse {
        $handle = curl_init($url);
        if ($handle === false) {
            throw new WaffleException("Waffle: failed to initialize curl handle for {$url}");
        }

        $headerLines = [];
        foreach ($headers as $name => $value) {
            $headerLines[] = "{$name}: {$value}";
        }

        $parts = parse_url($url);
        $path = is_array($parts) ? (string) ($parts['path'] ?? '') : '';
        $scheme = is_array($parts) ? strtolower((string) ($parts['scheme'] ?? '')) : '';
        $host = is_array($parts) ? strtolower((string) ($parts['host'] ?? '')) : '';
        $loopbackHttp = $scheme === 'http' && in_array($host, ['localhost', '127.0.0.1', '[::1]'], true);

        $limit = str_ends_with($path, '/receipt.pdf') ? self::MAX_RECEIPT_BYTES : self::MAX_RESPONSE_BYTES;
        $received = '';
        $receivedBytes = 0;
        $tooLarge = false;

        curl_setopt_array($handle, [
            CURLOPT_CUSTOMREQUEST => $method,
            CURLOPT_HTTPHEADER => $headerLines,
            CURLOPT_CONNECTTIMEOUT => $this->timeoutSeconds,
            CURLOPT_TIMEOUT => $this->timeoutSeconds,
            CURLOPT_SSL_VERIFYPEER => true,
            CURLOPT_SSL_VERIFYHOST => 2,
            CURLOPT_FOLLOWLOCATION => false,
            CURLOPT_PROTOCOLS => $loopbackHttp ? (CURLPROTO_HTTPS | CURLPROTO_HTTP) : CURLPROTO_HTTPS,
            CURLOPT_REDIR_PROTOCOLS => CURLPROTO_HTTPS,
            CURLOPT_WRITEFUNCTION => static function ($ch, string $chunk) use (&$received, &$receivedBytes, &$tooLarge, $limit): int {
                $length = strlen($chunk);
                $receivedBytes += $length;
                if ($receivedBytes > $limit) {
                    $tooLarge = true;

                    // Returning a count different from $length aborts the transfer.
                    return 0;
                }
                $received .= $chunk;

                return $length;
            },
        ]);

        if ($body !== null) {
            curl_setopt($handle, CURLOPT_POSTFIELDS, $body);
        }

        $ok = curl_exec($handle);
        if ($tooLarge) {
            throw new WaffleException("Waffle: response from {$url} exceeded the {$limit}-byte size limit");
        }
        if ($ok === false) {
            $error = curl_error($handle);
            throw new WaffleException("Waffle: HTTP request to {$url} failed: {$error}");
        }

        $statusCode = (int) curl_getinfo($handle, CURLINFO_RESPONSE_CODE);

        return new TransportResponse($statusCode, $received);
    }
}
