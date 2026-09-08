import { PaybridgeError } from "./errors.js";
import type {
  Balance,
  BankAccount,
  CalculateFeeParams,
  Charge,
  CreateChargeParams,
  CreatePayoutParams,
  FeeQuote,
  Payout,
  RegisterBankAccountParams,
} from "./types.js";

/** Default merchant API base URL — matches the local `docker-compose.yml` dev stack. */
export const DEFAULT_BASE_URL = "http://localhost:8080";

export interface PaybridgeClientOptions {
  /** Merchant API key, sent as `Authorization: Bearer <apiKey>`. */
  apiKey: string;
  /**
   * Merchant API base URL. Defaults to `http://localhost:8080` (the local
   * dev stack from `docker-compose.yml`). No public production hostname is
   * defined in the API contract yet — pass your deployment's URL here
   * once one exists.
   */
  baseUrl?: string;
  /** Injectable `fetch` implementation; defaults to the global `fetch`. */
  fetch?: typeof fetch;
}

interface RawChargeResponse {
  id: string;
  provider: string;
  mode: "live" | "sandbox";
  status: "pending" | "paid" | "failed" | "expired";
  gross_amount: number;
  fee_amount: number;
  net_amount: number;
  currency: string;
  checkout_url?: string;
  created_at: string;
  metadata?: Record<string, string>;
}

interface RawFeeQuoteResponse {
  provider: string;
  gross_amount: number;
  fee_amount: number;
  net_amount: number;
  currency: string;
}

interface RawBankAccountResponse {
  id: string;
  bank_code: string;
  account_number: string;
  account_holder_name: string;
}

interface RawPayoutResponse {
  id: string;
  bank_account_id: string;
  provider: string;
  mode: "live" | "sandbox";
  status: "pending" | "processing" | "completed" | "failed";
  amount: number;
  currency: string;
  failure_reason?: string;
}

interface RawBalanceResponse {
  currency: string;
  amount: number;
}

interface RawErrorResponse {
  error: string;
}

/**
 * Client for the Paybridge merchant API (`:8080` by default).
 *
 * The wire format is snake_case JSON; this SDK exposes camelCase request
 * and response fields and converts between the two internally. Every
 * money amount is an integer in the currency's minor unit — never a
 * float.
 */
export class PaybridgeClient {
  private readonly apiKey: string;
  private readonly baseUrl: string;
  private readonly fetchImpl: typeof fetch;

  constructor(options: PaybridgeClientOptions) {
    if (!options.apiKey) {
      throw new Error("PaybridgeClient: apiKey is required");
    }
    this.apiKey = options.apiKey;
    this.baseUrl = (options.baseUrl ?? DEFAULT_BASE_URL).replace(/\/+$/, "");
    this.fetchImpl = options.fetch ?? fetch;
  }

  /**
   * `POST /v1/charges`. `params.provider` is optional — omit it to let the
   * server auto-route to the merchant's highest-priority connected PSP.
   * `idempotencyKey` is required and must be supplied by the caller
   * (see {@link generateIdempotencyKey}); retrying the exact same key
   * returns the original charge rather than creating a duplicate.
   */
  async createCharge(
    params: CreateChargeParams,
    idempotencyKey: string,
  ): Promise<Charge> {
    const body: Record<string, unknown> = {
      amount: params.amount,
      currency: params.currency,
    };
    if (params.provider !== undefined) body["provider"] = params.provider;
    if (params.description !== undefined) body["description"] = params.description;
    if (params.customerRef !== undefined) body["customer_ref"] = params.customerRef;
    if (params.returnUrl !== undefined) body["return_url"] = params.returnUrl;
    if (params.expiresInMinutes !== undefined) {
      body["expires_in_minutes"] = params.expiresInMinutes;
    }
    if (params.metadata !== undefined) body["metadata"] = params.metadata;

    const raw = await this.requestJson<RawChargeResponse>("POST", "/v1/charges", {
      body,
      idempotencyKey,
    });
    return {
      id: raw.id,
      provider: raw.provider,
      mode: raw.mode,
      status: raw.status,
      grossAmount: raw.gross_amount,
      feeAmount: raw.fee_amount,
      netAmount: raw.net_amount,
      currency: raw.currency,
      ...(raw.checkout_url !== undefined ? { checkoutUrl: raw.checkout_url } : {}),
      createdAt: raw.created_at,
      ...(raw.metadata !== undefined ? { metadata: raw.metadata } : {}),
    };
  }

  /**
   * `POST /v1/fees/calculate`. Preview-only — no charge, no ledger write,
   * no provider call, no idempotency key needed. Unlike
   * {@link PaybridgeClient.createCharge}, `params.provider` is required:
   * this endpoint quotes a specific PSP rather than auto-routing.
   */
  async calculateFee(params: CalculateFeeParams): Promise<FeeQuote> {
    const raw = await this.requestJson<RawFeeQuoteResponse>(
      "POST",
      "/v1/fees/calculate",
      {
        body: {
          provider: params.provider,
          amount: params.amount,
          currency: params.currency,
        },
      },
    );
    return {
      provider: raw.provider,
      grossAmount: raw.gross_amount,
      feeAmount: raw.fee_amount,
      netAmount: raw.net_amount,
      currency: raw.currency,
    };
  }

  /** `POST /v1/bank-accounts`. All three fields are required. */
  async registerBankAccount(
    params: RegisterBankAccountParams,
  ): Promise<BankAccount> {
    const raw = await this.requestJson<RawBankAccountResponse>(
      "POST",
      "/v1/bank-accounts",
      {
        body: {
          bank_code: params.bankCode,
          account_number: params.accountNumber,
          account_holder_name: params.accountHolderName,
        },
      },
    );
    return {
      id: raw.id,
      bankCode: raw.bank_code,
      accountNumber: raw.account_number,
      accountHolderName: raw.account_holder_name,
    };
  }

  /**
   * `POST /v1/payouts`. `params.provider` is required — payout
   * auto-routing does not exist. `idempotencyKey` is required, same
   * semantics as {@link PaybridgeClient.createCharge}.
   */
  async createPayout(
    params: CreatePayoutParams,
    idempotencyKey: string,
  ): Promise<Payout> {
    const raw = await this.requestJson<RawPayoutResponse>("POST", "/v1/payouts", {
      body: {
        bank_account_id: params.bankAccountId,
        provider: params.provider,
        amount: params.amount,
        currency: params.currency,
      },
      idempotencyKey,
    });
    return {
      id: raw.id,
      bankAccountId: raw.bank_account_id,
      provider: raw.provider,
      mode: raw.mode,
      status: raw.status,
      amount: raw.amount,
      currency: raw.currency,
      ...(raw.failure_reason !== undefined
        ? { failureReason: raw.failure_reason }
        : {}),
    };
  }

  /**
   * `GET /v1/balance`. This is withdrawable balance — settled paid
   * charges minus non-failed payouts. Charges inside their settlement
   * hold window do not count yet. `currency` defaults server-side to
   * `IDR` when omitted.
   */
  async getBalance(currency?: string): Promise<Balance> {
    const path = currency
      ? `/v1/balance?currency=${encodeURIComponent(currency)}`
      : "/v1/balance";
    const raw = await this.requestJson<RawBalanceResponse>("GET", path, {});
    return { currency: raw.currency, amount: raw.amount };
  }

  /** `GET /healthz`. No auth. Returns `true` when the body is `"ok"`. */
  async healthz(): Promise<boolean> {
    const res = await this.fetchImpl(`${this.baseUrl}/healthz`, { method: "GET" });
    if (!res.ok) {
      const text = await res.text();
      throw new PaybridgeError(res.status, text || res.statusText);
    }
    const text = await res.text();
    return text.trim() === "ok";
  }

  private async requestJson<T>(
    method: "GET" | "POST",
    path: string,
    opts: { body?: Record<string, unknown>; idempotencyKey?: string },
  ): Promise<T> {
    const headers: Record<string, string> = {
      Authorization: `Bearer ${this.apiKey}`,
    };
    if (opts.body !== undefined) headers["Content-Type"] = "application/json";
    if (opts.idempotencyKey !== undefined) {
      headers["Idempotency-Key"] = opts.idempotencyKey;
    }

    const res = await this.fetchImpl(`${this.baseUrl}${path}`, {
      method,
      headers,
      ...(opts.body !== undefined ? { body: JSON.stringify(opts.body) } : {}),
    });

    if (!res.ok) {
      let message = res.statusText;
      try {
        const errBody = (await res.json()) as RawErrorResponse;
        if (errBody.error) message = errBody.error;
      } catch {
        // body wasn't JSON — fall back to statusText
      }
      throw new PaybridgeError(res.status, message);
    }

    return (await res.json()) as T;
  }
}
