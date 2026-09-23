<?php

declare(strict_types=1);

namespace Waffle\Dto;

/**
 * `Charge::$breakdown` — the merchant/API-key-safe answer to "35300
 * dicharge, 300 nya untuk apa" (never provider cost or margin, which
 * this scope can never see). Present on `POST /v1/charges`,
 * `GET /v1/charges/{id}`, and `GET /v1/charges`.
 *
 * `null` on the owning {@see Charge} for a two-phase (`checkout_channel_selection:
 * "payer"`) charge still awaiting the payer's channel choice — nothing
 * certain to show yet.
 */
final class ChargeBreakdown implements \JsonSerializable
{
    /**
     * @param int|null $baseAmount integer minor-unit amount, or `null`
     *   for a charge created before this snapshot existed — its
     *   `gross_amount`/`fee_amount`/`net_amount` on the parent
     *   {@see Charge} are still authoritative, there is just no certain
     *   rule/bearer to show alongside them. Never re-derive a rate from
     *   those amounts to fill the gap.
     * @param int $feeAmount integer minor-unit amount — never a float
     * @param string|null $feeBearer `"merchant"` or `"customer"` (never
     *   `"payer"` — that value belongs to the unrelated
     *   `checkout_channel_selection` request field, which encodes who
     *   *picks* the channel, not who *pays* the fee), or `null` alongside
     *   a `null` $baseAmount.
     * @param int $payerPaid integer minor-unit amount — always certain
     *   regardless of $feeBearer
     * @param int $merchantReceives integer minor-unit amount — always
     *   certain regardless of $feeBearer; `gross_amount - fee_amount`
     *   always equals `net_amount` on the parent {@see Charge}
     * @param string $channel `""` when the charge didn't narrow to a
     *   specific channel
     * @param string $vaBank `""` unless $channel is `"virtual_account"`
     */
    public function __construct(
        public readonly ?int $baseAmount,
        public readonly int $feeAmount,
        public readonly ?string $feeBearer,
        public readonly ?FeeRule $feeRule,
        public readonly int $payerPaid,
        public readonly int $merchantReceives,
        public readonly string $channel,
        public readonly string $vaBank,
    ) {
    }

    /** @param array<string,mixed> $data */
    public static function fromArray(array $data): self
    {
        return new self(
            baseAmount: isset($data['base_amount']) ? (int) $data['base_amount'] : null,
            feeAmount: (int) $data['fee_amount'],
            feeBearer: isset($data['fee_bearer']) ? (string) $data['fee_bearer'] : null,
            feeRule: isset($data['fee_rule']) && is_array($data['fee_rule'])
                ? FeeRule::fromArray($data['fee_rule'])
                : null,
            payerPaid: (int) $data['payer_paid'],
            merchantReceives: (int) $data['merchant_receives'],
            channel: (string) ($data['channel'] ?? ''),
            vaBank: (string) ($data['va_bank'] ?? ''),
        );
    }

    /** @return array<string,mixed> */
    public function jsonSerialize(): array
    {
        return [
            'base_amount' => $this->baseAmount,
            'fee_amount' => $this->feeAmount,
            'fee_bearer' => $this->feeBearer,
            'fee_rule' => $this->feeRule?->jsonSerialize(),
            'payer_paid' => $this->payerPaid,
            'merchant_receives' => $this->merchantReceives,
            'channel' => $this->channel,
            'va_bank' => $this->vaBank,
        ];
    }
}
