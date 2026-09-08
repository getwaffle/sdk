<?php

declare(strict_types=1);

namespace Paybridge\Http;

/**
 * Minimal HTTP transport abstraction used by {@see \Paybridge\Client}.
 * Deliberately smaller than a full PSR-18 `ClientInterface`/PSR-7
 * `RequestInterface` so this package pulls in zero HTTP dependencies; the
 * default implementation ({@see CurlTransport}) uses PHP's built-in curl
 * extension. Inject your own implementation (e.g. to adapt a PSR-18
 * client, or a fake for tests) via {@see \Paybridge\Client::__construct}.
 */
interface Transport
{
    /**
     * @param array<string,string> $headers header name => value
     */
    public function send(string $method, string $url, array $headers, ?string $body): TransportResponse;
}
