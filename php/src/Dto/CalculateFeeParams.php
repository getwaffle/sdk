<?php

declare(strict_types=1);

namespace Waffle\Dto;

/**
 * Request body for {@see \Waffle\Client::calculateFee()}
 * (POST /v1/fees/calculate).
 *
 * There is no `provider` field — the quote resolves against the same
 * auto-routed provider {@see CreateChargeParams} would actually use, so
 * a previewed fee always matches what a real charge would be billed.
 *
 * `channel`/`vaBank` narrow the quote exactly as {@see CreateChargeParams}
 * consumes them: `channel` picks one product under the auto-routed
 * provider (`"qris"` vs `"virtual_account"` can price differently),
 * `vaBank` narrows a `"virtual_account"` quote to one bank; leave both
 * `null` to match only a wildcard-channel/wildcard-bank fee rule.
 */
final class CalculateFeeParams implements \JsonSerializable
{
    /** @param int $amount integer minor-unit amount — never a float */
    public function __construct(
        public readonly int $amount,
        public readonly string $currency,
        public readonly ?string $channel = null,
        public readonly ?string $vaBank = null,
    ) {
    }

    /** @return array<string,mixed> */
    public function toArray(): array
    {
        $body = [
            'amount' => $this->amount,
            'currency' => $this->currency,
        ];
        if ($this->channel !== null) {
            $body['channel'] = $this->channel;
        }
        if ($this->vaBank !== null) {
            $body['va_bank'] = $this->vaBank;
        }

        return $body;
    }

    /** @return array<string,mixed> */
    public function jsonSerialize(): array
    {
        return $this->toArray();
    }
}
