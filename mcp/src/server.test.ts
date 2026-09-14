import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";
import { createMcpHandler } from "@modelcontextprotocol/server";
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

describe("paybridge MCP server", () => {
  let fetchMock: Mock;
  let handler: McpHttpHandler;
  let client: Client;

  beforeEach(async () => {
    fetchMock = vi.fn();
    handler = createMcpHandler(() =>
      createServer({ apiKey: "sk_test_123", baseUrl: "http://paybridge.test", fetch: fetchMock as unknown as typeof fetch }),
    );
    const transport = new StreamableHTTPClientTransport(new URL("http://test.local/mcp"), {
      fetch: (url, init) => handler.fetch(new Request(url, init)),
    });
    client = new Client({ name: "test-harness", version: "1.0.0" }, { versionNegotiation: { mode: "auto" } });
    await client.connect(transport);
  });

  afterEach(async () => {
    await client.close();
    await handler.close();
  });

  it("advertises every paybridge tool", async () => {
    const { tools } = await client.listTools();
    const names = tools.map((t) => t.name).sort();
    expect(names).toEqual(
      [
        "paybridge_calculate_fee",
        "paybridge_create_charge",
        "paybridge_create_payout",
        "paybridge_get_balance",
        "paybridge_get_bank_account",
        "paybridge_healthz",
        "paybridge_list_banks",
        "paybridge_register_bank_account",
      ].sort(),
    );
  });

  it("creates a charge, mints its own idempotency key, and returns structured content", async () => {
    fetchMock.mockResolvedValueOnce(
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
      name: "paybridge_create_charge",
      arguments: { amount: 100000, currency: "IDR", description: "Order #1" },
    });

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://paybridge.test/v1/charges");
    const headers = init.headers as Record<string, string>;
    expect(headers["Idempotency-Key"]).toBeTruthy();
    expect(JSON.parse(init.body as string)).not.toHaveProperty("provider");

    expect(result.isError).toBeFalsy();
    expect(result.structuredContent).toMatchObject({ id: "chg_1", status: "pending", grossAmount: 100000 });
    expect((result.content as Array<{ type: string; text: string }>)[0]?.text).toContain("Rp100.000");
  });

  it("mints a fresh idempotency key on every create_charge call", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(201, {
        id: "chg_2",
        mode: "sandbox",
        status: "pending",
        gross_amount: 50000,
        fee_amount: 1500,
        net_amount: 48500,
        currency: "IDR",
        created_at: "2026-09-14T00:00:00Z",
      }),
    );

    await client.callTool({ name: "paybridge_create_charge", arguments: { amount: 50000, currency: "IDR" } });
    await client.callTool({ name: "paybridge_create_charge", arguments: { amount: 50000, currency: "IDR" } });

    const firstHeaders = (fetchMock.mock.calls[0] as [string, RequestInit])[1].headers as Record<string, string>;
    const secondHeaders = (fetchMock.mock.calls[1] as [string, RequestInit])[1].headers as Record<string, string>;
    expect(firstHeaders["Idempotency-Key"]).not.toBe(secondHeaders["Idempotency-Key"]);
  });

  it("surfaces a 422 business rejection as an isError tool result carrying the HTTP status", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(422, { error: "insufficient available balance" }));

    const result = await client.callTool({
      name: "paybridge_create_payout",
      arguments: { bankAccountId: "ba_1", amount: 999999999, currency: "IDR" },
    });

    expect(result.isError).toBe(true);
    const text = (result.content as Array<{ type: string; text: string }>)[0]?.text ?? "";
    expect(text).toContain("422");
    expect(text).toContain("insufficient available balance");
  });

  it("rejects a missing required argument before the handler runs", async () => {
    const result = await client.callTool({ name: "paybridge_calculate_fee", arguments: { amount: 1000 } });
    expect(result.isError).toBe(true);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("turns a 404 on get_bank_account into a recoverable isError hint instead of a generic failure", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(404, { error: "not found" }));

    const result = await client.callTool({ name: "paybridge_get_bank_account", arguments: {} });

    expect(result.isError).toBe(true);
    const text = (result.content as Array<{ type: string; text: string }>)[0]?.text ?? "";
    expect(text).toContain("paybridge_register_bank_account");
  });

  it("maps listBanks results and omits a null logo_url", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, [{ code: "BCA", name: "Bank Central Asia", logo_url: null, sort_order: 1 }]),
    );

    const result = await client.callTool({ name: "paybridge_list_banks", arguments: {} });

    expect(result.structuredContent).toEqual({ banks: [{ code: "BCA", name: "Bank Central Asia", sortOrder: 1 }] });
  });

  it("resolves get_balance with no query param when currency is omitted, matching the SDK's server-default behavior", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { currency: "IDR", amount: 287500 }));

    const result = await client.callTool({ name: "paybridge_get_balance", arguments: {} });

    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://paybridge.test/v1/balance");
    expect(result.structuredContent).toEqual({ currency: "IDR", amount: 287500 });
  });

  it("reports healthz as unhealthy without throwing when the API is down", async () => {
    fetchMock.mockResolvedValueOnce(textResponse(500, "unavailable"));

    const result = await client.callTool({ name: "paybridge_healthz", arguments: {} });

    expect(result.isError).toBe(true);
  });
});
