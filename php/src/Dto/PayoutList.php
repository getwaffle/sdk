<?php

declare(strict_types=1);

namespace Waffle\Dto;

/**
 * Response body for GET /v1/payouts (200): a cursor-paginated page, same
 * shape/paging rule as {@see ChargeList}. Prefer
 * {@see \Waffle\Client::listAllPayouts()} over paging through this by
 * hand when you just want every matching payout.
 */
final class PayoutList implements \JsonSerializable
{
    /** @param array<Payout> $data */
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
            $items[] = Payout::fromArray($row);
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
            'data' => array_map(static fn (Payout $payout): array => $payout->jsonSerialize(), $this->data),
            'has_more' => $this->hasMore,
        ];
    }
}
