import { McpServer } from "@modelcontextprotocol/server";
import {
  PaybridgeClient,
  PaybridgeError,
  generateIdempotencyKey,
  type CreateChargeParams,
} from "@paybridge/sdk";
import * as z from "zod/v4";
import { formatMoney } from "./format.js";

/** Options for {@link createServer}. */
export interface PaybridgeMcpServerOptions {
  /** Merchant API key. Its mode (live vs sandbox) is fixed at issuance server-side — a sandbox key can never move live money no matter what a tool call asks for. */
  apiKey: string;
  /** Merchant API base URL. Defaults to `PaybridgeClient`'s own default (`http://localhost:8080`). */
  baseUrl?: string;
  /** Injectable `fetch`, forwarded to `PaybridgeClient` — used by tests. */
  fetch?: typeof fetch;
}

const modeSchema = z.enum(["live", "sandbox"]);
const channelSchema = z.enum(["qris", "virtual_account"]);
const metadataSchema = z.record(z.string(), z.string());

const chargeOutputSchema = z.object({
  id: z.string(),
  mode: modeSchema,
  status: z.enum(["pending", "paid", "failed", "expired"]),
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
  status: z.enum(["pending", "held", "processing", "completed", "failed"]),
  amount: z.number(),
  currency: z.string(),
  failureReason: z.string().optional(),
});

/** An ordinary tool-error result: content the model reads and can react to, never a thrown protocol error. */
function errorResult(err: unknown): { content: [{ type: "text"; text: string }]; isError: true } {
  const text =
    err instanceof PaybridgeError
      ? `Paybridge API error (HTTP ${err.status}): ${err.message}`
      : err instanceof Error
        ? err.message
        : String(err);
  return { content: [{ type: "text", text }], isError: true };
}

/**
 * Builds an MCP server exposing the Paybridge **merchant API** (`:8080`,
 * API-key authenticated) as tools — the same surface `@paybridge/sdk`
 * wraps, not the admin API or the dashboard-session-authenticated
 * `/v1/merchants/me/*` routes. Every write tool that requires an
 * `Idempotency-Key` (create_charge, create_payout) mints its own per
 * call, since an LLM caller has no retry state of its own to key
 * against — calling the tool twice creates two separate resources, the
 * same choice the merchant dashboard's own quick-charge/withdrawal forms
 * make for the same reason (see `internal/httpapi/merchant_me.go`'s
 * `quickChargeRequest` doc comment).
 */
export function createServer(options: PaybridgeMcpServerOptions): McpServer {
  const client = new PaybridgeClient({
    apiKey: options.apiKey,
    ...(options.baseUrl !== undefined ? { baseUrl: options.baseUrl } : {}),
    ...(options.fetch !== undefined ? { fetch: options.fetch } : {}),
  });

  const server = new McpServer({ name: "paybridge", version: "0.1.0" });

  server.registerTool(
    "paybridge_list_banks",
    {
      title: "List supported banks",
      description:
        "List every active bank Paybridge supports for virtual-account charges and withdrawal bank accounts (code, display name, sort order). Bank codes are not fixed — call this before register_bank_account or before create_charge with channel \"virtual_account\" to get a valid bank code, rather than guessing or hardcoding one.",
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

  server.registerTool(
    "paybridge_calculate_fee",
    {
      title: "Preview a charge's fee",
      description:
        "Preview the fee Paybridge would charge for a given amount — no charge is created, nothing is written to the ledger, no payment provider is called. Resolves against the same auto-routed provider a real create_charge call would use, so the preview always matches what a real charge would actually be billed.",
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

  server.registerTool(
    "paybridge_create_charge",
    {
      title: "Create a charge (payment link)",
      description:
        "Create a new charge — the payment-collection primitive behind a checkout link, QRIS code, or virtual-account number. There is no provider field: Paybridge always auto-routes to the merchant's highest-priority connected PSP, and which PSP handled it is never surfaced. Mints its own Idempotency-Key per call, so calling this tool twice creates two separate charges, never a retried duplicate. A live-mode API key moves real money the instant a payer pays; a sandbox-mode key never can, regardless of these arguments — mode is fixed server-side by which key was supplied to this server, not by anything passed here.",
      inputSchema: z.object({
        amount: z.number().int().positive().describe("Gross amount in the currency's minor unit, e.g. IDR 100000 means Rp100.000"),
        currency: z.string().min(1).describe('ISO 4217-ish currency code, e.g. "IDR"'),
        description: z.string().optional(),
        customerRef: z.string().optional().describe("Caller-defined reference to the paying customer, for the caller's own bookkeeping"),
        returnUrl: z.string().optional().describe("Where the payer's browser returns after a redirect-flow checkout"),
        channel: channelSchema.optional().describe(
          'Selects a specific in-app payment method instead of the default redirect checkout. Requires vaBank when set to "virtual_account". Omit for the default redirect checkoutUrl flow.',
        ),
        vaBank: z.string().optional().describe('Bank code from paybridge_list_banks; required only when channel is "virtual_account"'),
        expiresInMinutes: z.number().int().positive().optional(),
        metadata: metadataSchema.optional(),
      }),
      outputSchema: chargeOutputSchema,
      annotations: { readOnlyHint: false, destructiveHint: false, idempotentHint: false, openWorldHint: true },
    },
    async (params) => {
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
        const lines = [
          `Charge ${charge.id} (${charge.mode}) — status ${charge.status}`,
          `Gross ${formatMoney(charge.grossAmount, charge.currency)}, fee ${formatMoney(charge.feeAmount, charge.currency)}, net ${formatMoney(charge.netAmount, charge.currency)}`,
        ];
        if (charge.checkoutUrl) lines.push(`Checkout: ${charge.checkoutUrl}`);
        if (charge.qrString) lines.push(`QRIS payload: ${charge.qrString}`);
        if (charge.vaBank && charge.vaNumber) lines.push(`Virtual account: ${charge.vaBank} ${charge.vaNumber}`);
        return { content: [{ type: "text", text: lines.join("\n") }], structuredContent: charge };
      } catch (err) {
        return errorResult(err);
      }
    },
  );

  server.registerTool(
    "paybridge_register_bank_account",
    {
      title: "Register withdrawal bank account",
      description:
        "Register (or replace) the merchant's active withdrawal bank account for this API key's mode. Registering a new account disables any prior active one for that mode — only one active withdrawal destination per mode at a time — and a payout drawn against an account within 6 hours of it becoming active is held rather than dispatched, as a security measure against a hijacked key redirecting withdrawals. Call paybridge_list_banks first for a valid bank code.",
      inputSchema: z.object({
        bankCode: z.string().min(1).describe('Bank code from paybridge_list_banks, e.g. "BCA"'),
        accountNumber: z.string().min(1),
        accountHolderName: z.string().min(1),
      }),
      outputSchema: bankAccountOutputSchema,
      annotations: { readOnlyHint: false, destructiveHint: true, idempotentHint: false, openWorldHint: true },
    },
    async (params) => {
      try {
        const account = await client.registerBankAccount(params);
        const text = `Registered ${account.bankCode} ${account.accountNumber} (${account.accountHolderName}) as id ${account.id}. Any prior active account for this mode is now disabled; a payout drawn against this one within 6h is held, not dispatched.`;
        return { content: [{ type: "text", text }], structuredContent: account };
      } catch (err) {
        return errorResult(err);
      }
    },
  );

  server.registerTool(
    "paybridge_get_bank_account",
    {
      title: "Get active withdrawal bank account",
      description: "Get this API key's mode's current active withdrawal bank account, if one has ever been registered.",
      outputSchema: bankAccountOutputSchema,
      annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: true },
    },
    async () => {
      try {
        const account = await client.getActiveBankAccount();
        const text = `${account.bankCode} ${account.accountNumber} (${account.accountHolderName}), id ${account.id}`;
        return { content: [{ type: "text", text }], structuredContent: account };
      } catch (err) {
        if (err instanceof PaybridgeError && err.status === 404) {
          return {
            content: [{ type: "text", text: "No withdrawal bank account registered yet for this mode. Use paybridge_register_bank_account first." }],
            isError: true,
          };
        }
        return errorResult(err);
      }
    },
  );

  server.registerTool(
    "paybridge_create_payout",
    {
      title: "Withdraw to bank account",
      description:
        "Withdraw funds from the merchant's Paybridge balance to their registered bank account. Moves real money on a live-mode key; a sandbox-mode key never can, regardless of these arguments. Mints its own Idempotency-Key per call, so calling this tool twice creates two separate payouts. Fails with a 422 \"insufficient available balance\" error if amount exceeds paybridge_get_balance's withdrawable amount.",
      inputSchema: z.object({
        bankAccountId: z.string().min(1).describe("id from paybridge_register_bank_account or paybridge_get_bank_account"),
        amount: z.number().int().positive().describe("Amount in the currency's minor unit"),
        currency: z.string().min(1),
      }),
      outputSchema: payoutOutputSchema,
      annotations: { readOnlyHint: false, destructiveHint: true, idempotentHint: false, openWorldHint: true },
    },
    async (params) => {
      try {
        const payout = await client.createPayout(params, generateIdempotencyKey());
        const heldNote =
          payout.status === "held"
            ? " (held: the bank account was registered within the last 6h; Paybridge dispatches it automatically once the hold window passes — no action needed)"
            : "";
        const text = `Payout ${payout.id} (${payout.mode}) — status ${payout.status}${heldNote}. Amount ${formatMoney(payout.amount, payout.currency)}.`;
        return { content: [{ type: "text", text }], structuredContent: payout };
      } catch (err) {
        return errorResult(err);
      }
    },
  );

  server.registerTool(
    "paybridge_get_balance",
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

  server.registerTool(
    "paybridge_healthz",
    {
      title: "Check API health",
      description: "Check whether the configured Paybridge merchant API base URL is reachable and healthy. No auth required.",
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

  return server;
}
