<?php

declare(strict_types=1);

namespace Waffle\Http;

/**
 * Minimal HTTP transport abstraction used by {@see \Waffle\Client}.
 * Deliberately smaller than a full PSR-18 `ClientInterface`/PSR-7
 * `RequestInterface` so this package pulls in zero HTTP dependencies; the
 * default implementation ({@see CurlTransport}) uses PHP's built-in curl
 * extension. Inject your own implementation (e.g. to adapt a PSR-18
 * client, or a fake for tests) via {@see \Waffle\Client::__construct}.
 */
interface Transport
{
    /**
     * @param array<string,string> $headers header name => value. Carries
     *   the `Authorization` bearer credential, so it is marked
     *   `#[\SensitiveParameter]` (PHP 8.2+ keeps it out of stack traces);
     *   implementations should repeat the attribute and must never log it.
     */
    public function send(
        string $method,
        string $url,
        #[\SensitiveParameter] array $headers,
        ?string $body,
    ): TransportResponse;
}
