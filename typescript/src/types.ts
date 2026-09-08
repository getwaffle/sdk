/**
 * PSP identifier. Known values seen in the wild are `"xendit"`, `"doku"`,
 * and `"sandbox"`, but the set is server-configured — the type stays an
 * open string union so new providers don't require an SDK release.
 */
export type Provider = "xendit" | "doku" | "sandbox" | (string & {});

/** ISO 4217-ish currency code. Amounts are integers in the minor unit. */
export type Currency = "IDR" | (string & {});

export type ChargeStatus = "pending" | "paid" | "failed" | "expired";

/**
 * Payment channel selecting a specific in-app payment method instead of
 * the default redirect-based checkout flow. The server currently defines
 * exactly these two values, so — unlike {@link Provider} — this stays a
 * closed union; add a value here when the server adds a channel.
 */
export type Channel = "qris" | "virtual_account";

export type PayoutStatus =
  | "pending"
  | "processing"
  | "completed"
  | "failed";

export type Mode = "live" | "sandbox";

/**
 * Request body for `POST /v1/charges`.
 *
 * `provider` is optional: omit it to let the server auto-route to the
 * merchant's highest-priority connected PSP. This is NOT symmetric with
 * {@link CalculateFeeParams} or {@link CreatePayoutParams}, where
 * `provider` is required — do not assume the two endpoints behave alike.
 */
export interface CreateChargeParams {
  provider?: Provider;
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

/** Response body for `POST /v1/charges` (`201`). */
export interface Charge {
  id: string;
  provider: Provider;
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
 * Request body for `POST /v1/fees/calculate`. Unlike {@link CreateChargeParams},
 * `provider` is required here — this endpoint quotes a specific PSP, it
 * does not auto-route.
 */
export interface CalculateFeeParams {
  provider: Provider;
  amount: number;
  currency: Currency;
}

/** Response body for `POST /v1/fees/calculate` (`200`). */
export interface FeeQuote {
  provider: Provider;
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

/** Response body for `POST /v1/bank-accounts` (`201`). */
export interface BankAccount {
  id: string;
  bankCode: string;
  accountNumber: string;
  accountHolderName: string;
}

/**
 * Request body for `POST /v1/payouts`. `provider` is required — payout
 * auto-routing does not exist (charge auto-routing does; the two
 * endpoints are not symmetric).
 */
export interface CreatePayoutParams {
  bankAccountId: string;
  provider: Provider;
  amount: number;
  currency: Currency;
}

/** Response body for `POST /v1/payouts` (`201`). */
export interface Payout {
  id: string;
  bankAccountId: string;
  provider: Provider;
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
