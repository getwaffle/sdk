import { McpServer } from "@modelcontextprotocol/server";
import {
  WaffleClient,
  WaffleError,
  type Bank,
  type BankAccount,
  type Charge,
  type CreateChargeParams,
  type CreatePayoutParams,
  type ListChargesParams,
  type FeeQuote,
  type ListPayoutsParams,
  type Payout,
  type Scope,
  type WhoAmI,
} from "@getwaffle/sdk";
import * as z from "zod/v4";
import { formatMoney } from "./format.js";
import {
  MAX_ERROR_TEXT,
  MAX_QR,
  UNTRUSTED_PREFACE,
  WriteGuard,
  defaultWriteGuard,
  hashArgs,
  parseConfig,
  returnUrlProblem,
  safeHttpsUrl,
  sanitizeText,
  type McpConfig,
} from "./safety.js";

/** Options for {@link createServer}. */
export interface WaffleMcpServerOptions {
  /** Merchant API key. Its mode (live vs sandbox) is fixed at issuance server-side — a sandbox key can never move live money no matter what a tool call asks for. */
  apiKey: string;
  /** Merchant API base URL. Defaults to `WaffleClient`'s own default (`https://api.getwaffle.id`, production). */
  baseUrl?: string;
  /** Injectable `fetch`, forwarded to `WaffleClient` — used by tests. */
  fetch?: typeof fetch;
  /** Environment to read `WAFFLE_MCP_*` settings from. Defaults to `process.env`; mainly for tests. */
  env?: Record<string, string | undefined>;
  /** Idempotency-key / cumulative-amount state. Defaults to one shared per process. */
  writeGuard?: WriteGuard;
}

const modeSchema = z.enum(["live", "sandbox"]);
const channelSchema = z.enum(["qris", "virtual_account"]);
const chargeStatusSchema = z.enum(["pending", "paid", "failed", "expired"]);
const payoutStatusSchema = z.enum(["pending", "held", "processing", "completed", "failed"]);
const scopeSchema = z.enum(["charges:read", "charges:write", "payouts:read", "payouts:write", "balance:read"]);
const presetSchema = z.enum(["read_only", "accept_payments", "full", "custom"]);
const feeBearerSchema = z.enum(["merchant", "customer"]);
const checkoutChannelSelectionSchema = z.enum(["merchant", "payer"]);

const feeRuleSchema = z.object({
  type: z.enum(["percentage", "flat"]),
  percentBps: z.number(),
  flatAmount: z.number(),
});

const chargeBreakdownSchema = z.object({
  baseAmount: z.number().nullable(),
  feeAmount: z.number(),
  feeBearer: feeBearerSchema.nullable(),
  feeRule: feeRuleSchema,
  payerPaid: z.number(),
  merchantReceives: z.number(),
  channel: channelSchema.optional(),
  vaBank: z.string().optional(),
});

const chargeOutputSchema = z.object({
  id: z.string(),
  mode: modeSchema,
  status: chargeStatusSchema,
  grossAmount: z.number(),
  feeAmount: z.number(),
  netAmount: z.number(),
  currency: z.string(),
  channel: channelSchema.optional(),
  checkoutUrl: z.string().optional(),
  qrString: z.string().optional(),
  vaBank: z.string().optional(),
  vaNumber: z.string().optional(),
  createdAt: z.string(),
  breakdown: chargeBreakdownSchema.nullable().optional(),
  checkoutChannelSelection: checkoutChannelSelectionSchema.optional(),
  paidAt: z.string().optional(),
  expiresAt: z.string().optional(),
  settledAt: z.string().optional(),
});

const bankAccountOutputSchema = z.object({
  id: z.string(),
  bankCode: z.string(),
  accountNumber: z.string(),
  accountHolderName: z.string(),
  createdAt: z.string().optional(),
});

const payoutOutputSchema = z.object({
  id: z.string(),
  bankAccountId: z.string(),
  mode: modeSchema,
  status: payoutStatusSchema,
  amount: z.number(),
  currency: z.string(),
  bankCode: z.string().optional(),
  accountNumber: z.string().optional(),
  accountHolderName: z.string().optional(),
  createdAt: z.string().optional(),
  completedAt: z.string().optional(),
  failureReason: z.string().optional(),
});

const whoamiOutputSchema = z.object({
  merchantId: z.string(),
  businessName: z.string(),
  mode: modeSchema,
  preset: presetSchema,
  scopes: z.array(scopeSchema),
});

const idSchema = z.string().min(1).max(128).regex(/^[A-Za-z0-9_.:-]+$/, "must contain only letters, digits and _ . : -");
const currencySchema = z.string().regex(/^[A-Z]{3}$/, "must be a 3-letter uppercase currency code, e.g. IDR");
const amountSchema = z.number().int().positive().max(Number.MAX_SAFE_INTEGER);
const dateSchema = z
  .string()
  .max(40)
  .regex(/^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2}))?$/, "must be RFC3339 or YYYY-MM-DD")
  .refine((v) => !Number.isNaN(Date.parse(v)), "not a valid date");
const idempotencyKeySchema = z
  .string()
  .min(8)
  .max(128)
  .regex(/^[A-Za-z0-9._:-]+$/, "must contain only letters, digits and _ . : -")
  .describe(
    "REQUIRED. A unique key (8-128 chars, letters/digits/._:-) that you generate ONCE per intended operation. On ANY error, timeout or uncertainty, retry with the SAME key and the SAME arguments; NEVER invent a new key for the same intended operation (that creates a duplicate). Use a new key only for a genuinely new operation the user asked for.",
  );
const metadataSchema = z
  .record(z.string().min(1).max(40), z.string().max(200))
  .refine((m) => Object.keys(m).length <= 20, "at most 20 metadata keys");

const listChargesInputSchema = z.object({
  limit: z.number().int().positive().max(100).optional().describe("Default 20, max 100"),
  startingAfter: idSchema.optional().describe("Cursor: the id of the last charge from a previous page"),
  status: chargeStatusSchema.optional(),
  createdGte: dateSchema.optional().describe("RFC3339 or YYYY-MM-DD"),
  createdLte: dateSchema.optional().describe("RFC3339 or YYYY-MM-DD"),
});

const listPayoutsInputSchema = z.object({
  limit: z.number().int().positive().max(100).optional().describe("Default 20, max 100"),
  startingAfter: idSchema.optional().describe("Cursor: the id of the last payout from a previous page"),
  status: payoutStatusSchema.optional(),
  createdGte: dateSchema.optional().describe("RFC3339 or YYYY-MM-DD"),
  createdLte: dateSchema.optional().describe("RFC3339 or YYYY-MM-DD"),
});

/** An ordinary tool-error result: content the model reads and can react to, never a thrown protocol error. The message is API-originated, so it is sanitized and truncated. */
function errorResult(err: unknown): { content: [{ type: "text"; text: string }]; isError: true } {
  const text =
    err instanceof WaffleError
      ? `${UNTRUSTED_PREFACE}\nWaffle API error (HTTP ${Number.isFinite(err.status) ? err.status : 0}): ${sanitizeText(err.message, MAX_ERROR_TEXT)}`
      : sanitizeText(err instanceof Error ? err.message : String(err), MAX_ERROR_TEXT);
  return { content: [{ type: "text", text }], isError: true };
}

/** Prefixes tool text that carries API-originated data with the untrusted-data notice. */
function wrap(text: string): string {
  return `${UNTRUSTED_PREFACE}\n${text}`;
}

// --- model-facing shaping: explicit allowlists, every string sanitized ------

function num(n: unknown): number {
  return typeof n === "number" && Number.isFinite(n) ? n : 0;
}

function shapeCharge(c: Charge) {
  const b = c.breakdown;
  return {
    id: sanitizeText(c.id),
    mode: c.mode,
    status: c.status,
    grossAmount: num(c.grossAmount),
    feeAmount: num(c.feeAmount),
    netAmount: num(c.netAmount),
    currency: sanitizeText(c.currency, 16),
    ...(c.channel !== undefined ? { channel: c.channel } : {}),
    ...(safeHttpsUrl(c.checkoutUrl) !== undefined ? { checkoutUrl: safeHttpsUrl(c.checkoutUrl) as string } : {}),
    ...(c.qrString ? { qrString: sanitizeText(c.qrString, MAX_QR) } : {}),
    ...(c.vaBank ? { vaBank: sanitizeText(c.vaBank) } : {}),
    ...(c.vaNumber ? { vaNumber: sanitizeText(c.vaNumber, 64) } : {}),
    createdAt: sanitizeText(c.createdAt, 64),
    ...(b
      ? {
          breakdown: {
            baseAmount: b.baseAmount === null ? null : num(b.baseAmount),
            feeAmount: num(b.feeAmount),
            feeBearer: b.feeBearer,
            feeRule: { type: b.feeRule.type, percentBps: num(b.feeRule.percentBps), flatAmount: num(b.feeRule.flatAmount) },
            payerPaid: num(b.payerPaid),
            merchantReceives: num(b.merchantReceives),
            ...(b.channel !== undefined ? { channel: b.channel } : {}),
            ...(b.vaBank ? { vaBank: sanitizeText(b.vaBank) } : {}),
          },
        }
      : b === null
        ? { breakdown: null }
        : {}),
    ...(c.checkoutChannelSelection !== undefined ? { checkoutChannelSelection: c.checkoutChannelSelection } : {}),
    ...(c.paidAt ? { paidAt: sanitizeText(c.paidAt, 64) } : {}),
    ...(c.expiresAt ? { expiresAt: sanitizeText(c.expiresAt, 64) } : {}),
    ...(c.settledAt ? { settledAt: sanitizeText(c.settledAt, 64) } : {}),
    // metadata is deliberately never forwarded to the model.
  };
}
type ShapedCharge = ReturnType<typeof shapeCharge>;

function shapePayout(p: Payout) {
  return {
    id: sanitizeText(p.id),
    bankAccountId: sanitizeText(p.bankAccountId),
    mode: p.mode,
    status: p.status,
    amount: num(p.amount),
    currency: sanitizeText(p.currency, 16),
    ...(p.bankCode ? { bankCode: sanitizeText(p.bankCode, 32) } : {}),
    ...(p.accountNumber ? { accountNumber: sanitizeText(p.accountNumber, 64) } : {}),
    ...(p.accountHolderName ? { accountHolderName: sanitizeText(p.accountHolderName) } : {}),
    ...(p.createdAt ? { createdAt: sanitizeText(p.createdAt, 64) } : {}),
    ...(p.completedAt ? { completedAt: sanitizeText(p.completedAt, 64) } : {}),
    ...(p.failureReason ? { failureReason: sanitizeText(p.failureReason) } : {}),
  };
}
type ShapedPayout = ReturnType<typeof shapePayout>;

function shapeBankAccount(a: BankAccount) {
  return {
    id: sanitizeText(a.id),
    bankCode: sanitizeText(a.bankCode, 32),
    accountNumber: sanitizeText(a.accountNumber, 64),
    accountHolderName: sanitizeText(a.accountHolderName),
    ...(a.createdAt ? { createdAt: sanitizeText(a.createdAt, 64) } : {}),
  };
}

function shapeBank(b: Bank) {
  const logoUrl = safeHttpsUrl(b.logoUrl);
  return {
    code: sanitizeText(b.code, 32),
    name: sanitizeText(b.name),
    ...(logoUrl !== undefined ? { logoUrl } : {}),
    sortOrder: num(b.sortOrder),
  };
}

function shapeQuote(q: FeeQuote) {
  return { grossAmount: num(q.grossAmount), feeAmount: num(q.feeAmount), netAmount: num(q.netAmount), currency: sanitizeText(q.currency, 16) };
}

/** A hand-built tool-error result for a rejection that never touches the API (e.g. a missing `confirm: true`). */
function refusalResult(text: string): { content: [{ type: "text"; text: string }]; isError: true } {
  return { content: [{ type: "text", text }], isError: true };
}

/** Human-readable summary lines shared by create_charge and get_charge (input is already shaped). */
function chargeSummaryLines(charge: ShapedCharge): string[] {
  const lines = [
    `Charge ${charge.id} (${charge.mode}) — status ${charge.status}`,
    `Gross ${formatMoney(charge.grossAmount, charge.currency)}, fee ${formatMoney(charge.feeAmount, charge.currency)}, net ${formatMoney(charge.netAmount, charge.currency)}`,
  ];
  if (charge.checkoutUrl) lines.push(`Checkout: ${charge.checkoutUrl}`);
  if (charge.qrString) lines.push(`QRIS payload: ${charge.qrString}`);
  if (charge.vaBank && charge.vaNumber) lines.push(`Virtual account: ${charge.vaBank} ${charge.vaNumber}`);
  if (charge.paidAt) lines.push(`Paid at: ${charge.paidAt}`);
  return lines;
}

/** Human-readable summary lines shared by create_payout and get_payout (input is already shaped). */
function payoutSummaryLines(payout: ShapedPayout): string[] {
  const heldNote =
    payout.status === "held"
      ? " (held: the bank account was registered within the last 6h; Waffle dispatches it automatically once the hold window passes — no action needed)"
      : "";
  const lines = [`Payout ${payout.id} (${payout.mode}) — status ${payout.status}${heldNote}. Amount ${formatMoney(payout.amount, payout.currency)}.`];
  if (payout.bankCode && payout.accountNumber) lines.push(`To: ${payout.bankCode} ${payout.accountNumber} (${payout.accountHolderName ?? "unknown holder"})`);
  if (payout.completedAt) lines.push(`Completed at: ${payout.completedAt}`);
  if (payout.failureReason) lines.push(`Failure reason: ${payout.failureReason}`);
  return lines;
}

/** Whether a failed write definitely did not happen (so its amount can be released from the session total). */
function definitelyRejected(err: unknown): boolean {
  return err instanceof WaffleError && err.status >= 400 && err.status < 500 && err.status !== 408 && err.status !== 409;
}

/**
 * Builds an MCP server exposing the Waffle **merchant API** (API-key authenticated) as tools — the same surface `@getwaffle/sdk`
 * wraps, not the admin API or the dashboard-session-authenticated
 * `/v1/merchants/me/*` routes. Write tools (create_charge,
 * create_payout) require the caller to supply an `idempotencyKey` and
 * pass it through unchanged, so a retry after a timeout is deduplicated
 * server-side. They are registered only when `WAFFLE_MCP_ENABLE_WRITES=1`
 * (and, for a live key, `WAFFLE_MCP_ALLOW_LIVE=1`), and are bounded by
 * per-call and per-process amount caps. Everything returned to the model
 * is built from an allowlist of fields and sanitized as untrusted data.
 *
 * On start, calls `whoami()` once with the configured key and registers
 * **only** the tools that key's scopes allow — a `read_only` key never
 * even sees `create_charge`/`create_payout` as callable tools, rather
 * than seeing them and getting a 403 at call time. If `whoami()` fails
 * (bad key, unreachable API), this rejects — callers must fail loudly at
 * startup rather than silently serve zero or all tools.
 */
export async function createServer(options: WaffleMcpServerOptions): Promise<McpServer> {
  const config: McpConfig = parseConfig(options.env ?? process.env);
  const guard = options.writeGuard ?? defaultWriteGuard();
  const client = new WaffleClient({
    apiKey: options.apiKey,
    ...(options.baseUrl !== undefined ? { baseUrl: options.baseUrl } : {}),
    ...(options.fetch !== undefined ? { fetch: options.fetch } : {}),
  });

  let who: WhoAmI;
  try {
    who = await client.whoami();
  } catch (err) {
    const reason = err instanceof Error ? err.message : String(err);
    throw new Error(`waffle-mcp: whoami() failed at startup — refusing to start with an unverified key: ${reason}`);
  }

  const scopes = new Set<Scope>(who.scopes);
  const mode = who.mode;
  const moneyNote = mode === "live" ? "Moves real money." : "Sandbox mode — never moves real money.";
  const writesAllowed = config.enableWrites && (mode !== "live" || config.allowLive);
  const returnUrlSchema = z
    .string()
    .max(2048)
    .superRefine((v, ctx) => {
      const problem = returnUrlProblem(v, config.returnUrlAllowlist);
      if (problem) ctx.addIssue({ code: "custom", message: problem });
    });
  const retryRule =
    "idempotencyKey is REQUIRED and is passed to Waffle unchanged: generate one unique key per intended operation. On ANY error, timeout or uncertainty about the outcome, retry with the SAME idempotencyKey and identical arguments (check with the matching get/list tool if unsure). NEVER invent a new key for the same intended operation — that would create a duplicate. Reusing a key with different arguments is rejected.";
  const limitNote = `Per-call limit ${config.maxAmount} minor units; per-session cumulative limit ${config.sessionMaxAmount}.`;

  const server = new McpServer({ name: "waffle", version: "0.1.0" });

  // Always available — no scope required.

  server.registerTool(
    "waffle_whoami",
    {
      title: "Identify the configured API key",
      description:
        "Return the merchant, mode (live/sandbox), preset, and scopes for the API key this server is configured with. No scope required. Useful for confirming which environment you're pointed at before creating a charge or payout.",
      outputSchema: whoamiOutputSchema,
      annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: true },
    },
    async () => {
      try {
        const r = await client.whoami();
        const result = {
          merchantId: sanitizeText(r.merchantId),
          businessName: sanitizeText(r.businessName),
          mode: r.mode,
          preset: r.preset,
          scopes: r.scopes,
        };
        const text = wrap(`${result.businessName} (${result.merchantId}) — mode ${result.mode}, preset ${result.preset}, scopes: ${result.scopes.join(", ") || "none"}`);
        return { content: [{ type: "text", text }], structuredContent: result };
      } catch (err) {
        return errorResult(err);
      }
    },
  );

  server.registerTool(
    "waffle_healthz",
    {
      title: "Check API health",
      description: "Check whether the configured Waffle merchant API base URL is reachable and healthy. No auth required.",
      outputSchema: z.object({ healthy: z.boolean() }),
      annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: true },
    },
    async () => {
      try {
        const healthy = await client.healthz();
        return { content: [{ type: "text", text: healthy ? "healthy" : "unhealthy" }], structuredContent: { healthy } };
      } catch (err) {
        return errorResult(err);
      }
    },
  );

  server.registerTool(
    "waffle_list_banks",
    {
      title: "List supported banks",
      description:
        'List every active bank Waffle supports for virtual-account charges (code, display name, sort order). Bank codes are not fixed — call this before create_charge with channel "virtual_account" to get a valid bank code, rather than guessing or hardcoding one.',
      outputSchema: z.object({
        banks: z.array(
          z.object({
            code: z.string(),
            name: z.string(),
            logoUrl: z.string().optional(),
            sortOrder: z.number(),
          }),
        ),
      }),
      annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: true },
    },
    async () => {
      try {
        const banks = (await client.listBanks()).map(shapeBank);
        const text = wrap(banks.length > 0 ? banks.map((b) => `${b.code} — ${b.name}`).join("\n") : "No active banks configured.");
        return { content: [{ type: "text", text }], structuredContent: { banks } };
      } catch (err) {
        return errorResult(err);
      }
    },
  );

  // charges:read

  if (scopes.has("charges:read")) {
    server.registerTool(
      "waffle_get_charge",
      {
        title: "Get a charge",
        description: "Look up a single charge by id — status, amounts, checkout details. 404s if the charge doesn't exist or doesn't belong to this merchant.",
        inputSchema: z.object({ id: idSchema.describe("Charge id, e.g. chg_...") }),
        outputSchema: chargeOutputSchema,
        annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: true },
      },
      async ({ id }) => {
        try {
          const charge = shapeCharge(await client.getCharge(id));
          return { content: [{ type: "text", text: wrap(chargeSummaryLines(charge).join("\n")) }], structuredContent: charge };
        } catch (err) {
          return errorResult(err);
        }
      },
    );

    server.registerTool(
      "waffle_list_charges",
      {
        title: "List charges",
        description: "List charges for this merchant, newest first, cursor-paginated. Filter by status or created-at range.",
        inputSchema: listChargesInputSchema,
        outputSchema: z.object({ data: z.array(chargeOutputSchema), hasMore: z.boolean() }),
        annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: true },
      },
      async (params) => {
        try {
          const listParams: ListChargesParams = {
            ...(params.limit !== undefined ? { limit: params.limit } : {}),
            ...(params.startingAfter !== undefined ? { startingAfter: params.startingAfter } : {}),
            ...(params.status !== undefined ? { status: params.status } : {}),
            ...(params.createdGte !== undefined ? { createdGte: params.createdGte } : {}),
            ...(params.createdLte !== undefined ? { createdLte: params.createdLte } : {}),
          };
          const page = await client.listCharges(listParams);
          const shaped = { data: page.data.map(shapeCharge), hasMore: page.hasMore === true };
          const text = wrap(
            shaped.data.length > 0
              ? `${shaped.data.length} charge(s)${shaped.hasMore ? " (more available)" : ""}:\n` +
                  shaped.data.map((c) => `${c.id} — ${c.status} — ${formatMoney(c.grossAmount, c.currency)}`).join("\n")
              : "No charges found.",
          );
          return { content: [{ type: "text", text }], structuredContent: shaped };
        } catch (err) {
          return errorResult(err);
        }
      },
    );

    server.registerTool(
      "waffle_get_charge_receipt",
      {
        title: "Get a charge's receipt",
        description:
          "Confirm a paid charge's receipt (bukti pembayaran PDF) is available and return the API path to it — this does NOT return the PDF bytes themselves, since a receipt isn't something a model should read. Hand the returned path to a human, or fetch it yourself against the configured base URL with the same API key to download the PDF. Fails with a 409 if the charge hasn't been paid yet.",
        inputSchema: z.object({ id: idSchema.describe("Charge id") }),
        outputSchema: z.object({ chargeId: z.string(), receiptPath: z.string() }),
        annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: true },
      },
      async ({ id }) => {
        try {
          // Fetching (and discarding) confirms the receipt exists and surfaces a
          // clean 409 "not paid yet" error rather than guessing from charge status.
          await client.getChargeReceipt(id);
          const receiptPath = `/v1/charges/${encodeURIComponent(id)}/receipt.pdf`;
          const text = `Receipt available for charge ${id}. Path: ${receiptPath} (fetch against the merchant API base URL with the same API key to download the PDF).`;
          return { content: [{ type: "text", text }], structuredContent: { chargeId: id, receiptPath } };
        } catch (err) {
          return errorResult(err);
        }
      },
    );

    server.registerTool(
      "waffle_calculate_fee",
      {
        title: "Preview a charge's fee",
        description:
          "Preview the fee Waffle would charge for a given amount — no charge is created, nothing is written to the ledger, no payment provider is called. Resolves against the same auto-routed provider a real create_charge call would use, so the preview always matches what a real charge would actually be billed.",
        inputSchema: z.object({
          amount: amountSchema.describe("Gross amount in the currency's minor unit, e.g. IDR 100000 means Rp100.000"),
          currency: currencySchema.describe('ISO 4217-ish currency code, 3 uppercase letters, e.g. "IDR"'),
        }),
        outputSchema: z.object({
          grossAmount: z.number(),
          feeAmount: z.number(),
          netAmount: z.number(),
          currency: z.string(),
        }),
        annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: true },
      },
      async ({ amount, currency }) => {
        try {
          const quote = shapeQuote(await client.calculateFee({ amount, currency }));
          const text = wrap(`Gross ${formatMoney(quote.grossAmount, quote.currency)}, fee ${formatMoney(quote.feeAmount, quote.currency)}, net ${formatMoney(quote.netAmount, quote.currency)}`);
          return { content: [{ type: "text", text }], structuredContent: quote };
        } catch (err) {
          return errorResult(err);
        }
      },
    );
  }

  // charges:write — only registered when the operator opted in via env.

  if (scopes.has("charges:write") && writesAllowed) {
    server.registerTool(
      "waffle_create_charge",
      {
        title: "Create a charge (payment link)",
        description:
          `Create a new charge for the given amount and currency — the payment-collection primitive behind a checkout link, QRIS code, or virtual-account number — against a ${mode.toUpperCase()} Waffle account. ${moneyNote} ${limitNote} There is no provider field: Waffle always auto-routes to the merchant's highest-priority connected PSP. ${retryRule} confirm: true is advisory and must only be set after the user asked for this exact charge.`,
        inputSchema: z.object({
          idempotencyKey: idempotencyKeySchema,
          amount: amountSchema.describe("Gross amount in the currency's minor unit, e.g. IDR 100000 means Rp100.000"),
          currency: currencySchema.describe('ISO 4217-ish currency code, 3 uppercase letters, e.g. "IDR"'),
          description: z.string().max(500).optional(),
          customerRef: z.string().max(100).optional().describe("Caller-defined reference to the paying customer, for the caller's own bookkeeping"),
          returnUrl: returnUrlSchema.optional().describe(
            `Where Waffle's hosted checkout (/pay/{id}) sends the payer once the charge is paid, failed or expired: a button to this URL with waffle_charge_id and waffle_status added (a hint only, never proof of payment), and on live charges a cancellable 5 second automatic redirect when the host is on the merchant's own domain. Must be https, no credentials${config.returnUrlAllowlist ? `; host must be one of: ${config.returnUrlAllowlist.join(", ")}` : ""}.`,
          ),
          returnLabel: z
            .string()
            .max(40)
            .regex(/^[^\p{Cc}\p{Cf}]*$/u, "returnLabel must not contain control characters")
            .optional()
            .describe('Text of the checkout page\'s return button (<= 40 chars). Requires returnUrl. Defaults to "Kembali ke {merchant name}".'),
          channel: channelSchema.optional().describe(
            'Selects a specific in-app payment method instead of the default redirect checkout. Requires vaBank when set to "virtual_account". Omit for the default redirect checkoutUrl flow.',
          ),
          vaBank: z.string().min(1).max(32).regex(/^[A-Za-z0-9_-]+$/).optional().describe('Bank code from waffle_list_banks; required only when channel is "virtual_account"'),
          expiresInMinutes: z.number().int().positive().max(Number.MAX_SAFE_INTEGER).optional(),
          metadata: metadataSchema.optional().describe("At most 20 keys; key <= 40 chars, value <= 200 chars. Never echoed back to you."),
          confirm: z
            .boolean()
            .describe(`Must be exactly true to create this charge in ${mode} mode; refused otherwise. Advisory: the server enforces its own limits regardless.`),
        }),
        outputSchema: chargeOutputSchema,
        annotations: { readOnlyHint: false, destructiveHint: false, idempotentHint: true, openWorldHint: true },
      },
      async (params) => {
        if (params.confirm !== true) {
          return refusalResult(
            "Refusing to create a charge without confirm: true. Pass confirm: true once you intend to actually create this charge.",
          );
        }
        if (params.amount > config.maxAmount) {
          return refusalResult(`Refused: amount ${params.amount} exceeds the per-call limit WAFFLE_MCP_MAX_AMOUNT (${config.maxAmount}).`);
        }
        if (params.returnLabel !== undefined && params.returnUrl === undefined) {
          return refusalResult("Refused: returnLabel requires returnUrl.");
        }
        if (params.channel === "virtual_account" && params.vaBank === undefined) {
          return refusalResult('Refused: vaBank is required when channel is "virtual_account".');
        }
        const { idempotencyKey, confirm: _confirm, ...args } = params;
        const begun = guard.begin(idempotencyKey, hashArgs("waffle_create_charge", args), params.amount, config.sessionMaxAmount);
        if ("error" in begun) return refusalResult(begun.error);
        try {
          const chargeParams: CreateChargeParams = {
            amount: params.amount,
            currency: params.currency,
            ...(params.description !== undefined ? { description: params.description } : {}),
            ...(params.customerRef !== undefined ? { customerRef: params.customerRef } : {}),
            ...(params.returnUrl !== undefined ? { returnUrl: new URL(params.returnUrl).href } : {}),
            ...(params.returnLabel !== undefined ? { returnLabel: params.returnLabel.trim() } : {}),
            ...(params.channel !== undefined ? { channel: params.channel } : {}),
            ...(params.vaBank !== undefined ? { vaBank: params.vaBank } : {}),
            ...(params.expiresInMinutes !== undefined ? { expiresInMinutes: params.expiresInMinutes } : {}),
            ...(params.metadata !== undefined ? { metadata: params.metadata } : {}),
          };
          const charge = shapeCharge(await client.createCharge(chargeParams, idempotencyKey));
          begun.settle("ok");
          return { content: [{ type: "text", text: wrap(chargeSummaryLines(charge).join("\n")) }], structuredContent: charge };
        } catch (err) {
          begun.settle(definitelyRejected(err) ? "rejected" : "unknown");
          return errorResult(err);
        }
      },
    );
  }

  // payouts:read

  if (scopes.has("payouts:read")) {
    server.registerTool(
      "waffle_get_payout",
      {
        title: "Get a payout",
        description: "Look up a single payout (withdrawal) by id — status, amount, destination bank account. 404s if it doesn't exist or doesn't belong to this merchant.",
        inputSchema: z.object({ id: idSchema.describe("Payout id, e.g. po_...") }),
        outputSchema: payoutOutputSchema,
        annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: true },
      },
      async ({ id }) => {
        try {
          const payout = shapePayout(await client.getPayout(id));
          return { content: [{ type: "text", text: wrap(payoutSummaryLines(payout).join("\n")) }], structuredContent: payout };
        } catch (err) {
          return errorResult(err);
        }
      },
    );

    server.registerTool(
      "waffle_list_payouts",
      {
        title: "List payouts",
        description: "List payouts (withdrawals) for this merchant, newest first, cursor-paginated. Filter by status or created-at range.",
        inputSchema: listPayoutsInputSchema,
        outputSchema: z.object({ data: z.array(payoutOutputSchema), hasMore: z.boolean() }),
        annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: true },
      },
      async (params) => {
        try {
          const listParams: ListPayoutsParams = {
            ...(params.limit !== undefined ? { limit: params.limit } : {}),
            ...(params.startingAfter !== undefined ? { startingAfter: params.startingAfter } : {}),
            ...(params.status !== undefined ? { status: params.status } : {}),
            ...(params.createdGte !== undefined ? { createdGte: params.createdGte } : {}),
            ...(params.createdLte !== undefined ? { createdLte: params.createdLte } : {}),
          };
          const page = await client.listPayouts(listParams);
          const shaped = { data: page.data.map(shapePayout), hasMore: page.hasMore === true };
          const text = wrap(
            shaped.data.length > 0
              ? `${shaped.data.length} payout(s)${shaped.hasMore ? " (more available)" : ""}:\n` +
                  shaped.data.map((p) => `${p.id} — ${p.status} — ${formatMoney(p.amount, p.currency)}`).join("\n")
              : "No payouts found.",
          );
          return { content: [{ type: "text", text }], structuredContent: shaped };
        } catch (err) {
          return errorResult(err);
        }
      },
    );

    server.registerTool(
      "waffle_get_bank_account",
      {
        title: "Get active withdrawal bank account",
        description:
          "Get this API key's mode's current active withdrawal bank account, if one has ever been registered. Registration itself is dashboard-only, never through this server or any API key — a leaked key can never redirect where funds go.",
        outputSchema: bankAccountOutputSchema,
        annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: true },
      },
      async () => {
        try {
          const account = shapeBankAccount(await client.getBankAccount());
          const text = wrap(`${account.bankCode} ${account.accountNumber} (${account.accountHolderName}), id ${account.id}`);
          return { content: [{ type: "text", text }], structuredContent: account };
        } catch (err) {
          if (err instanceof WaffleError && err.status === 404) {
            return {
              content: [
                {
                  type: "text",
                  text: "No withdrawal bank account registered yet for this mode. Register one from the Waffle dashboard (Payouts → Bank accounts) — this is deliberately not something an API key or this tool can do.",
                },
              ],
              isError: true,
            };
          }
          return errorResult(err);
        }
      },
    );
  }

  // payouts:write — only registered when the operator opted in via env.

  if (scopes.has("payouts:write") && writesAllowed) {
    server.registerTool(
      "waffle_create_payout",
      {
        title: "Withdraw to bank account",
        description:
          `Withdraw funds for the given amount and currency from the merchant's balance to their registered bank account, against a ${mode.toUpperCase()} Waffle account. ${moneyNote} ${limitNote} ${retryRule} Fails with a 422 "insufficient available balance" error if amount exceeds waffle_get_balance's withdrawable amount. confirm: true is advisory and must only be set after the user asked for this exact withdrawal.`,
        inputSchema: z.object({
          idempotencyKey: idempotencyKeySchema,
          bankAccountId: idSchema.describe("id from waffle_get_bank_account"),
          amount: amountSchema.describe("Amount in the currency's minor unit"),
          currency: currencySchema,
          confirm: z
            .boolean()
            .describe(`Must be exactly true to create this payout in ${mode} mode; refused otherwise. Advisory: the server enforces its own limits regardless.`),
        }),
        outputSchema: payoutOutputSchema,
        annotations: { readOnlyHint: false, destructiveHint: true, idempotentHint: true, openWorldHint: true },
      },
      async (params) => {
        if (params.confirm !== true) {
          return refusalResult(
            "Refusing to create a payout without confirm: true. Pass confirm: true once you intend to actually withdraw funds.",
          );
        }
        if (params.amount > config.maxAmount) {
          return refusalResult(`Refused: amount ${params.amount} exceeds the per-call limit WAFFLE_MCP_MAX_AMOUNT (${config.maxAmount}).`);
        }
        const { idempotencyKey, confirm: _confirm, ...args } = params;
        const begun = guard.begin(idempotencyKey, hashArgs("waffle_create_payout", args), params.amount, config.sessionMaxAmount);
        if ("error" in begun) return refusalResult(begun.error);
        try {
          const payoutParams: CreatePayoutParams = {
            bankAccountId: params.bankAccountId,
            amount: params.amount,
            currency: params.currency,
          };
          const payout = shapePayout(await client.createPayout(payoutParams, idempotencyKey));
          begun.settle("ok");
          return { content: [{ type: "text", text: wrap(payoutSummaryLines(payout).join("\n")) }], structuredContent: payout };
        } catch (err) {
          begun.settle(definitelyRejected(err) ? "rejected" : "unknown");
          return errorResult(err);
        }
      },
    );
  }

  // balance:read

  if (scopes.has("balance:read")) {
    server.registerTool(
      "waffle_get_balance",
      {
        title: "Get withdrawable balance",
        description:
          'Get the merchant\'s withdrawable balance for this API key\'s mode: settled paid charges minus non-failed payouts. A charge inside its settlement hold window does not count yet even if its status is "paid" — this is not the same as total lifetime revenue.',
        inputSchema: z.object({
          currency: currencySchema.optional().describe('3 uppercase letters; defaults server-side to "IDR" when omitted'),
        }),
        outputSchema: z.object({ currency: z.string(), amount: z.number() }),
        annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: true },
      },
      async ({ currency }) => {
        try {
          const raw = await client.getBalance(currency);
          const balance = { currency: sanitizeText(raw.currency, 16), amount: num(raw.amount) };
          return {
            content: [{ type: "text", text: wrap(`Withdrawable balance: ${formatMoney(balance.amount, balance.currency)}`) }],
            structuredContent: balance,
          };
        } catch (err) {
          return errorResult(err);
        }
      },
    );
  }

  return server;
}
