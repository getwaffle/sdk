<?php

declare(strict_types=1);

namespace Waffle\Tests;

use Waffle\Http\Transport;
use Waffle\Http\TransportResponse;

/**
 * Returns one canned {@see TransportResponse} per call, in order — used
 * by ClientTest to exercise multi-page auto-pagination
 * (Client::listAllCharges()/listAllPayouts()), where {@see FakeTransport}'s
 * single most-recent-request/response isn't enough.
 */
final class SequencedTransport implements Transport
{
    /** @var array<string> every URL requested, in order */
    public array $urls = [];

    private int $index = 0;

    /** @param array<TransportResponse> $responses */
    public function __construct(private readonly array $responses)
    {
    }

    public function send(string $method, string $url, array $headers, ?string $body): TransportResponse
    {
        $this->urls[] = $url;

        if (!isset($this->responses[$this->index])) {
            throw new \OutOfBoundsException("SequencedTransport: no canned response left for request #{$this->index} ({$url})");
        }

        return $this->responses[$this->index++];
    }
}
