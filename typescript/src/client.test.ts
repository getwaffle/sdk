import { beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { PaybridgeClient } from "./client.js";
import { PaybridgeError } from "./errors.js";

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function textResponse(status: number, body: string): Response {
  return new Response(body, { status });
}

describe("PaybridgeClient", () => {
  let fetchMock: Mock;
  let client: PaybridgeClient;

  beforeEach(() => {
    fetchMock = vi.fn();
    client = new PaybridgeClient({
      apiKey: "sk_test_123",
      baseUrl: "http://localhost:8080",
      fetch: fetchMock as unknown as typeof fetch,
    });
  });

  it("sends a correctly shaped createCharge request and maps the response", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(201, {
        id: "charge-1",
        mode: "sandbox",
        status: "pending",
        gross_amount: 100000,
        fee_amount: 3000,
        net_amount: 97000,
        currency: "IDR",
        checkout_url: "https://pay.example/charge-1",
        created_at: "2026-09-08T01:00:00Z",
        metadata: { order: "42" },
      }),
    );

    const charge = await client.createCharge(
      {
        amount: 100000,
        currency: "IDR",
        description: "order 42",
        customerRef: "cust-1",
        metadata: { order: "42" },
      },
      "idem-key-1",
    );

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/v1/charges");
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>)["Authorization"]).toBe(
      "Bearer sk_test_123",
    );
    expect((init.headers as Record<string, string>)["Idempotency-Key"]).toBe(
      "idem-key-1",
    );
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe(
      "application/json",
    );
    expect(JSON.parse(init.body as string)).toEqual({
      amount: 100000,
      currency: "IDR",
      description: "order 42",
      customer_ref: "cust-1",
      metadata: { order: "42" },
    });

    expect(charge).toEqual({
      id: "charge-1",
      mode: "sandbox",
      status: "pending",
      grossAmount: 100000,
      feeAmount: 3000,
      netAmount: 97000,
      currency: "IDR",
      checkoutUrl: "https://pay.example/charge-1",
      createdAt: "2026-09-08T01:00:00Z",
      metadata: { order: "42" },
    });
  });

  it("does not require or send a provider field in the createCharge body", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(201, {
        id: "charge-2",
        mode: "sandbox",
        status: "pending",
        gross_amount: 5000,
        fee_amount: 150,
        net_amount: 4850,
        currency: "IDR",
        created_at: "2026-09-08T02:00:00Z",
      }),
    );

    await client.createCharge({ amount: 5000, currency: "IDR" }, "idem-key-2");

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const body = JSON.parse(init.body as string);
    expect(body).not.toHaveProperty("provider");
  });

  it("sends channel and vaBank in createCharge body and maps qr/va response fields", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(201, {
        id: "charge-va-1",
        mode: "sandbox",
        status: "pending",
        gross_amount: 75000,
        fee_amount: 2000,
        net_amount: 73000,
        currency: "IDR",
        channel: "virtual_account",
        va_bank: "BCA",
        va_number: "8808123456789",
        created_at: "2026-09-08T01:00:00Z",
      }),
    );

    const charge = await client.createCharge(
      { amount: 75000, currency: "IDR", channel: "virtual_account", vaBank: "BCA" },
      "idem-key-va-1",
    );

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({
      amount: 75000,
      currency: "IDR",
      channel: "virtual_account",
      va_bank: "BCA",
    });

    expect(charge).toEqual({
      id: "charge-va-1",
      mode: "sandbox",
      status: "pending",
      grossAmount: 75000,
      feeAmount: 2000,
      netAmount: 73000,
      currency: "IDR",
      channel: "virtual_account",
      vaBank: "BCA",
      vaNumber: "8808123456789",
      createdAt: "2026-09-08T01:00:00Z",
    });
  });

  it("maps qr_string in createCharge response for the qris channel", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(201, {
        id: "charge-qris-1",
        mode: "sandbox",
        status: "pending",
        gross_amount: 15000,
        fee_amount: 500,
        net_amount: 14500,
        currency: "IDR",
        channel: "qris",
        qr_string: "00020101021226610014ID.CO.QRIS.WWW",
        created_at: "2026-09-08T01:00:00Z",
      }),
    );

    const charge = await client.createCharge(
      { amount: 15000, currency: "IDR", channel: "qris" },
      "idem-key-qris-1",
    );

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({
      amount: 15000,
      currency: "IDR",
      channel: "qris",
    });
    expect(charge.qrString).toBe("00020101021226610014ID.CO.QRIS.WWW");
    expect(charge.vaBank).toBeUndefined();
    expect(charge.vaNumber).toBeUndefined();
  });

  it("sends a correctly shaped calculateFee request with no provider field", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, {
        gross_amount: 100000,
        fee_amount: 3000,
        net_amount: 97000,
        currency: "IDR",
      }),
    );

    const quote = await client.calculateFee({
      amount: 100000,
      currency: "IDR",
    });

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/v1/fees/calculate");
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>)["Idempotency-Key"]).toBeUndefined();
    expect(JSON.parse(init.body as string)).toEqual({
      amount: 100000,
      currency: "IDR",
    });
    expect(quote).toEqual({
      grossAmount: 100000,
      feeAmount: 3000,
      netAmount: 97000,
      currency: "IDR",
    });
  });

  it("sends a correctly shaped registerBankAccount request", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(201, {
        id: "bank-1",
        bank_code: "BCA",
        account_number: "1234567890",
        account_holder_name: "Budi Santoso",
      }),
    );

    const account = await client.registerBankAccount({
      bankCode: "BCA",
      accountNumber: "1234567890",
      accountHolderName: "Budi Santoso",
    });

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/v1/bank-accounts");
    expect(JSON.parse(init.body as string)).toEqual({
      bank_code: "BCA",
      account_number: "1234567890",
      account_holder_name: "Budi Santoso",
    });
    expect(account).toEqual({
      id: "bank-1",
      bankCode: "BCA",
      accountNumber: "1234567890",
      accountHolderName: "Budi Santoso",
    });
  });

  it("sends a correctly shaped createPayout request with no provider field", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(201, {
        id: "payout-1",
        bank_account_id: "bank-1",
        mode: "sandbox",
        status: "completed",
        amount: 40000,
        currency: "IDR",
      }),
    );

    const payout = await client.createPayout(
      {
        bankAccountId: "bank-1",
        amount: 40000,
        currency: "IDR",
      },
      "idem-key-3",
    );

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/v1/payouts");
    expect((init.headers as Record<string, string>)["Idempotency-Key"]).toBe(
      "idem-key-3",
    );
    expect(JSON.parse(init.body as string)).toEqual({
      bank_account_id: "bank-1",
      amount: 40000,
      currency: "IDR",
    });
    expect(payout).toEqual({
      id: "payout-1",
      bankAccountId: "bank-1",
      mode: "sandbox",
      status: "completed",
      amount: 40000,
      currency: "IDR",
    });
  });

  it("sends getBalance with a currency query param when supplied", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { currency: "IDR", amount: 287500 }));

    const balance = await client.getBalance("IDR");

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/v1/balance?currency=IDR");
    expect(init.method).toBe("GET");
    expect(init.body).toBeUndefined();
    expect(balance).toEqual({ currency: "IDR", amount: 287500 });
  });

  it("sends getBalance without a query param when currency is omitted", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { currency: "IDR", amount: 0 }));

    await client.getBalance();

    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/v1/balance");
  });

  it("sends listBanks as an unauthenticated-shaped GET and maps camelCase fields, omitting null logo_url", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, [
        { code: "BCA", name: "Bank Central Asia", logo_url: "https://cdn.example/bca.svg", sort_order: 1 },
        { code: "MANDIRI", name: "Bank Mandiri", logo_url: null, sort_order: 2 },
      ]),
    );

    const banks = await client.listBanks();

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/v1/banks");
    expect(init.method).toBe("GET");
    expect(banks).toEqual([
      { code: "BCA", name: "Bank Central Asia", logoUrl: "https://cdn.example/bca.svg", sortOrder: 1 },
      { code: "MANDIRI", name: "Bank Mandiri", sortOrder: 2 },
    ]);
  });

  it("calls healthz with no auth header and returns true on 200 'ok'", async () => {
    fetchMock.mockResolvedValueOnce(textResponse(200, "ok"));

    const healthy = await client.healthz();

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/healthz");
    expect(init.headers).toBeUndefined();
    expect(healthy).toBe(true);
  });

  it("throws a typed PaybridgeError with status and message on 401", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(401, { error: "invalid api key" }));

    await expect(
      client.createCharge({ amount: 1000, currency: "IDR" }, "idem-key-4"),
    ).rejects.toMatchObject(
      new PaybridgeError(401, "invalid api key"),
    );
  });

  it("throws a typed PaybridgeError distinguishing 422 business rejection", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(422, { error: "insufficient available balance" }),
    );

    const error = await client
      .createPayout(
        { bankAccountId: "bank-1", amount: 999999999, currency: "IDR" },
        "idem-key-5",
      )
      .catch((e: unknown) => e);

    expect(error).toBeInstanceOf(PaybridgeError);
    expect((error as PaybridgeError).status).toBe(422);
    expect((error as PaybridgeError).message).toBe("insufficient available balance");
  });

  it("throws a typed PaybridgeError distinguishing 429 fraud/rate limit", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(429, { error: "velocity limit exceeded" }),
    );

    const error = await client
      .createCharge({ amount: 1000, currency: "IDR" }, "idem-key-6")
      .catch((e: unknown) => e);

    expect(error).toBeInstanceOf(PaybridgeError);
    expect((error as PaybridgeError).status).toBe(429);
    expect((error as PaybridgeError).message).toBe("velocity limit exceeded");
  });
});
