<?php

declare(strict_types=1);

namespace Waffle\Dto;

/**
 * Response body for GET /v1/charges (200): a cursor-paginated page.
 * Page forward by passing the last item in $data's `id` as the next
 * call's `startingAfter` — there is no separate cursor token. Prefer
 * {@see \Waffle\Client::listAllCharges()} over paging through this by
 * hand when you just want every matching charge.
 */
final class ChargeList implements \JsonSerializable
{
    /** @param array<Charge> $data */
    public function __construct(
        public readonly array $data,
        public readonly bool $hasMore,
    ) {
    }

    /** @param array<string,mixed> $data */
    public static function fromArray(array $data): self
    {
        $items = [];
        /** @var array<string,mixed> $row */
        foreach ((array) ($data['data'] ?? []) as $row) {
            $items[] = Charge::fromArray($row);
        }

        return new self(
            data: $items,
            hasMore: (bool) ($data['has_more'] ?? false),
        );
    }

    /** @return array<string,mixed> */
    public function jsonSerialize(): array
    {
        return [
            'data' => array_map(static fn (Charge $charge): array => $charge->jsonSerialize(), $this->data),
            'has_more' => $this->hasMore,
        ];
    }
}
