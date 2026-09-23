<?php

declare(strict_types=1);

namespace Waffle\Dto;

/**
 * One entry of GET /v1/banks's response: the public, active-only bank
 * directory (ordered by $sortOrder) backing virtual-account bank
 * pickers. $logoUrl is `null` when the bank has no logo on file.
 */
final class Bank implements \JsonSerializable
{
    public function __construct(
        public readonly string $code,
        public readonly string $name,
        public readonly ?string $logoUrl,
        public readonly int $sortOrder,
    ) {
    }

    /** @param array<string,mixed> $data */
    public static function fromArray(array $data): self
    {
        return new self(
            code: (string) $data['code'],
            name: (string) $data['name'],
            logoUrl: isset($data['logo_url']) ? (string) $data['logo_url'] : null,
            sortOrder: (int) $data['sort_order'],
        );
    }

    /** @return array<string,mixed> */
    public function jsonSerialize(): array
    {
        return [
            'code' => $this->code,
            'name' => $this->name,
            'logo_url' => $this->logoUrl,
            'sort_order' => $this->sortOrder,
        ];
    }
}
