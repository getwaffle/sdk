import { beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { WaffleClient } from "./client.js";
import { WaffleError, WafflePermissionError } from "./errors.js";

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function textResponse(status: number, body: string): Response {
  return new Response(body, { status });
}

describe("WaffleClient", () => {
  let fetchMock: Mock;
  let client: WaffleClient;

  beforeEach(() => {
    fetchMock = vi.fn();
    client = new WaffleClient({
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

  it("sends a correctly shaped getBankAccount request and maps masked fields", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, {
        id: "bank-1",
        bank_code: "BCA",
        account_number: "******7890",
        account_holder_name: "Budi Santoso",
        created_at: "2026-09-08T01:00:00Z",
      }),
    );

    const account = await client.getBankAccount();

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/v1/bank-accounts");
    expect(init.method).toBe("GET");
    expect(account).toEqual({
      id: "bank-1",
      bankCode: "BCA",
      accountNumber: "******7890",
      accountHolderName: "Budi Santoso",
      createdAt: "2026-09-08T01:00:00Z",
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

  it("throws a typed WaffleError with status and message on 401", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(401, { error: "invalid api key" }));

    await expect(
      client.createCharge({ amount: 1000, currency: "IDR" }, "idem-key-4"),
    ).rejects.toMatchObject(
      new WaffleError(401, "invalid api key"),
    );
  });

  it("throws a typed WaffleError distinguishing 422 business rejection", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(422, { error: "insufficient available balance" }),
    );

    const error = await client
      .createPayout(
        { bankAccountId: "bank-1", amount: 999999999, currency: "IDR" },
        "idem-key-5",
      )
      .catch((e: unknown) => e);

    expect(error).toBeInstanceOf(WaffleError);
    expect((error as WaffleError).status).toBe(422);
    expect((error as WaffleError).message).toBe("insufficient available balance");
  });

  it("throws a typed WaffleError distinguishing 429 fraud/rate limit", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(429, { error: "velocity limit exceeded" }),
    );

    const error = await client
      .createCharge({ amount: 1000, currency: "IDR" }, "idem-key-6")
      .catch((e: unknown) => e);

    expect(error).toBeInstanceOf(WaffleError);
    expect((error as WaffleError).status).toBe(429);
    expect((error as WaffleError).message).toBe("velocity limit exceeded");
  });

  it("throws a typed WafflePermissionError on 403 scope denial, parsing the scope", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(403, { error: "this API key lacks the payouts:write permission" }),
    );

    const error = await client
      .createPayout(
        { bankAccountId: "bank-1", amount: 1000, currency: "IDR" },
        "idem-key-7",
      )
      .catch((e: unknown) => e);

    expect(error).toBeInstanceOf(WaffleError);
    expect(error).toBeInstanceOf(WafflePermissionError);
    expect((error as WafflePermissionError).status).toBe(403);
    expect((error as WafflePermissionError).scope).toBe("payouts:write");
    expect((error as WafflePermissionError).message).toBe(
      "this API key lacks the payouts:write permission",
    );
  });

  it("throws a plain WaffleError (not WafflePermissionError) for a 403 that doesn't match the scope pattern", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(403, { error: "forbidden" }));

    const error = await client.getBalance().catch((e: unknown) => e);

    expect(error).toBeInstanceOf(WaffleError);
    expect(error).not.toBeInstanceOf(WafflePermissionError);
    expect((error as WaffleError).status).toBe(403);
  });

  it("auto-generates an Idempotency-Key for createCharge when omitted", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(201, {
        id: "charge-auto",
        mode: "sandbox",
        status: "pending",
        gross_amount: 1000,
        fee_amount: 0,
        net_amount: 1000,
        currency: "IDR",
        created_at: "2026-09-08T01:00:00Z",
      }),
    );

    await client.createCharge({ amount: 1000, currency: "IDR" });

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const key = (init.headers as Record<string, string>)["Idempotency-Key"];
    expect(key).toBeTruthy();
    expect(key).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("auto-generates an Idempotency-Key for createPayout when omitted", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(201, {
        id: "payout-auto",
        bank_account_id: "bank-1",
        mode: "sandbox",
        status: "pending",
        amount: 1000,
        currency: "IDR",
      }),
    );

    await client.createPayout({ bankAccountId: "bank-1", amount: 1000, currency: "IDR" });

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const key = (init.headers as Record<string, string>)["Idempotency-Key"];
    expect(key).toBeTruthy();
    expect(key).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("sends channel/vaBank in calculateFee and maps the quote", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, {
        gross_amount: 75000,
        fee_amount: 2000,
        net_amount: 73000,
        currency: "IDR",
      }),
    );

    await client.calculateFee({
      amount: 75000,
      currency: "IDR",
      channel: "virtual_account",
      vaBank: "BCA",
    });

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({
      amount: 75000,
      currency: "IDR",
      channel: "virtual_account",
      va_bank: "BCA",
    });
  });

  it("sends a correctly shaped getCharge request and maps optional timestamps and breakdown", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, {
        id: "charge-1",
        mode: "sandbox",
        status: "paid",
        gross_amount: 100000,
        fee_amount: 3000,
        net_amount: 97000,
        currency: "IDR",
        created_at: "2026-09-08T01:00:00Z",
        paid_at: "2026-09-08T01:05:00Z",
        expires_at: "2026-09-08T02:00:00Z",
        settled_at: "2026-09-09T00:00:00Z",
        checkout_channel_selection: "merchant",
        breakdown: {
          base_amount: 100000,
          fee_amount: 3000,
          fee_bearer: "merchant",
          fee_rule: { type: "percentage", percent_bps: 300, flat_amount: 0 },
          payer_paid: 100000,
          merchant_receives: 97000,
          channel: "qris",
        },
      }),
    );

    const charge = await client.getCharge("charge-1");

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/v1/charges/charge-1");
    expect(init.method).toBe("GET");
    expect(charge.paidAt).toBe("2026-09-08T01:05:00Z");
    expect(charge.expiresAt).toBe("2026-09-08T02:00:00Z");
    expect(charge.settledAt).toBe("2026-09-09T00:00:00Z");
    expect(charge.checkoutChannelSelection).toBe("merchant");
    expect(charge.breakdown).toEqual({
      baseAmount: 100000,
      feeAmount: 3000,
      feeBearer: "merchant",
      feeRule: { type: "percentage", percentBps: 300, flatAmount: 0 },
      payerPaid: 100000,
      merchantReceives: 97000,
      channel: "qris",
    });
  });

  it("maps a null breakdown for an unpriced payer-selection charge", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(201, {
        id: "charge-payer-1",
        mode: "sandbox",
        status: "pending",
        gross_amount: 0,
        fee_amount: 0,
        net_amount: 0,
        currency: "IDR",
        created_at: "2026-09-08T01:00:00Z",
        checkout_channel_selection: "payer",
        breakdown: null,
      }),
    );

    const charge = await client.createCharge({
      amount: 100000,
      currency: "IDR",
      checkoutChannelSelection: "payer",
    });

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toMatchObject({
      checkout_channel_selection: "payer",
    });
    expect(charge.breakdown).toBeNull();
  });

  it("sends listCharges query params and maps hasMore", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, {
        data: [
          {
            id: "charge-1",
            mode: "sandbox",
            status: "paid",
            gross_amount: 1000,
            fee_amount: 0,
            net_amount: 1000,
            currency: "IDR",
            created_at: "2026-09-08T01:00:00Z",
          },
        ],
        has_more: true,
      }),
    );

    const page = await client.listCharges({
      limit: 10,
      startingAfter: "charge-0",
      status: "paid",
      createdGte: "2026-09-01",
      createdLte: "2026-09-30",
    });

    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    const parsed = new URL(url);
    expect(parsed.pathname).toBe("/v1/charges");
    expect(parsed.searchParams.get("limit")).toBe("10");
    expect(parsed.searchParams.get("starting_after")).toBe("charge-0");
    expect(parsed.searchParams.get("status")).toBe("paid");
    expect(parsed.searchParams.get("created[gte]")).toBe("2026-09-01");
    expect(parsed.searchParams.get("created[lte]")).toBe("2026-09-30");
    expect(page.hasMore).toBe(true);
    expect(page.data).toHaveLength(1);
    expect(page.data[0]!.id).toBe("charge-1");
  });

  it("listChargesAutoPaging pages through starting_after until has_more is false", async () => {
    fetchMock
      .mockResolvedValueOnce(
        jsonResponse(200, {
          data: [
            {
              id: "charge-1",
              mode: "sandbox",
              status: "paid",
              gross_amount: 1000,
              fee_amount: 0,
              net_amount: 1000,
              currency: "IDR",
              created_at: "2026-09-08T01:00:00Z",
            },
          ],
          has_more: true,
        }),
      )
      .mockResolvedValueOnce(
        jsonResponse(200, {
          data: [
            {
              id: "charge-2",
              mode: "sandbox",
              status: "paid",
              gross_amount: 2000,
              fee_amount: 0,
              net_amount: 2000,
              currency: "IDR",
              created_at: "2026-09-08T02:00:00Z",
            },
          ],
          has_more: false,
        }),
      );

    const ids: string[] = [];
    for await (const charge of client.listChargesAutoPaging()) {
      ids.push(charge.id);
    }

    expect(ids).toEqual(["charge-1", "charge-2"]);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    const [secondUrl] = fetchMock.mock.calls[1] as [string, RequestInit];
    expect(new URL(secondUrl).searchParams.get("starting_after")).toBe("charge-1");
  });

  it("fetches the charge receipt PDF as bytes", async () => {
    const pdfBytes = new Uint8Array([0x25, 0x50, 0x44, 0x46]);
    fetchMock.mockResolvedValueOnce(
      new Response(pdfBytes, {
        status: 200,
        headers: { "Content-Type": "application/pdf" },
      }),
    );

    const bytes = await client.getChargeReceipt("charge-1");

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/v1/charges/charge-1/receipt.pdf");
    expect(init.method).toBe("GET");
    expect(bytes).toBeInstanceOf(Uint8Array);
    expect(Array.from(bytes)).toEqual([0x25, 0x50, 0x44, 0x46]);
  });

  it("throws a WaffleError with status 409 when the charge receipt isn't ready", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(409, { error: "charge not paid" }));

    const error = await client.getChargeReceipt("charge-1").catch((e: unknown) => e);

    expect(error).toBeInstanceOf(WaffleError);
    expect((error as WaffleError).status).toBe(409);
    expect((error as WaffleError).message).toBe("charge not paid");
  });

  it("sends a correctly shaped getPayout request and maps GET-only fields", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, {
        id: "payout-1",
        bank_account_id: "bank-1",
        mode: "sandbox",
        status: "completed",
        amount: 40000,
        currency: "IDR",
        bank_code: "BCA",
        account_number: "******7890",
        account_holder_name: "Budi Santoso",
        created_at: "2026-09-08T01:00:00Z",
        completed_at: "2026-09-08T01:10:00Z",
      }),
    );

    const payout = await client.getPayout("payout-1");

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/v1/payouts/payout-1");
    expect(init.method).toBe("GET");
    expect(payout).toEqual({
      id: "payout-1",
      bankAccountId: "bank-1",
      mode: "sandbox",
      status: "completed",
      amount: 40000,
      currency: "IDR",
      bankCode: "BCA",
      accountNumber: "******7890",
      accountHolderName: "Budi Santoso",
      createdAt: "2026-09-08T01:00:00Z",
      completedAt: "2026-09-08T01:10:00Z",
    });
  });

  it("sends listPayouts query params and maps hasMore", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, {
        data: [
          {
            id: "payout-1",
            bank_account_id: "bank-1",
            mode: "sandbox",
            status: "completed",
            amount: 1000,
            currency: "IDR",
          },
        ],
        has_more: false,
      }),
    );

    const page = await client.listPayouts({ status: "completed" });

    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    const parsed = new URL(url);
    expect(parsed.searchParams.get("status")).toBe("completed");
    expect(page.hasMore).toBe(false);
    expect(page.data[0]!.id).toBe("payout-1");
  });

  it("listPayoutsAutoPaging pages through starting_after until has_more is false", async () => {
    fetchMock
      .mockResolvedValueOnce(
        jsonResponse(200, {
          data: [
            {
              id: "payout-1",
              bank_account_id: "bank-1",
              mode: "sandbox",
              status: "completed",
              amount: 1000,
              currency: "IDR",
            },
          ],
          has_more: true,
        }),
      )
      .mockResolvedValueOnce(
        jsonResponse(200, {
          data: [
            {
              id: "payout-2",
              bank_account_id: "bank-1",
              mode: "sandbox",
              status: "completed",
              amount: 2000,
              currency: "IDR",
            },
          ],
          has_more: false,
        }),
      );

    const ids: string[] = [];
    for await (const payout of client.listPayoutsAutoPaging()) {
      ids.push(payout.id);
    }

    expect(ids).toEqual(["payout-1", "payout-2"]);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("fetches the payout receipt PDF as bytes", async () => {
    const pdfBytes = new Uint8Array([0x25, 0x50, 0x44, 0x46]);
    fetchMock.mockResolvedValueOnce(new Response(pdfBytes, { status: 200 }));

    const bytes = await client.getPayoutReceipt("payout-1");

    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/v1/payouts/payout-1/receipt.pdf");
    expect(Array.from(bytes)).toEqual([0x25, 0x50, 0x44, 0x46]);
  });

  it("sends a correctly shaped whoami request with no scope required", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, {
        merchant_id: "merchant-1",
        business_name: "Toko Budi",
        mode: "sandbox",
        preset: "read_only",
        scopes: ["charges:read", "payouts:read", "balance:read"],
      }),
    );

    const who = await client.whoami();

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/v1/whoami");
    expect(init.method).toBe("GET");
    expect(who).toEqual({
      merchantId: "merchant-1",
      businessName: "Toko Budi",
      mode: "sandbox",
      preset: "read_only",
      scopes: ["charges:read", "payouts:read", "balance:read"],
    });
  });
});
