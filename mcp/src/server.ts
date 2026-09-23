import { McpServer } from "@modelcontextprotocol/server";
import {
  WaffleClient,
  WaffleError,
  generateIdempotencyKey,
  type Charge,
  type CreateChargeParams,
  type CreatePayoutParams,
  type ListChargesParams,
  type ListPayoutsParams,
  type Payout,
  type Scope,
  type WhoAmI,
} from "@waffle/sdk";
import * as z from "zod/v4";
import { formatMoney } from "./format.js";

/** Options for {@link createServer}. */
export interface WaffleMcpServerOptions {
  /** Merchant API key. Its mode (live vs sandbox) is fixed at issuance server-side — a sandbox key can never move live money no matter what a tool call asks for. */
  apiKey: string;
  /** Merchant API base URL. Defaults to `WaffleClient`'s own default (`https://api.getwaffle.id`, production). */
  baseUrl?: string;
  /** Injectable `fetch`, forwarded to `WaffleClient` — used by tests. */
  fetch?: typeof fetch;
}

const modeSchema = z.enum(["live", "sandbox"]);
const channelSchema = z.enum(["qris", "virtual_account"]);
const metadataSchema = z.record(z.string(), z.string());
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
  metadata: metadataSchema.optional(),
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

const listChargesInputSchema = z.object({
  limit: z.number().int().positive().max(100).optional().describe("Default 20, max 100"),
  startingAfter: z.string().optional().describe("Cursor: the id of the last charge from a previous page"),
  status: chargeStatusSchema.optional(),
  createdGte: z.string().optional().describe("RFC3339 or YYYY-MM-DD"),
  createdLte: z.string().optional().describe("RFC3339 or YYYY-MM-DD"),
});

const listPayoutsInputSchema = z.object({
  limit: z.number().int().positive().max(100).optional().describe("Default 20, max 100"),
  startingAfter: z.string().optional().describe("Cursor: the id of the last payout from a previous page"),
  status: payoutStatusSchema.optional(),
  createdGte: z.string().optional().describe("RFC3339 or YYYY-MM-DD"),
  createdLte: z.string().optional().describe("RFC3339 or YYYY-MM-DD"),
});

/** An ordinary tool-error result: content the model reads and can react to, never a thrown protocol error. */
function errorResult(err: unknown): { content: [{ type: "text"; text: string }]; isError: true } {
  const text =
    err instanceof WaffleError
      ? `Waffle API error (HTTP ${err.status}): ${err.message}`
      : err instanceof Error
        ? err.message
        : String(err);
  return { content: [{ type: "text", text }], isError: true };
}

/** A hand-built tool-error result for a rejection that never touches the API (e.g. a missing `confirm: true`). */
function refusalResult(text: string): { content: [{ type: "text"; text: string }]; isError: true } {
  return { content: [{ type: "text", text }], isError: true };
}

/** Human-readable summary lines shared by create_charge and get_charge. */
function chargeSummaryLines(charge: Charge): string[] {
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

/** Human-readable summary lines shared by create_payout and get_payout. */
function payoutSummaryLines(payout: Payout): string[] {
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

/**
 * Builds an MCP server exposing the Waffle **merchant API** (`:8080`,
 * API-key authenticated) as tools — the same surface `@waffle/sdk`
 * wraps, not the admin API or the dashboard-session-authenticated
 * `/v1/merchants/me/*` routes. Every write tool that requires an
 * `Idempotency-Key` (create_charge, create_payout) mints its own per
 * call, since an LLM caller has no retry state of its own to key
 * against — calling the tool twice creates two separate resources, the
 * same choice the merchant dashboard's own quick-charge/withdrawal forms
 * make for the same reason (see `internal/httpapi/merchant_me.go`'s
 * `quickChargeRequest` doc comment).
 *
 * On start, calls `whoami()` once with the configured key and registers
 * **only** the tools that key's scopes allow — a `read_only` key never
 * even sees `create_charge`/`create_payout` as callable tools, rather
 * than seeing them and getting a 403 at call time. If `whoami()` fails
 * (bad key, unreachable API), this rejects — callers must fail loudly at
 * startup rather than silently serve zero or all tools.
 */
export async function createServer(options: WaffleMcpServerOptions): Promise<McpServer> {
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
  const moneyNote = mode === "live" ? "Moves real money once confirmed." : "Sandbox mode — never moves real money, safe to use freely.";

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
        const result = await client.whoami();
        const text = `${result.businessName} (${result.merchantId}) — mode ${result.mode}, preset ${result.preset}, scopes: ${result.scopes.join(", ") || "none"}`;
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
        const banks = await client.listBanks();
        const text = banks.length > 0 ? banks.map((b) => `${b.code} — ${b.name}`).join("\n") : "No active banks configured.";
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
        inputSchema: z.object({ id: z.string().min(1).describe("Charge id, e.g. chg_...") }),
        outputSchema: chargeOutputSchema,
        annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: true },
      },
      async ({ id }) => {
        try {
          const charge = await client.getCharge(id);
          return { content: [{ type: "text", text: chargeSummaryLines(charge).join("\n") }], structuredContent: charge };
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
          const text =
            page.data.length > 0
              ? `${page.data.length} charge(s)${page.hasMore ? " (more available)" : ""}:\n` +
                page.data.map((c) => `${c.id} — ${c.status} — ${formatMoney(c.grossAmount, c.currency)}`).join("\n")
              : "No charges found.";
          return { content: [{ type: "text", text }], structuredContent: page };
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
        inputSchema: z.object({ id: z.string().min(1).describe("Charge id") }),
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
          amount: z.number().int().positive().describe("Gross amount in the currency's minor unit, e.g. IDR 100000 means Rp100.000"),
          currency: z.string().min(1).describe('ISO 4217-ish currency code, e.g. "IDR"'),
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
          const quote = await client.calculateFee({ amount, currency });
          const text = `Gross ${formatMoney(quote.grossAmount, quote.currency)}, fee ${formatMoney(quote.feeAmount, quote.currency)}, net ${formatMoney(quote.netAmount, quote.currency)}`;
          return { content: [{ type: "text", text }], structuredContent: quote };
        } catch (err) {
          return errorResult(err);
        }
      },
    );
  }

  // charges:write

  if (scopes.has("charges:write")) {
    server.registerTool(
      "waffle_create_charge",
      {
        title: "Create a charge (payment link)",
        description:
          `Create a new charge for the given amount and currency — the payment-collection primitive behind a checkout link, QRIS code, or virtual-account number — against a ${mode.toUpperCase()} Waffle account. ${moneyNote} There is no provider field: Waffle always auto-routes to the merchant's highest-priority connected PSP, and which PSP handled it is never surfaced. Mints its own Idempotency-Key per call, so calling this tool twice creates two separate charges, never a retried duplicate. Requires confirm: true — the call is rejected before any request is sent otherwise.`,
        inputSchema: z.object({
          amount: z.number().int().positive().describe("Gross amount in the currency's minor unit, e.g. IDR 100000 means Rp100.000"),
          currency: z.string().min(1).describe('ISO 4217-ish currency code, e.g. "IDR"'),
          description: z.string().optional(),
          customerRef: z.string().optional().describe("Caller-defined reference to the paying customer, for the caller's own bookkeeping"),
          returnUrl: z.string().optional().describe("Where the payer's browser returns after a redirect-flow checkout"),
          channel: channelSchema.optional().describe(
            'Selects a specific in-app payment method instead of the default redirect checkout. Requires vaBank when set to "virtual_account". Omit for the default redirect checkoutUrl flow.',
          ),
          vaBank: z.string().optional().describe('Bank code from waffle_list_banks; required only when channel is "virtual_account"'),
          expiresInMinutes: z.number().int().positive().optional(),
          metadata: metadataSchema.optional(),
          confirm: z
            .boolean()
            .describe(
              `Must be exactly true to actually create this charge and move money in ${mode} mode. The call is refused with false or if omitted.`,
            ),
        }),
        outputSchema: chargeOutputSchema,
        annotations: { readOnlyHint: false, destructiveHint: false, idempotentHint: false, openWorldHint: true },
      },
      async (params) => {
        if (params.confirm !== true) {
          return refusalResult(
            "Refusing to create a charge without confirm: true. Pass confirm: true once you intend to actually create this charge.",
          );
        }
        try {
          const chargeParams: CreateChargeParams = {
            amount: params.amount,
            currency: params.currency,
            ...(params.description !== undefined ? { description: params.description } : {}),
            ...(params.customerRef !== undefined ? { customerRef: params.customerRef } : {}),
            ...(params.returnUrl !== undefined ? { returnUrl: params.returnUrl } : {}),
            ...(params.channel !== undefined ? { channel: params.channel } : {}),
            ...(params.vaBank !== undefined ? { vaBank: params.vaBank } : {}),
            ...(params.expiresInMinutes !== undefined ? { expiresInMinutes: params.expiresInMinutes } : {}),
            ...(params.metadata !== undefined ? { metadata: params.metadata } : {}),
          };
          const charge = await client.createCharge(chargeParams, generateIdempotencyKey());
          return { content: [{ type: "text", text: chargeSummaryLines(charge).join("\n") }], structuredContent: charge };
        } catch (err) {
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
        inputSchema: z.object({ id: z.string().min(1).describe("Payout id, e.g. po_...") }),
        outputSchema: payoutOutputSchema,
        annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: true },
      },
      async ({ id }) => {
        try {
          const payout = await client.getPayout(id);
          return { content: [{ type: "text", text: payoutSummaryLines(payout).join("\n") }], structuredContent: payout };
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
          const text =
            page.data.length > 0
              ? `${page.data.length} payout(s)${page.hasMore ? " (more available)" : ""}:\n` +
                page.data.map((p) => `${p.id} — ${p.status} — ${formatMoney(p.amount, p.currency)}`).join("\n")
              : "No payouts found.";
          return { content: [{ type: "text", text }], structuredContent: page };
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
          const account = await client.getBankAccount();
          const text = `${account.bankCode} ${account.accountNumber} (${account.accountHolderName}), id ${account.id}`;
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

  // payouts:write

  if (scopes.has("payouts:write")) {
    server.registerTool(
      "waffle_create_payout",
      {
        title: "Withdraw to bank account",
        description:
          `Withdraw funds for the given amount and currency from the merchant's balance to their registered bank account, against a ${mode.toUpperCase()} Waffle account. ${moneyNote} Mints its own Idempotency-Key per call, so calling this tool twice creates two separate payouts. Fails with a 422 "insufficient available balance" error if amount exceeds waffle_get_balance's withdrawable amount. Requires confirm: true — the call is rejected before any request is sent otherwise.`,
        inputSchema: z.object({
          bankAccountId: z.string().min(1).describe("id from waffle_get_bank_account"),
          amount: z.number().int().positive().describe("Amount in the currency's minor unit"),
          currency: z.string().min(1),
          confirm: z
            .boolean()
            .describe(
              `Must be exactly true to actually create this payout and move money in ${mode} mode. The call is refused with false or if omitted.`,
            ),
        }),
        outputSchema: payoutOutputSchema,
        annotations: { readOnlyHint: false, destructiveHint: true, idempotentHint: false, openWorldHint: true },
      },
      async (params) => {
        if (params.confirm !== true) {
          return refusalResult(
            "Refusing to create a payout without confirm: true. Pass confirm: true once you intend to actually withdraw funds.",
          );
        }
        try {
          const payoutParams: CreatePayoutParams = {
            bankAccountId: params.bankAccountId,
            amount: params.amount,
            currency: params.currency,
          };
          const payout = await client.createPayout(payoutParams, generateIdempotencyKey());
          return { content: [{ type: "text", text: payoutSummaryLines(payout).join("\n") }], structuredContent: payout };
        } catch (err) {
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
          currency: z.string().min(1).optional().describe('Defaults server-side to "IDR" when omitted'),
        }),
        outputSchema: z.object({ currency: z.string(), amount: z.number() }),
        annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: true },
      },
      async ({ currency }) => {
        try {
          const balance = await client.getBalance(currency);
          return {
            content: [{ type: "text", text: `Withdrawable balance: ${formatMoney(balance.amount, balance.currency)}` }],
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
