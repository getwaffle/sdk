/** ISO 4217-ish currency code. Amounts are integers in the minor unit. */

export type Currency = "IDR" | (string & {});

export type ChargeStatus = "pending" | "paid" | "failed" | "expired";

/**
 * Payment channel selecting a specific in-app payment method instead of
 * the default redirect-based checkout flow. The server currently defines
 * exactly these two values.
 */
export type Channel = "qris" | "virtual_account";

export type PayoutStatus =
  | "pending"
  | "held"
  | "processing"
  | "completed"
  | "failed";

export type Mode = "live" | "sandbox";

/**
 * Which side of a charge's checkout gets to pick the payment channel.
 * `"merchant"` (the default when omitted) means the merchant picks
 * `channel`/`vaBank` up front, same as before this field existed.
 * `"payer"` defers the choice to whoever opens `checkoutUrl` — such a
 * charge has no `channel` until the payer picks one, and its `breakdown`
 * is `null` until then since the fee depends on the chosen channel.
 */
export type CheckoutChannelSelection = "merchant" | "payer";

/** Who absorbs a charge's fee. */
export type FeeBearer = "merchant" | "customer";

export type FeeRuleType = "percentage" | "flat";

/** The fee rule applied to a charge, as returned in `Charge.breakdown`. */
export interface FeeRule {
  type: FeeRuleType;
  /** Percentage fee in basis points (1/100 of a percent). */
  percentBps: number;
  /** Flat fee component, in the charge's minor currency unit. */
  flatAmount: number;
}

/**
 * Itemized fee breakdown for a charge. `null` on the `Charge` itself for
 * an unpriced `checkoutChannelSelection: "payer"` charge — there's no fee
 * to break down until the payer picks a channel.
 */
export interface ChargeBreakdown {
  /** `null` when the charge has no base amount to attribute yet (see above). */
  baseAmount: number | null;
  feeAmount: number;
  /** `null` alongside `baseAmount` for the same reason. */
  feeBearer: FeeBearer | null;
  feeRule: FeeRule;
  payerPaid: number;
  merchantReceives: number;
  channel?: Channel;
  vaBank?: string;
}

/**
 * Request body for `POST /v1/charges`. There is no `provider` field —
 * the server always auto-routes to the merchant's highest-priority
 * connected PSP; which PSPs are connected is an admin-only decision the
 * merchant never names or sees.
 */
export interface CreateChargeParams {
  amount: number;
  currency: Currency;
  description?: string;
  customerRef?: string;
  returnUrl?: string;
  /**
   * Selects a specific payment channel instead of the default
   * redirect-based checkout flow. Omit for the original redirect-only
   * behavior (a `checkoutUrl` is returned). `vaBank` is required only
   * when `channel` is `"virtual_account"`.
   */
  channel?: Channel;
  /** Bank code (e.g. `"BCA"`, `"MANDIRI"`); required only when `channel` is `"virtual_account"`. */
  vaBank?: string;
  expiresInMinutes?: number;
  /** See {@link CheckoutChannelSelection}. Defaults server-side to `"merchant"`. */
  checkoutChannelSelection?: CheckoutChannelSelection;
  metadata?: Record<string, string>;
}

/** Response body for `POST /v1/charges` (`201`) and `GET /v1/charges/{id}` (`200`). No `provider` field. */
export interface Charge {
  id: string;
  mode: Mode;
  status: ChargeStatus;
  grossAmount: number;
  feeAmount: number;
  netAmount: number;
  currency: Currency;
  /** Echoes the request's `channel`, omitted when the default redirect flow was used. */
  channel?: Channel;
  /** Omitted by the server when empty. */
  checkoutUrl?: string;
  /** Raw QRIS payload string. Present only when `channel` was `"qris"`. */
  qrString?: string;
  /** Present only when `channel` was `"virtual_account"`. */
  vaBank?: string;
  /** Present only when `channel` was `"virtual_account"`. */
  vaNumber?: string;
  createdAt: string;
  /** Omitted by the server when empty. */
  metadata?: Record<string, string>;
  /** Itemized fee breakdown. `null`/omitted for an unpriced `"payer"`-selection charge until the payer picks a channel. */
  breakdown?: ChargeBreakdown | null;
  /** Echoes the request's `checkoutChannelSelection`. */
  checkoutChannelSelection?: CheckoutChannelSelection;
  /** Present only from `getCharge`/`listCharges`, once the charge has been paid. */
  paidAt?: string;
  /** Present only from `getCharge`/`listCharges`, when the charge has an expiry. */
  expiresAt?: string;
  /** Present only from `getCharge`/`listCharges`, once the charge has settled. */
  settledAt?: string;
}

/** Query params for `GET /v1/charges`. */
export interface ListChargesParams {
  /** Default 20, max 100. */
  limit?: number;
  /** Cursor: the `id` of the last charge from a previous page. */
  startingAfter?: string;
  status?: ChargeStatus;
  /** RFC3339 or `YYYY-MM-DD`. */
  createdGte?: string;
  /** RFC3339 or `YYYY-MM-DD`. */
  createdLte?: string;
}

/** Response body for `GET /v1/charges` (`200`). */
export interface ListChargesResponse {
  data: Charge[];
  hasMore: boolean;
}

/**
 * Request body for `POST /v1/fees/calculate`. No `provider` field — the
 * quote resolves against the same auto-routed PSP a real charge would
 * use, so a previewed fee always matches what a real charge would be
 * billed.
 */
export interface CalculateFeeParams {
  amount: number;
  currency: Currency;
  channel?: Channel;
  /** Bank code; only meaningful alongside `channel: "virtual_account"`. */
  vaBank?: string;
}

/** Response body for `POST /v1/fees/calculate` (`200`). No `provider` field. */
export interface FeeQuote {
  grossAmount: number;
  feeAmount: number;
  netAmount: number;
  currency: Currency;
}

/**
 * Response body for `GET /v1/bank-accounts` (`200`) — the caller's
 * currently registered withdrawal destination for their mode.
 * `accountNumber` comes back masked; bank-account registration is
 * dashboard-only and has no SDK method (see `docs/api-contract.md` —
 * `POST /v1/bank-accounts` no longer exists).
 */
export interface BankAccount {
  id: string;
  bankCode: string;
  /** Masked by the server, e.g. `"******7890"`. */
  accountNumber: string;
  accountHolderName: string;
  createdAt?: string;
}

/**
 * Request body for `POST /v1/payouts`. No `provider` field — like
 * `POST /v1/charges`, this auto-routes to the merchant's
 * highest-priority connected PSP.
 */
export interface CreatePayoutParams {
  bankAccountId: string;
  amount: number;
  currency: Currency;
}

/**
 * Response body for `POST /v1/payouts` (`201`) and `GET /v1/payouts/{id}`
 * (`200`). No `provider` field — which PSP handled the payout is never
 * surfaced to the merchant. `status: "held"` means the payout was claimed
 * (debited) but drawn against a recently-registered bank account — a
 * security hold on withdrawal-account changes — so it is deliberately not
 * yet dispatched to a PSP; the server resumes it automatically once the
 * hold clears, no caller action needed.
 */
export interface Payout {
  id: string;
  bankAccountId: string;
  mode: Mode;
  status: PayoutStatus;
  amount: number;
  currency: Currency;
  /** Present only from `getPayout`/`listPayouts`. */
  bankCode?: string;
  /** Present only from `getPayout`/`listPayouts`. */
  accountNumber?: string;
  /** Present only from `getPayout`/`listPayouts`. */
  accountHolderName?: string;
  /** Present only from `getPayout`/`listPayouts`. */
  createdAt?: string;
  /** Present only from `getPayout`/`listPayouts`, once completed. */
  completedAt?: string;
  /** Omitted by the server when empty. */
  failureReason?: string;
}

/** Query params for `GET /v1/payouts`. */
export interface ListPayoutsParams {
  /** Default 20, max 100. */
  limit?: number;
  /** Cursor: the `id` of the last payout from a previous page. */
  startingAfter?: string;
  status?: PayoutStatus;
  /** RFC3339 or `YYYY-MM-DD`. */
  createdGte?: string;
  /** RFC3339 or `YYYY-MM-DD`. */
  createdLte?: string;
}

/** Response body for `GET /v1/payouts` (`200`). */
export interface ListPayoutsResponse {
  data: Payout[];
  hasMore: boolean;
}

/** Response body for `GET /v1/balance` (`200`). Withdrawable balance only. */
export interface Balance {
  currency: Currency;
  amount: number;
}

/**
 * Response body entry for `GET /v1/banks` (`200`, array). Public,
 * unauthenticated, active-only, ordered by `sortOrder` — the same
 * directory the checkout page and dashboard bank pickers render from.
 * No `id`/`active` field: every returned row is already active by
 * construction (the server always calls `ListActive`).
 */
export interface Bank {
  code: string;
  name: string;
  /** Omitted when the bank has no logo asset on file. */
  logoUrl?: string;
  sortOrder: number;
}

/** An API key scope. A `*:write` scope does not imply the matching `*:read`. */
export type Scope =
  | "charges:read"
  | "charges:write"
  | "payouts:read"
  | "payouts:write"
  | "balance:read";

/**
 * A named bundle of scopes an API key was issued with. `"read_only"` is
 * `charges:read` + `payouts:read` + `balance:read`; `"accept_payments"` is
 * `charges:read` + `charges:write` + `balance:read`; `"full"` is all five
 * scopes; `"custom"` is any other combination.
 */
export type Preset = "read_only" | "accept_payments" | "full" | "custom";

/**
 * Response body for `GET /v1/whoami` (`200`). No scope required — safe to
 * call with any valid key. Callers (e.g. an MCP server built on this SDK)
 * should call this once at startup and use `scopes` to decide which
 * operations to expose, rather than hardcoding assumptions about a key's
 * permissions.
 */
export interface WhoAmI {
  merchantId: string;
  businessName: string;
  mode: Mode;
  preset: Preset;
  scopes: Scope[];
}
