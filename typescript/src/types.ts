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
  metadata?: Record<string, string>;
}

/** Response body for `POST /v1/charges` (`201`). No `provider` field. */
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
}

/** Response body for `POST /v1/fees/calculate` (`200`). No `provider` field. */
export interface FeeQuote {
  grossAmount: number;
  feeAmount: number;
  netAmount: number;
  currency: Currency;
}

/** Request body for `POST /v1/bank-accounts`. All fields required. */
export interface RegisterBankAccountParams {
  bankCode: string;
  accountNumber: string;
  accountHolderName: string;
}

/** Response body for `POST /v1/bank-accounts` (`201`) and `GET /v1/bank-accounts` (`200`). */
export interface BankAccount {
  id: string;
  bankCode: string;
  accountNumber: string;
  accountHolderName: string;
  /**
   * When this became the active withdrawal account for the caller's
   * mode. Present from `getActiveBankAccount`; a payout drawn against
   * an account within 6 hours of this timestamp is held rather than
   * dispatched — see {@link PayoutStatus}.
   */
  createdAt?: string;
}

/**
 * Request body for `POST /v1/payouts`. No `provider` field — like
 * `POST /v1/charges`, this auto-routes to the merchant's
 * highest-priority connected PSP (payouts used to require naming one
 * explicitly; that asymmetry with charges is gone).
 */
export interface CreatePayoutParams {
  bankAccountId: string;
  amount: number;
  currency: Currency;
}

/**
 * Response body for `POST /v1/payouts` (`201`). No `provider` field —
 * which PSP handled the payout is never surfaced to the merchant.
 * `status: "held"` means the payout was claimed (debited) but drawn
 * against a bank account registered within the last 6 hours — a
 * security hold on withdrawal-account changes (see
 * {@link BankAccount.createdAt}) — so it is deliberately not yet
 * dispatched to a PSP; the server resumes it automatically once the
 * account has aged past the window, no caller action needed.
 */
export interface Payout {
  id: string;
  bankAccountId: string;
  mode: Mode;
  status: PayoutStatus;
  amount: number;
  currency: Currency;
  /** Omitted by the server when empty. */
  failureReason?: string;
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
