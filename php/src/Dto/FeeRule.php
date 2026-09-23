<?php

declare(strict_types=1);

namespace Waffle\Dto;

/**
 * The fee rule snapshot captured on a charge at the moment it was priced
 * (`Charge::$breakdown`'s `fee_rule`). Copied by value at charge time, so
 * a later admin price edit can never change what a past charge is shown
 * to have charged.
 */
final class FeeRule implements \JsonSerializable
{
    /**
     * @param string $type `"percentage"` or `"flat"`
     * @param int $percentBps basis points (100 = 1%); 0 for a flat rule
     * @param int $flatAmount integer minor-unit amount — the whole fee
     *   for a `"flat"` rule, or an additive surcharge on top of the
     *   percentage for a `"percentage"` rule with a flat component
     */
    public function __construct(
        public readonly string $type,
        public readonly int $percentBps,
        public readonly int $flatAmount,
    ) {
    }

    /** @param array<string,mixed> $data */
    public static function fromArray(array $data): self
    {
        return new self(
            type: (string) $data['type'],
            percentBps: (int) $data['percent_bps'],
            flatAmount: (int) $data['flat_amount'],
        );
    }

    /** @return array<string,mixed> */
    public function jsonSerialize(): array
    {
        return [
            'type' => $this->type,
            'percent_bps' => $this->percentBps,
            'flat_amount' => $this->flatAmount,
        ];
    }
}
