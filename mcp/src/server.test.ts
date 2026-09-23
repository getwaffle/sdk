import { afterEach, describe, expect, it, vi, type Mock } from "vitest";
import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";
import { createMcpHandler } from "@modelcontextprotocol/server";
import type { Scope } from "@waffle/sdk";
import { createServer } from "./server.js";

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function textResponse(status: number, body: string): Response {
  return new Response(body, { status });
}

function whoamiBody(scopes: Scope[], mode: "live" | "sandbox" = "sandbox") {
  const preset =
    scopes.length === 5
      ? "full"
      : scopes.includes("charges:write")
        ? "accept_payments"
        : "read_only";
  return {
    merchant_id: "mer_1",
    business_name: "Test Merchant",
    mode,
    preset,
    scopes,
  };
}

const FULL_SCOPES: Scope[] = ["charges:read", "charges:write", "payouts:read", "payouts:write", "balance:read"];
const READ_ONLY_SCOPES: Scope[] = ["charges:read", "payouts:read", "balance:read"];

describe("waffle MCP server", () => {
  let cleanups: Array<() => Promise<void>> = [];

  afterEach(async () => {
    for (const cleanup of cleanups) await cleanup();
    cleanups = [];
  });

  /**
   * Connects a fresh MCP client against a server built for a key with the
   * given scopes. `fetchMock` always answers `GET .../v1/whoami` (the
   * startup scope-gating call, and the `waffle_whoami` tool itself) from
   * `scopes`/`mode` regardless of how many times it's called; every other
   * URL is served from a FIFO queue that test bodies push onto via
   * `mockNext`. `toolCalls()` returns only the non-whoami fetch calls, in
   * order, so assertions don't have to account for the startup call.
   */
  async function connect(scopes: Scope[], mode: "live" | "sandbox" = "sandbox") {
    const queue: Response[] = [];
    const fetchMock: Mock = vi.fn(async (url: string) => {
      if (typeof url === "string" && url.endsWith("/v1/whoami")) {
        return jsonResponse(200, whoamiBody(scopes, mode));
      }
      const next = queue.shift();
      if (!next) throw new Error(`fetchMock: no response queued for ${String(url)}`);
      return next;
    });

    const handler = createMcpHandler(() =>
      createServer({ apiKey: "sk_test_123", baseUrl: "http://waffle.test", fetch: fetchMock as unknown as typeof fetch }),
    );
    const transport = new StreamableHTTPClientTransport(new URL("http://test.local/mcp"), {
      fetch: (url, init) => handler.fetch(new Request(url, init)),
    });
    const client = new Client({ name: "test-harness", version: "1.0.0" }, { versionNegotiation: { mode: "auto" } });
    await client.connect(transport);

    cleanups.push(async () => {
      await client.close();
      await handler.close();
    });

    return {
      client,
      fetchMock,
      mockNext: (res: Response) => queue.push(res),
      toolCalls: () => fetchMock.mock.calls.filter(([url]) => !(typeof url === "string" && url.endsWith("/v1/whoami"))),
    };
  }

  it("startup fails loudly when whoami() rejects, instead of registering zero or all tools", async () => {
    const badFetch = vi.fn().mockResolvedValue(jsonResponse(401, { error: "invalid API key" }));
    await expect(
      createServer({ apiKey: "sk_bad", baseUrl: "http://waffle.test", fetch: badFetch as unknown as typeof fetch }),
    ).rejects.toThrow(/whoami/i);
  });

  it("registers every tool for a full-scope key", async () => {
    const { client } = await connect(FULL_SCOPES);
    const { tools } = await client.listTools();
    const names = tools.map((t) => t.name).sort();
    expect(names).toEqual(
      [
        "waffle_calculate_fee",
        "waffle_create_charge",
        "waffle_create_payout",
        "waffle_get_balance",
        "waffle_get_bank_account",
        "waffle_get_charge",
        "waffle_get_charge_receipt",
        "waffle_get_payout",
        "waffle_healthz",
        "waffle_list_banks",
        "waffle_list_charges",
        "waffle_list_payouts",
        "waffle_whoami",
      ].sort(),
    );
    // register_bank_account no longer exists — the route it targeted was removed server-side.
    expect(names).not.toContain("waffle_register_bank_account");
  });

  it("registers only the tools a read_only key's scopes allow, never the money-moving ones", async () => {
    const { client } = await connect(READ_ONLY_SCOPES);
    const { tools } = await client.listTools();
    const names = tools.map((t) => t.name).sort();
    expect(names).toEqual(
      [
        "waffle_calculate_fee",
        "waffle_get_balance",
        "waffle_get_bank_account",
        "waffle_get_charge",
        "waffle_get_charge_receipt",
        "waffle_get_payout",
        "waffle_healthz",
        "waffle_list_banks",
        "waffle_list_charges",
        "waffle_list_payouts",
        "waffle_whoami",
      ].sort(),
    );
    expect(names).not.toContain("waffle_create_charge");
    expect(names).not.toContain("waffle_create_payout");
  });

  it("registers create_charge but not create_payout for an accept_payments-shaped key", async () => {
    const { client } = await connect(["charges:read", "charges:write", "balance:read"]);
    const { tools } = await client.listTools();
    const names = tools.map((t) => t.name);
    expect(names).toContain("waffle_create_charge");
    expect(names).not.toContain("waffle_create_payout");
    expect(names).not.toContain("waffle_get_bank_account");
    expect(names).not.toContain("waffle_get_payout");
    expect(names).not.toContain("waffle_list_payouts");
  });

  it("creates a charge, mints its own idempotency key, and returns structured content", async () => {
    const { client, mockNext, toolCalls } = await connect(FULL_SCOPES);
    mockNext(
      jsonResponse(201, {
        id: "chg_1",
        mode: "sandbox",
        status: "pending",
        gross_amount: 100000,
        fee_amount: 3000,
        net_amount: 97000,
        currency: "IDR",
        checkout_url: "https://pay.example/chg_1",
        created_at: "2026-09-14T00:00:00Z",
      }),
    );

    const result = await client.callTool({
      name: "waffle_create_charge",
      arguments: { amount: 100000, currency: "IDR", description: "Order #1", confirm: true },
    });

    const [url, init] = toolCalls()[0] as [string, RequestInit];
    expect(url).toBe("http://waffle.test/v1/charges");
    const headers = init.headers as Record<string, string>;
    expect(headers["Idempotency-Key"]).toBeTruthy();
    expect(JSON.parse(init.body as string)).not.toHaveProperty("provider");
    expect(JSON.parse(init.body as string)).not.toHaveProperty("confirm");

    expect(result.isError).toBeFalsy();
    expect(result.structuredContent).toMatchObject({ id: "chg_1", status: "pending", grossAmount: 100000 });
    expect((result.content as Array<{ type: string; text: string }>)[0]?.text).toContain("Rp100.000");
  });

  it("refuses to create a charge when confirm is false, without calling the API", async () => {
    const { client, toolCalls } = await connect(FULL_SCOPES);

    const result = await client.callTool({
      name: "waffle_create_charge",
      arguments: { amount: 100000, currency: "IDR", confirm: false },
    });

    expect(result.isError).toBe(true);
    const text = (result.content as Array<{ type: string; text: string }>)[0]?.text ?? "";
    expect(text.toLowerCase()).toContain("confirm");
    expect(toolCalls()).toHaveLength(0);
  });

  it("refuses to create a charge when confirm is omitted entirely", async () => {
    const { client, toolCalls } = await connect(FULL_SCOPES);

    const result = await client.callTool({
      name: "waffle_create_charge",
      arguments: { amount: 100000, currency: "IDR" },
    });

    expect(result.isError).toBe(true);
    expect(toolCalls()).toHaveLength(0);
  });

  it("refuses to create a payout when confirm is not exactly true", async () => {
    const { client, toolCalls } = await connect(FULL_SCOPES);

    const result = await client.callTool({
      name: "waffle_create_payout",
      arguments: { bankAccountId: "ba_1", amount: 50000, currency: "IDR", confirm: false },
    });

    expect(result.isError).toBe(true);
    const text = (result.content as Array<{ type: string; text: string }>)[0]?.text ?? "";
    expect(text.toLowerCase()).toContain("confirm");
    expect(toolCalls()).toHaveLength(0);
  });

  it("bakes the key's mode into the create_charge and create_payout tool descriptions", async () => {
    const { client } = await connect(FULL_SCOPES, "sandbox");
    const { tools } = await client.listTools();
    const createCharge = tools.find((t) => t.name === "waffle_create_charge");
    const createPayout = tools.find((t) => t.name === "waffle_create_payout");
    expect(createCharge?.description ?? "").toContain("SANDBOX");
    expect(createPayout?.description ?? "").toContain("SANDBOX");
  });

  it("mints a fresh idempotency key on every create_charge call", async () => {
    const { client, mockNext, toolCalls } = await connect(FULL_SCOPES);
    const chargeResponse = () =>
      jsonResponse(201, {
        id: "chg_2",
        mode: "sandbox",
        status: "pending",
        gross_amount: 50000,
        fee_amount: 1500,
        net_amount: 48500,
        currency: "IDR",
        created_at: "2026-09-14T00:00:00Z",
      });
    mockNext(chargeResponse());
    mockNext(chargeResponse());

    await client.callTool({ name: "waffle_create_charge", arguments: { amount: 50000, currency: "IDR", confirm: true } });
    await client.callTool({ name: "waffle_create_charge", arguments: { amount: 50000, currency: "IDR", confirm: true } });

    const calls = toolCalls();
    const firstHeaders = (calls[0] as [string, RequestInit])[1].headers as Record<string, string>;
    const secondHeaders = (calls[1] as [string, RequestInit])[1].headers as Record<string, string>;
    expect(firstHeaders["Idempotency-Key"]).not.toBe(secondHeaders["Idempotency-Key"]);
  });

  it("surfaces a 422 business rejection as an isError tool result carrying the HTTP status", async () => {
    const { client, mockNext } = await connect(FULL_SCOPES);
    mockNext(jsonResponse(422, { error: "insufficient available balance" }));

    const result = await client.callTool({
      name: "waffle_create_payout",
      arguments: { bankAccountId: "ba_1", amount: 999999999, currency: "IDR", confirm: true },
    });

    expect(result.isError).toBe(true);
    const text = (result.content as Array<{ type: string; text: string }>)[0]?.text ?? "";
    expect(text).toContain("422");
    expect(text).toContain("insufficient available balance");
  });

  it("rejects a missing required argument before the handler runs", async () => {
    const { client, toolCalls } = await connect(FULL_SCOPES);
    const result = await client.callTool({ name: "waffle_calculate_fee", arguments: { amount: 1000 } });
    expect(result.isError).toBe(true);
    expect(toolCalls()).toHaveLength(0);
  });

  it("turns a 404 on get_bank_account into a recoverable isError hint instead of a generic failure", async () => {
    const { client, mockNext } = await connect(FULL_SCOPES);
    mockNext(jsonResponse(404, { error: "not found" }));

    const result = await client.callTool({ name: "waffle_get_bank_account", arguments: {} });

    expect(result.isError).toBe(true);
    const text = (result.content as Array<{ type: string; text: string }>)[0]?.text ?? "";
    expect(text).toContain("No withdrawal bank account registered");
  });

  it("maps listBanks results and omits a null logo_url", async () => {
    const { client, mockNext } = await connect(FULL_SCOPES);
    mockNext(jsonResponse(200, [{ code: "BCA", name: "Bank Central Asia", logo_url: null, sort_order: 1 }]));

    const result = await client.callTool({ name: "waffle_list_banks", arguments: {} });

    expect(result.structuredContent).toEqual({ banks: [{ code: "BCA", name: "Bank Central Asia", sortOrder: 1 }] });
  });

  it("resolves get_balance with no query param when currency is omitted, matching the SDK's server-default behavior", async () => {
    const { client, mockNext, toolCalls } = await connect(FULL_SCOPES);
    mockNext(jsonResponse(200, { currency: "IDR", amount: 287500 }));

    const result = await client.callTool({ name: "waffle_get_balance", arguments: {} });

    const [url] = toolCalls()[0] as [string, RequestInit];
    expect(url).toBe("http://waffle.test/v1/balance");
    expect(result.structuredContent).toEqual({ currency: "IDR", amount: 287500 });
  });

  it("reports healthz as unhealthy without throwing when the API is down", async () => {
    const { client, mockNext } = await connect(FULL_SCOPES);
    mockNext(textResponse(500, "unavailable"));

    const result = await client.callTool({ name: "waffle_healthz", arguments: {} });

    expect(result.isError).toBe(true);
  });

  it("returns whoami's merchant, mode, preset and scopes as structured content", async () => {
    const { client } = await connect(FULL_SCOPES, "live");

    const result = await client.callTool({ name: "waffle_whoami", arguments: {} });

    expect(result.structuredContent).toMatchObject({ merchantId: "mer_1", mode: "live", preset: "full" });
  });

  it("fetches (and discards) the charge receipt, returning its path rather than raw PDF bytes", async () => {
    const { client, mockNext } = await connect(FULL_SCOPES);
    mockNext(
      new Response(new Uint8Array([0x25, 0x50, 0x44, 0x46]), {
        status: 200,
        headers: { "Content-Type": "application/pdf" },
      }),
    );

    const result = await client.callTool({ name: "waffle_get_charge_receipt", arguments: { id: "chg_1" } });

    expect(result.isError).toBeFalsy();
    expect(result.structuredContent).toEqual({ chargeId: "chg_1", receiptPath: "/v1/charges/chg_1/receipt.pdf" });
    const text = (result.content as Array<{ type: string; text: string }>)[0]?.text ?? "";
    expect(text).not.toMatch(/%PDF/); // never the raw bytes
    expect(text).toContain("/v1/charges/chg_1/receipt.pdf");
  });

  it("surfaces a 409 'not paid yet' error cleanly from get_charge_receipt", async () => {
    const { client, mockNext } = await connect(FULL_SCOPES);
    mockNext(jsonResponse(409, { error: "charge is not paid yet" }));

    const result = await client.callTool({ name: "waffle_get_charge_receipt", arguments: { id: "chg_1" } });

    expect(result.isError).toBe(true);
    const text = (result.content as Array<{ type: string; text: string }>)[0]?.text ?? "";
    expect(text).toContain("409");
    expect(text).toContain("not paid yet");
  });
});
