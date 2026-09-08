<?php

declare(strict_types=1);

namespace Paybridge\Http;

use Paybridge\Exception\PaybridgeException;

/** Default {@see Transport} implementation, backed by PHP's curl extension. */
final class CurlTransport implements Transport
{
    public function __construct(private readonly int $timeoutSeconds = 30)
    {
    }

    public function send(string $method, string $url, array $headers, ?string $body): TransportResponse
    {
        $handle = curl_init($url);
        if ($handle === false) {
            throw new PaybridgeException("Paybridge: failed to initialize curl handle for {$url}");
        }

        $headerLines = [];
        foreach ($headers as $name => $value) {
            $headerLines[] = "{$name}: {$value}";
        }

        curl_setopt_array($handle, [
            CURLOPT_CUSTOMREQUEST => $method,
            CURLOPT_HTTPHEADER => $headerLines,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_CONNECTTIMEOUT => $this->timeoutSeconds,
            CURLOPT_TIMEOUT => $this->timeoutSeconds,
        ]);

        if ($body !== null) {
            curl_setopt($handle, CURLOPT_POSTFIELDS, $body);
        }

        $responseBody = curl_exec($handle);
        if ($responseBody === false) {
            $error = curl_error($handle);
            throw new PaybridgeException("Paybridge: HTTP request to {$url} failed: {$error}");
        }

        $statusCode = (int) curl_getinfo($handle, CURLINFO_RESPONSE_CODE);

        return new TransportResponse($statusCode, (string) $responseBody);
    }
}
