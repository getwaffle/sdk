<?php

declare(strict_types=1);

namespace Paybridge\Tests;

use Paybridge\Http\Transport;
use Paybridge\Http\TransportResponse;

/**
 * Records the single most recent request sent through it and returns a
 * caller-configured canned response. Used by ClientTest to assert exact
 * method/URL/headers/body without a network call.
 */
final class FakeTransport implements Transport
{
    public ?string $lastMethod = null;
    public ?string $lastUrl = null;
    /** @var array<string,string>|null */
    public ?array $lastHeaders = null;
    public ?string $lastBody = null;

    private TransportResponse $response;

    public function __construct(TransportResponse $response)
    {
        $this->response = $response;
    }

    public function send(string $method, string $url, array $headers, ?string $body): TransportResponse
    {
        $this->lastMethod = $method;
        $this->lastUrl = $url;
        $this->lastHeaders = $headers;
        $this->lastBody = $body;

        return $this->response;
    }
}
