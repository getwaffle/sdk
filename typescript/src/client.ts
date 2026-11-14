import {
  errorFromResponse,
  WaffleError,
  WaffleResponseTooLargeError,
  WaffleTimeoutError,
  WaffleValidationError,
} from "./errors.js";
import { generateIdempotencyKey, validateIdempotencyKey } from "./idempotency.js";
import type {
  Balance,
  Bank,
  BankAccount,
  CalculateFeeParams,
  Charge,
  ChargeBreakdown,
  CreateChargeParams,
  CreatePayoutParams,
  FeeQuote,
  ListChargesParams,
  ListChargesResponse,
  ListPayoutsParams,
  ListPayoutsResponse,
  Payout,
  WhoAmI,
} from "./types.js";

/** Default merchant API base URL — Waffle's production API. */
export const DEFAULT_BASE_URL = "https://api.getwaffle.id";

/** Default per-request timeout in milliseconds. */
export const DEFAULT_TIMEOUT_MS = 30_000;
/** Cap for JSON/text response bodies (8 MiB). */
const MAX_TEXT_BYTES = 8 * 1024 * 1024;
/** Cap for raw (receipt PDF) response bodies (16 MiB). */
const MAX_BINARY_BYTES = 16 * 1024 * 1024;

const LOCAL_HOSTS = new Set(["localhost", "127.0.0.1", "[::1]"]);

/**
 * Validates and normalises a base URL. Requires `https:` (plain `http:` only
 * for localhost, 127.0.0.1 and [::1]); rejects userinfo, query, fragment and
 * an empty host; strips trailing slashes. Throws a plain `Error` otherwise.
 */
export function normalizeBaseUrl(input: string): string {
  const raw = String(input).trim();
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    throw new Error("WaffleClient: baseUrl is not a valid absolute URL");
  }
  if (u.protocol !== "https:" && u.protocol !== "http:") {
    throw new Error("WaffleClient: baseUrl must use https");
  }
  if (!u.hostname) throw new Error("WaffleClient: baseUrl has an empty host");
  if (u.protocol === "http:" && !LOCAL_HOSTS.has(u.hostname)) {
    throw new Error(
      "WaffleClient: baseUrl must use https (http is allowed only for localhost, 127.0.0.1 and [::1])",
    );
  }
  if (u.username || u.password || /^[a-z]+:\/\/[^/?#]*@/i.test(raw)) {
    throw new Error("WaffleClient: baseUrl must not contain credentials (userinfo)");
  }
  if (u.search || u.hash || /[?#]/.test(raw)) {
    throw new Error("WaffleClient: baseUrl must not contain a query string or fragment");
  }
  return `${u.origin}${u.pathname.replace(/\/+$/, "")}`;
}

function requireAmount(amount: unknown): void {
  if (typeof amount !== "number" || !Number.isSafeInteger(amount) || amount <= 0) {
    throw new WaffleValidationError(
      "amount must be a positive safe integer in the currency's minor unit",
    );
  }
}

function redactedKey(apiKey: string): string {
  const m = /^(sk_(?:live|test)_)/.exec(apiKey);
  return `${m ? m[1] : ""}****`;
}

export interface WaffleClientOptions {
  /** Merchant API key, sent as `Authorization: Bearer <apiKey>`. */
  apiKey: string;
  /**
   * Merchant API base URL. Defaults to Waffle's production API
   * (`https://api.getwaffle.id`).
   */
  baseUrl?: string;
  /** Injectable `fetch` implementation; defaults to the global `fetch`. */
  fetch?: typeof fetch;
  /**
   * Per-request timeout in milliseconds (default 30000; must be a positive
   * finite number). On expiry a {@link WaffleTimeoutError} is thrown. For a
   * POST the outcome is UNKNOWN: retry with the SAME idempotency key.
   */
  timeoutMs?: number;
  /**
   * Optional caller `AbortSignal` applied to every request, combined with the
   * timeout. When it aborts, the abort reason/`AbortError` is thrown as is.
   */
  signal?: AbortSignal;
}

interface RawFeeRule {
  type: "percentage" | "flat";
  percent_bps: number;
  flat_amount: number;
}

interface RawChargeBreakdown {
  base_amount: number | null;
  fee_amount: number;
  fee_bearer: "merchant" | "customer" | null;
  fee_rule: RawFeeRule;
  payer_paid: number;
  merchant_receives: number;
  channel?: "qris" | "virtual_account";
  va_bank?: string;
}

interface RawChargeResponse {
  id: string;
  mode: "live" | "sandbox";
  status: "pending" | "paid" | "failed" | "expired";
  gross_amount: number;
  fee_amount: number;
  net_amount: number;
  currency: string;
  channel?: "qris" | "virtual_account";
  checkout_url?: string;
  qr_string?: string;
  va_bank?: string;
  va_number?: string;
  created_at: string;
  metadata?: Record<string, string>;
  breakdown?: RawChargeBreakdown | null;
  checkout_channel_selection?: "merchant" | "payer";
  paid_at?: string;
  expires_at?: string;
  settled_at?: string;
}

interface RawListChargesResponse {
  data: RawChargeResponse[];
  has_more: boolean;
}

interface RawFeeQuoteResponse {
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
  created_at?: string;
}

interface RawBankResponse {
  code: string;
  name: string;
  logo_url: string | null;
  sort_order: number;
}

interface RawPayoutResponse {
  id: string;
  bank_account_id: string;
  mode: "live" | "sandbox";
  status: "pending" | "held" | "processing" | "completed" | "failed";
  amount: number;
  currency: string;
  bank_code?: string;
  account_number?: string;
  account_holder_name?: string;
  created_at?: string;
  completed_at?: string;
  failure_reason?: string;
}

interface RawListPayoutsResponse {
  data: RawPayoutResponse[];
  has_more: boolean;
}

interface RawBalanceResponse {
  currency: string;
  amount: number;
}

interface RawWhoAmIResponse {
  merchant_id: string;
  business_name: string;
  mode: "live" | "sandbox";
  preset: "read_only" | "accept_payments" | "full" | "custom";
  scopes: string[];
}

interface RawErrorResponse {
  error: string;
}

function mapFeeRule(raw: RawFeeRule) {
  return {
    type: raw.type,
    percentBps: raw.percent_bps,
    flatAmount: raw.flat_amount,
  };
}

function mapBreakdown(raw: RawChargeBreakdown): ChargeBreakdown {
  return {
    baseAmount: raw.base_amount,
    feeAmount: raw.fee_amount,
    feeBearer: raw.fee_bearer,
    feeRule: mapFeeRule(raw.fee_rule),
    payerPaid: raw.payer_paid,
    merchantReceives: raw.merchant_receives,
    ...(raw.channel !== undefined ? { channel: raw.channel } : {}),
    ...(raw.va_bank !== undefined ? { vaBank: raw.va_bank } : {}),
  };
}

function mapCharge(raw: RawChargeResponse): Charge {
  return {
    id: raw.id,
    mode: raw.mode,
    status: raw.status,
    grossAmount: raw.gross_amount,
    feeAmount: raw.fee_amount,
    netAmount: raw.net_amount,
    currency: raw.currency,
    ...(raw.channel !== undefined ? { channel: raw.channel } : {}),
    ...(raw.checkout_url !== undefined ? { checkoutUrl: raw.checkout_url } : {}),
    ...(raw.qr_string !== undefined ? { qrString: raw.qr_string } : {}),
    ...(raw.va_bank !== undefined ? { vaBank: raw.va_bank } : {}),
    ...(raw.va_number !== undefined ? { vaNumber: raw.va_number } : {}),
    createdAt: raw.created_at,
    ...(raw.metadata !== undefined ? { metadata: raw.metadata } : {}),
    ...(raw.breakdown !== undefined
      ? { breakdown: raw.breakdown === null ? null : mapBreakdown(raw.breakdown) }
      : {}),
    ...(raw.checkout_channel_selection !== undefined
      ? { checkoutChannelSelection: raw.checkout_channel_selection }
      : {}),
    ...(raw.paid_at !== undefined ? { paidAt: raw.paid_at } : {}),
    ...(raw.expires_at !== undefined ? { expiresAt: raw.expires_at } : {}),
    ...(raw.settled_at !== undefined ? { settledAt: raw.settled_at } : {}),
  };
}

function mapPayout(raw: RawPayoutResponse): Payout {
  return {
    id: raw.id,
    bankAccountId: raw.bank_account_id,
    mode: raw.mode,
    status: raw.status,
    amount: raw.amount,
    currency: raw.currency,
    ...(raw.bank_code !== undefined ? { bankCode: raw.bank_code } : {}),
    ...(raw.account_number !== undefined ? { accountNumber: raw.account_number } : {}),
    ...(raw.account_holder_name !== undefined
      ? { accountHolderName: raw.account_holder_name }
      : {}),
    ...(raw.created_at !== undefined ? { createdAt: raw.created_at } : {}),
    ...(raw.completed_at !== undefined ? { completedAt: raw.completed_at } : {}),
    ...(raw.failure_reason !== undefined ? { failureReason: raw.failure_reason } : {}),
  };
}

function buildQuery(params: Record<string, string | number | undefined>): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined) query.set(key, String(value));
  }
  const qs = query.toString();
  return qs ? `?${qs}` : "";
}

/**
 * Client for the Waffle merchant API.
 *
 * The wire format is snake_case JSON; this SDK exposes camelCase request
 * and response fields and converts between the two internally. Every
 * money amount is an integer in the currency's minor unit — never a
 * float.
 */
export class WaffleClient {
  readonly #apiKey: string;
  readonly #baseUrl: string;
  readonly #fetchImpl: typeof fetch;
  readonly #timeoutMs: number;
  readonly #signal: AbortSignal | undefined;

  constructor(options: WaffleClientOptions) {
    if (!options.apiKey || typeof options.apiKey !== "string") {
      throw new Error("WaffleClient: apiKey is required");
    }
    const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
    if (typeof timeoutMs !== "number" || !Number.isFinite(timeoutMs) || timeoutMs <= 0) {
      throw new Error("WaffleClient: timeoutMs must be a positive finite number");
    }
    this.#apiKey = options.apiKey;
    this.#baseUrl = normalizeBaseUrl(options.baseUrl ?? DEFAULT_BASE_URL);
    this.#fetchImpl = options.fetch ?? fetch;
    this.#timeoutMs = timeoutMs;
    this.#signal = options.signal;
  }

  /** Never exposes the API key: only the mode prefix (`sk_live_`/`sk_test_`) and `****`. */
  toJSON(): { apiKey: string; baseUrl: string } {
    return { apiKey: redactedKey(this.#apiKey), baseUrl: this.#baseUrl };
  }

  [Symbol.for("nodejs.util.inspect.custom")](): { name: string; apiKey: string; baseUrl: string } {
    return { name: "WaffleClient", ...this.toJSON() };
  }

  /**
   * `POST /v1/charges` (requires scope `charges:write`). There is no
   * `provider` field — the server always auto-routes to the merchant's
   * highest-priority connected PSP. `idempotencyKey` is optional; if
   * omitted, one is generated automatically via
   * {@link generateIdempotencyKey}. Pass one explicitly only if you need
   * to control retry semantics yourself (retrying the exact same key
   * returns the original charge rather than creating a duplicate).
   */
  async createCharge(
    params: CreateChargeParams,
    idempotencyKey?: string,
  ): Promise<Charge> {
    requireAmount(params.amount);
    if (idempotencyKey !== undefined) validateIdempotencyKey(idempotencyKey);
    const body: Record<string, unknown> = {
      amount: params.amount,
      currency: params.currency,
    };
    if (params.description !== undefined) body["description"] = params.description;
    if (params.customerRef !== undefined) body["customer_ref"] = params.customerRef;
    if (params.returnUrl !== undefined) body["return_url"] = params.returnUrl;
    if (params.channel !== undefined) body["channel"] = params.channel;
    if (params.vaBank !== undefined) body["va_bank"] = params.vaBank;
    if (params.expiresInMinutes !== undefined) {
      body["expires_in_minutes"] = params.expiresInMinutes;
    }
    if (params.checkoutChannelSelection !== undefined) {
      body["checkout_channel_selection"] = params.checkoutChannelSelection;
    }
    if (params.metadata !== undefined) body["metadata"] = params.metadata;

    const raw = await this.#requestJson<RawChargeResponse>("POST", "/v1/charges", {
      body,
      idempotencyKey: idempotencyKey ?? generateIdempotencyKey(),
    });
    return mapCharge(raw);
  }

  /**
   * `GET /v1/charges/{id}` (requires scope `charges:read`). Throws a
   * {@link WaffleError} with status 404 if the charge doesn't exist (or
   * doesn't belong to this merchant).
   */
  async getCharge(id: string): Promise<Charge> {
    const raw = await this.#requestJson<RawChargeResponse>(
      "GET",
      `/v1/charges/${encodeURIComponent(id)}`,
      {},
    );
    return mapCharge(raw);
  }

  /**
   * `GET /v1/charges` (requires scope `charges:read`). Returns a single
   * page; see {@link WaffleClient.listChargesAutoPaging} for an iterator
   * that pages through all results automatically.
   */
  async listCharges(params: ListChargesParams = {}): Promise<ListChargesResponse> {
    const qs = buildQuery({
      limit: params.limit,
      starting_after: params.startingAfter,
      status: params.status,
      "created[gte]": params.createdGte,
      "created[lte]": params.createdLte,
    });
    const raw = await this.#requestJson<RawListChargesResponse>(
      "GET",
      `/v1/charges${qs}`,
      {},
    );
    return { data: raw.data.map(mapCharge), hasMore: raw.has_more };
  }

  /**
   * Auto-paginating async iterator over `GET /v1/charges` (requires scope
   * `charges:read`). Yields individual charges, transparently fetching
   * the next page via `starting_after` as needed.
   *
   * ```ts
   * for await (const charge of client.listChargesAutoPaging({ status: "paid" })) {
   *   // ...
   * }
   * ```
   */
  async *listChargesAutoPaging(
    params: Omit<ListChargesParams, "startingAfter"> = {},
  ): AsyncGenerator<Charge, void, void> {
    let startingAfter: string | undefined;
    for (;;) {
      const page = await this.listCharges({
        ...params,
        ...(startingAfter !== undefined ? { startingAfter } : {}),
      });
      for (const charge of page.data) yield charge;
      if (!page.hasMore || page.data.length === 0) return;
      startingAfter = page.data[page.data.length - 1]!.id;
    }
  }

  /**
   * `GET /v1/charges/{id}/receipt.pdf` (requires scope `charges:read`).
   * Returns the raw PDF bytes. Throws a {@link WaffleError} with status
   * 409 if the charge isn't `"paid"` yet.
   */
  async getChargeReceipt(id: string): Promise<Uint8Array> {
    return this.#requestBinary("GET", `/v1/charges/${encodeURIComponent(id)}/receipt.pdf`);
  }

  /**
   * `POST /v1/fees/calculate` (requires scope `charges:read`).
   * Preview-only — no charge, no ledger write, no provider call, no
   * idempotency key needed. There is no `provider` field: the quote
   * resolves against the same auto-routed PSP a real charge would use,
   * so a previewed fee always matches what a real charge would be
   * billed.
   */
  async calculateFee(params: CalculateFeeParams): Promise<FeeQuote> {
    requireAmount(params.amount);
    const body: Record<string, unknown> = {
      amount: params.amount,
      currency: params.currency,
    };
    if (params.channel !== undefined) body["channel"] = params.channel;
    if (params.vaBank !== undefined) body["va_bank"] = params.vaBank;

    const raw = await this.#requestJson<RawFeeQuoteResponse>(
      "POST",
      "/v1/fees/calculate",
      { body },
    );
    return {
      grossAmount: raw.gross_amount,
      feeAmount: raw.fee_amount,
      netAmount: raw.net_amount,
      currency: raw.currency,
    };
  }

  /**
   * `GET /v1/bank-accounts` (requires scope `payouts:read`). Returns the
   * caller's currently registered withdrawal account for their mode, with
   * `accountNumber` masked. Bank-account registration is dashboard-only —
   * there is no SDK method for it. Throws a {@link WaffleError} with
   * status 404 if the merchant has never registered one for this mode.
   */
  async getBankAccount(): Promise<BankAccount> {
    const raw = await this.#requestJson<RawBankAccountResponse>(
      "GET",
      "/v1/bank-accounts",
      {},
    );
    return {
      id: raw.id,
      bankCode: raw.bank_code,
      accountNumber: raw.account_number,
      accountHolderName: raw.account_holder_name,
      ...(raw.created_at !== undefined ? { createdAt: raw.created_at } : {}),
    };
  }

  /**
   * `POST /v1/payouts` (requires scope `payouts:write`). There is no
   * `provider` field — like {@link WaffleClient.createCharge}, this
   * auto-routes to the merchant's highest-priority connected PSP.
   * `idempotencyKey` is optional; if omitted, one is generated
   * automatically via {@link generateIdempotencyKey}, same semantics as
   * {@link WaffleClient.createCharge}.
   */
  async createPayout(
    params: CreatePayoutParams,
    idempotencyKey?: string,
  ): Promise<Payout> {
    requireAmount(params.amount);
    if (idempotencyKey !== undefined) validateIdempotencyKey(idempotencyKey);
    const raw = await this.#requestJson<RawPayoutResponse>("POST", "/v1/payouts", {
      body: {
        bank_account_id: params.bankAccountId,
        amount: params.amount,
        currency: params.currency,
      },
      idempotencyKey: idempotencyKey ?? generateIdempotencyKey(),
    });
    return mapPayout(raw);
  }

  /**
   * `GET /v1/payouts/{id}` (requires scope `payouts:read`). Throws a
   * {@link WaffleError} with status 404 if the payout doesn't exist (or
   * doesn't belong to this merchant).
   */
  async getPayout(id: string): Promise<Payout> {
    const raw = await this.#requestJson<RawPayoutResponse>(
      "GET",
      `/v1/payouts/${encodeURIComponent(id)}`,
      {},
    );
    return mapPayout(raw);
  }

  /**
   * `GET /v1/payouts` (requires scope `payouts:read`). Returns a single
   * page; see {@link WaffleClient.listPayoutsAutoPaging} for an iterator
   * that pages through all results automatically.
   */
  async listPayouts(params: ListPayoutsParams = {}): Promise<ListPayoutsResponse> {
    const qs = buildQuery({
      limit: params.limit,
      starting_after: params.startingAfter,
      status: params.status,
      "created[gte]": params.createdGte,
      "created[lte]": params.createdLte,
    });
    const raw = await this.#requestJson<RawListPayoutsResponse>(
      "GET",
      `/v1/payouts${qs}`,
      {},
    );
    return { data: raw.data.map(mapPayout), hasMore: raw.has_more };
  }

  /**
   * Auto-paginating async iterator over `GET /v1/payouts` (requires scope
   * `payouts:read`). Yields individual payouts, transparently fetching
   * the next page via `starting_after` as needed.
   */
  async *listPayoutsAutoPaging(
    params: Omit<ListPayoutsParams, "startingAfter"> = {},
  ): AsyncGenerator<Payout, void, void> {
    let startingAfter: string | undefined;
    for (;;) {
      const page = await this.listPayouts({
        ...params,
        ...(startingAfter !== undefined ? { startingAfter } : {}),
      });
      for (const payout of page.data) yield payout;
      if (!page.hasMore || page.data.length === 0) return;
      startingAfter = page.data[page.data.length - 1]!.id;
    }
  }

  /**
   * `GET /v1/payouts/{id}/receipt.pdf` (requires scope `payouts:read`).
   * Returns the raw PDF bytes. Throws a {@link WaffleError} with status
   * 409 if the payout isn't `"completed"` yet.
   */
  async getPayoutReceipt(id: string): Promise<Uint8Array> {
    return this.#requestBinary("GET", `/v1/payouts/${encodeURIComponent(id)}/receipt.pdf`);
  }

  /**
   * `GET /v1/balance` (requires scope `balance:read`). This is
   * withdrawable balance — settled paid charges minus non-failed
   * payouts. Charges inside their settlement hold window do not count
   * yet. `currency` defaults server-side to `IDR` when omitted.
   */
  async getBalance(currency?: string): Promise<Balance> {
    const path = currency
      ? `/v1/balance?currency=${encodeURIComponent(currency)}`
      : "/v1/balance";
    const raw = await this.#requestJson<RawBalanceResponse>("GET", path, {});
    return { currency: raw.currency, amount: raw.amount };
  }

  /**
   * `GET /v1/whoami`. No scope required — safe to call with any valid
   * key. Callers (e.g. an MCP server built on this SDK) should call this
   * once at startup and use `scopes` to decide which operations to
   * expose, rather than hardcoding assumptions about a key's
   * permissions.
   */
  async whoami(): Promise<WhoAmI> {
    const raw = await this.#requestJson<RawWhoAmIResponse>("GET", "/v1/whoami", {});
    return {
      merchantId: raw.merchant_id,
      businessName: raw.business_name,
      mode: raw.mode,
      preset: raw.preset,
      scopes: raw.scopes as WhoAmI["scopes"],
    };
  }

  /**
   * `GET /v1/banks`. Public and unauthenticated (an API key is sent
   * anyway, harmlessly — the route ignores it), active-only, ordered by
   * `sortOrder`.
   */
  async listBanks(): Promise<Bank[]> {
    const raw = await this.#requestJson<RawBankResponse[]>("GET", "/v1/banks", {});
    return raw.map((b) => ({
      code: b.code,
      name: b.name,
      ...(b.logo_url != null ? { logoUrl: b.logo_url } : {}),
      sortOrder: b.sort_order,
    }));
  }

  /** `GET /healthz`. No auth. Returns `true` when the body is `"ok"`. */
  async healthz(): Promise<boolean> {
    return this.#exec("GET", "/healthz", {}, async (res, signal) => {
      if (!res.ok) {
        throw errorFromResponse(res.status, await this.#parseErrorMessage(res, signal));
      }
      const text = new TextDecoder().decode(
        await readLimited(res, MAX_TEXT_BYTES, signal),
      );
      return text.trim() === "ok";
    });
  }

  /** Builds the request signal: timeout combined with the optional caller signal. */
  #makeSignal(): { signal: AbortSignal; timeout: AbortSignal; cleanup: () => void } {
    const timeout = AbortSignal.timeout(this.#timeoutMs);
    const caller = this.#signal;
    if (!caller) return { signal: timeout, timeout, cleanup: () => {} };
    const anyFn = (AbortSignal as unknown as { any?: (s: AbortSignal[]) => AbortSignal }).any;
    if (typeof anyFn === "function") {
      return { signal: anyFn.call(AbortSignal, [timeout, caller]), timeout, cleanup: () => {} };
    }
    const ctrl = new AbortController();
    const onAbort = (src: AbortSignal) => () => ctrl.abort(src.reason);
    const onT = onAbort(timeout);
    const onC = onAbort(caller);
    if (caller.aborted) ctrl.abort(caller.reason);
    else {
      timeout.addEventListener("abort", onT, { once: true });
      caller.addEventListener("abort", onC, { once: true });
    }
    return {
      signal: ctrl.signal,
      timeout,
      cleanup: () => {
        timeout.removeEventListener("abort", onT);
        caller.removeEventListener("abort", onC);
      },
    };
  }

  async #exec<T>(
    method: "GET" | "POST",
    path: string,
    init: { headers?: Record<string, string>; body?: string },
    handle: (res: Response, signal: AbortSignal) => Promise<T>,
  ): Promise<T> {
    const { signal, timeout, cleanup } = this.#makeSignal();
    try {
      // redirect:"error" so the Authorization header and POST body can never
      // be forwarded by a redirect.
      const res = await this.#fetchImpl(`${this.#baseUrl}${path}`, {
        method,
        ...init,
        redirect: "error",
        signal,
      });
      return await handle(res, signal);
    } catch (err) {
      if (err instanceof WaffleError) throw err;
      if (timeout.aborted && !this.#signal?.aborted) {
        throw new WaffleTimeoutError(method, path, this.#timeoutMs);
      }
      throw err;
    } finally {
      cleanup();
    }
  }

  async #parseErrorMessage(res: Response, signal: AbortSignal): Promise<string> {
    let message = res.statusText;
    try {
      const text = new TextDecoder().decode(await readLimited(res, MAX_TEXT_BYTES, signal));
      const errBody = JSON.parse(text) as { error?: unknown };
      if (typeof errBody?.error === "string" && errBody.error) message = errBody.error;
    } catch {
      if (signal.aborted) throw signal.reason ?? new Error("aborted");
      // body wasn't JSON / too large — fall back to statusText
    }
    return message;
  }

  async #requestJson<T>(
    method: "GET" | "POST",
    path: string,
    opts: { body?: Record<string, unknown>; idempotencyKey?: string },
  ): Promise<T> {
    const headers: Record<string, string> = {
      Authorization: `Bearer ${this.#apiKey}`,
    };
    if (opts.body !== undefined) headers["Content-Type"] = "application/json";
    if (opts.idempotencyKey !== undefined) {
      headers["Idempotency-Key"] = opts.idempotencyKey;
    }
    return this.#exec(
      method,
      path,
      { headers, ...(opts.body !== undefined ? { body: JSON.stringify(opts.body) } : {}) },
      async (res, signal) => {
        if (!res.ok) {
          throw errorFromResponse(res.status, await this.#parseErrorMessage(res, signal));
        }
        const bytes = await readLimited(res, MAX_TEXT_BYTES, signal);
        return JSON.parse(new TextDecoder().decode(bytes)) as T;
      },
    );
  }

  async #requestBinary(method: "GET", path: string): Promise<Uint8Array> {
    return this.#exec(
      method,
      path,
      { headers: { Authorization: `Bearer ${this.#apiKey}` } },
      async (res, signal) => {
        if (!res.ok) {
          throw errorFromResponse(res.status, await this.#parseErrorMessage(res, signal));
        }
        return readLimited(res, MAX_BINARY_BYTES, signal);
      },
    );
  }
}

/**
 * Reads a response body enforcing `limit` bytes: Content-Length is checked
 * first, then the cap is enforced while streaming (the stream is cancelled
 * once exceeded).
 */
async function readLimited(res: Response, limit: number, signal: AbortSignal): Promise<Uint8Array> {
  const declared = res.headers.get("content-length");
  if (declared !== null && /^\d+$/.test(declared.trim()) && Number(declared) > limit) {
    void res.body?.cancel().catch(() => {});
    throw new WaffleResponseTooLargeError(res.status, limit);
  }
  if (!res.body) {
    const buf = new Uint8Array(await res.arrayBuffer());
    if (buf.length > limit) throw new WaffleResponseTooLargeError(res.status, limit);
    return buf;
  }
  const reader = res.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    for (;;) {
      if (signal.aborted) throw signal.reason ?? new Error("aborted");
      const { done, value } = await reader.read();
      if (done) break;
      total += value.length;
      if (total > limit) {
        await reader.cancel().catch(() => {});
        throw new WaffleResponseTooLargeError(res.status, limit);
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }
  const out = new Uint8Array(total);
  let off = 0;
  for (const c of chunks) {
    out.set(c, off);
    off += c.length;
  }
  return out;
}
