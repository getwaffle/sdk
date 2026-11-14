import { describe, expect, it, vi, type Mock } from "vitest";
import util from "node:util";
import { WaffleClient, normalizeBaseUrl } from "./client.js";
import {
  WaffleError,
  WaffleResponseTooLargeError,
  WaffleTimeoutError,
  WaffleValidationError,
} from "./errors.js";

const KEY = "sk_live_supersecretvalue";
const ok = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const CHARGE = {
  id: "c1", mode: "live", status: "pending", gross_amount: 1000, fee_amount: 0,
  net_amount: 1000, currency: "IDR", created_at: "2026-01-01T00:00:00Z",
};

function make(fetchMock: Mock, extra: Record<string, unknown> = {}) {
  return new WaffleClient({ apiKey: KEY, fetch: fetchMock as unknown as typeof fetch, ...extra });
}

/** fetch that never resolves until its signal aborts. */
function hangingFetch(): Mock {
  return vi.fn((_u: string, init: RequestInit) =>
    new Promise<Response>((_res, rej) => {
      init.signal!.addEventListener("abort", () => rej(init.signal!.reason));
    }),
  );
}

describe("redirect policy and signal", () => {
  it("passes redirect:'error' and a signal on every request kind", async () => {
    const f = vi.fn(async (u: string, init?: RequestInit) =>
      u.endsWith("/healthz") ? new Response("ok") :
      u.endsWith(".pdf") ? new Response(new Uint8Array([1, 2])) :
      ok(init?.method === "POST" ? CHARGE : { data: [], has_more: false }));
    const c = make(f as unknown as Mock);
    await c.createCharge({ amount: 1000, currency: "IDR" });
    await c.listCharges();
    await c.healthz();
    await c.getChargeReceipt("c1");
    expect(f).toHaveBeenCalledTimes(4);
    for (const call of f.mock.calls as unknown as [string, RequestInit][]) {
      expect(call[1].redirect).toBe("error");
      expect(call[1].signal).toBeInstanceOf(AbortSignal);
    }
  });
});

describe("timeouts", () => {
  it("POST timeout throws WaffleTimeoutError saying outcome is unknown", async () => {
    const c = make(hangingFetch(), { timeoutMs: 20 });
    const err = await c.createCharge({ amount: 1000, currency: "IDR" }, "k1").catch((e) => e);
    expect(err).toBeInstanceOf(WaffleTimeoutError);
    expect(err).toBeInstanceOf(WaffleError);
    expect(err.outcomeUnknown).toBe(true);
    expect(err.message).toMatch(/UNKNOWN/);
    expect(err.message).toMatch(/SAME idempotency key/);
  });

  it("GET and healthz time out too", async () => {
    const c = make(hangingFetch(), { timeoutMs: 20 });
    const g = await c.getCharge("c1").catch((e) => e);
    expect(g).toBeInstanceOf(WaffleTimeoutError);
    expect(g.outcomeUnknown).toBe(false);
    expect(await c.healthz().catch((e) => e)).toBeInstanceOf(WaffleTimeoutError);
  });

  it("times out while a body stalls mid-stream", async () => {
    const f = vi.fn(async (_u: string, init: RequestInit) => {
      const body = new ReadableStream({
        start(ctrl) {
          init.signal!.addEventListener("abort", () => ctrl.error(init.signal!.reason));
        },
      });
      return new Response(body, { status: 200 });
    });
    const c = make(f as unknown as Mock, { timeoutMs: 20 });
    expect(await c.getCharge("c1").catch((e) => e)).toBeInstanceOf(WaffleTimeoutError);
  });

  it("a caller abort surfaces as the abort error, not a timeout", async () => {
    const ac = new AbortController();
    const c = make(hangingFetch(), { signal: ac.signal });
    const p = c.getCharge("c1").catch((e) => e);
    ac.abort();
    const err = await p;
    expect(err).not.toBeInstanceOf(WaffleTimeoutError);
    expect((err as Error).name).toBe("AbortError");
  });

  it("works when AbortSignal.any is unavailable", async () => {
    const orig = (AbortSignal as unknown as { any?: unknown }).any;
    (AbortSignal as unknown as { any?: unknown }).any = undefined;
    try {
      const ac = new AbortController();
      const c = make(hangingFetch(), { signal: ac.signal, timeoutMs: 20 });
      expect(await c.getCharge("c1").catch((e) => e)).toBeInstanceOf(WaffleTimeoutError);
      const c2 = make(hangingFetch(), { signal: ac.signal });
      const p = c2.getCharge("c1").catch((e) => e);
      ac.abort();
      expect(((await p) as Error).name).toBe("AbortError");
    } finally {
      (AbortSignal as unknown as { any?: unknown }).any = orig;
    }
  });

  it("rejects invalid timeoutMs", () => {
    for (const t of [0, -1, NaN, Infinity]) {
      expect(() => new WaffleClient({ apiKey: KEY, timeoutMs: t })).toThrow(/timeoutMs/);
    }
  });
});

describe("baseUrl validation", () => {
  const good: [string, string][] = [
    ["https://api.getwaffle.id", "https://api.getwaffle.id"],
    ["  https://api.getwaffle.id/  ", "https://api.getwaffle.id"],
    ["https://example.com/", "https://example.com"],
    ["https://Example.com:8443/", "https://example.com:8443"],
    ["http://localhost:8080", "http://localhost:8080"],
    ["http://127.0.0.1:8080/", "http://127.0.0.1:8080"],
    ["http://[::1]:8080", "http://[::1]:8080"],
  ];
  it.each(good)("accepts %s", (input, want) => {
    expect(normalizeBaseUrl(input)).toBe(want);
    expect(() => new WaffleClient({ apiKey: KEY, baseUrl: input })).not.toThrow();
  });

  const bad = [
    "http://api.getwaffle.id",
    "https://a.com/v1/",
    "https://api.getwaffle.id//",
    "https://a.com/v1",
    "http://localhost:3000/some/path",
    "http://localhost.evil.com",
    "ftp://api.getwaffle.id",
    "https://user:pw@api.getwaffle.id",
    "https://user@api.getwaffle.id",
    "https://api.getwaffle.id?x=1",
    "https://api.getwaffle.id/?",
    "https://api.getwaffle.id#frag",
    "https://",
    "api.getwaffle.id",
    "",
    "//api.getwaffle.id",
  ];
  it.each(bad)("rejects %j", (input) => {
    expect(() => new WaffleClient({ apiKey: KEY, baseUrl: input })).toThrow(/baseUrl/);
  });

  it("still supports a custom fetch", async () => {
    const f = vi.fn(async () => ok(CHARGE));
    await make(f as unknown as Mock, { baseUrl: "https://x.example/" }).getCharge("c1");
    expect(f.mock.calls[0]![0]).toBe("https://x.example/v1/charges/c1");
  });
});

describe("response size caps", () => {
  const MiB = 1024 * 1024;
  const stream = (chunks: number, size: number) => {
    let n = 0;
    return new ReadableStream<Uint8Array>({
      pull(ctrl) {
        if (n++ >= chunks) return ctrl.close();
        ctrl.enqueue(new Uint8Array(size).fill(0x20));
      },
    });
  };

  it("rejects JSON by Content-Length before reading", async () => {
    const f = vi.fn(async () => new Response("{}", { headers: { "content-length": String(9 * MiB) } }));
    const err = await make(f as unknown as Mock).getCharge("c1").catch((e) => e);
    expect(err).toBeInstanceOf(WaffleResponseTooLargeError);
    expect(err.limitBytes).toBe(8 * MiB);
  });

  it("enforces the cap while streaming without a Content-Length", async () => {
    const f = vi.fn(async () => new Response(stream(10, MiB)));
    expect(await make(f as unknown as Mock).getCharge("c1").catch((e) => e)).toBeInstanceOf(
      WaffleResponseTooLargeError,
    );
  });

  it("binary cap is 16 MiB: 12 MiB ok, 17 MiB rejected", async () => {
    const f12 = vi.fn(async () => new Response(stream(12, MiB)));
    const bytes = await make(f12 as unknown as Mock).getChargeReceipt("c1");
    expect(bytes.length).toBe(12 * MiB);
    const f17 = vi.fn(async () => new Response(stream(17, MiB)));
    expect(await make(f17 as unknown as Mock).getChargeReceipt("c1").catch((e) => e)).toBeInstanceOf(
      WaffleResponseTooLargeError,
    );
  });

  it("caps healthz bodies", async () => {
    const f = vi.fn(async () => new Response(stream(9, MiB)));
    expect(await make(f as unknown as Mock).healthz().catch((e) => e)).toBeInstanceOf(
      WaffleResponseTooLargeError,
    );
  });
});

describe("error message sanitising", () => {
  it("truncates to 512 chars and replaces control/format chars, keeping status", async () => {
    const evil = ["bad", "\u001b[31m", "\r\nline", "\u202Eevil", "\u2028x", "\u200B"].join("") + "a".repeat(2000);
    const f = vi.fn(async () => ok({ error: evil }, 422));
    const err = await make(f as unknown as Mock).getCharge("c1").catch((e) => e);
    expect(err).toBeInstanceOf(WaffleError);
    expect(err.status).toBe(422);
    expect(err.message.length).toBeLessThanOrEqual(512);
    expect(err.message).not.toMatch(/[\p{Cc}\p{Cf}\u2028\u2029]/u);
    expect(err.message.startsWith("bad [31m  line evil x ")).toBe(true);
  });

  it("keeps permission scope parsing and ignores non-string error fields", async () => {
    const f1 = vi.fn(async () => ok({ error: "this API key lacks the payouts:write permission" }, 403));
    const e1 = await make(f1 as unknown as Mock).getCharge("c1").catch((e) => e);
    expect(e1.scope).toBe("payouts:write");
    const f2 = vi.fn(async () => ok({ error: { nested: 1 } }, 500));
    const e2 = await make(f2 as unknown as Mock).getCharge("c1").catch((e) => e);
    expect(e2.status).toBe(500);
    expect(typeof e2.message).toBe("string");
  });
});

describe("API key redaction", () => {
  it("never appears via JSON.stringify, util.inspect, console.log or Object.keys", () => {
    const c = new WaffleClient({ apiKey: KEY });
    const test = new WaffleClient({ apiKey: "sk_test_abcdef" });
    for (const out of [
      JSON.stringify(c),
      util.inspect(c, { depth: 5, showHidden: true }),
      util.format("%o %s %j", c, c, c),
      JSON.stringify(Object.getOwnPropertyNames(c)),
    ]) {
      expect(out).not.toContain("supersecretvalue");
    }
    expect(JSON.parse(JSON.stringify(c)).apiKey).toBe("sk_live_****");
    expect(JSON.parse(JSON.stringify(test)).apiKey).toBe("sk_test_****");
    expect(util.inspect(c)).toContain("sk_live_****");
    expect(Object.getOwnPropertySymbols(c).map(String)).not.toContain(KEY);
  });
});

describe("input validation", () => {
  it.each([0, -5, 1.5, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1, "100" as unknown as number])(
    "rejects amount %s before any request",
    async (amount) => {
      const f = vi.fn();
      const c = make(f);
      await expect(c.createCharge({ amount, currency: "IDR" })).rejects.toBeInstanceOf(WaffleValidationError);
      await expect(c.createPayout({ bankAccountId: "b", amount, currency: "IDR" })).rejects.toBeInstanceOf(
        WaffleValidationError,
      );
      await expect(c.calculateFee({ amount, currency: "IDR" })).rejects.toBeInstanceOf(WaffleValidationError);
      expect(f).not.toHaveBeenCalled();
    },
  );

  it.each(["", "a b", "a/b", "ключ", "a\nb", "x".repeat(256)])(
    "rejects idempotency key %j",
    async (key) => {
      const f = vi.fn();
      const c = make(f);
      await expect(c.createCharge({ amount: 1000, currency: "IDR" }, key)).rejects.toBeInstanceOf(
        WaffleValidationError,
      );
      await expect(
        c.createPayout({ bankAccountId: "b", amount: 1000, currency: "IDR" }, key),
      ).rejects.toBeInstanceOf(WaffleValidationError);
      expect(f).not.toHaveBeenCalled();
    },
  );

  it("accepts valid keys (255 chars, punctuation) and generates one when omitted", async () => {
    const f = vi.fn(async () => ok(CHARGE, 201));
    const c = make(f as unknown as Mock);
    await c.createCharge({ amount: 1000, currency: "IDR" }, "a".repeat(255));
    await c.createCharge({ amount: 1000, currency: "IDR" }, "order:42_v1.2-x");
    await c.createCharge({ amount: 1000, currency: "IDR" });
    const keys = (f.mock.calls as unknown as [string, RequestInit][]).map(
      (x) => (x[1].headers as Record<string, string>)["Idempotency-Key"],
    );
    expect(keys[0]).toHaveLength(255);
    expect(keys[1]).toBe("order:42_v1.2-x");
    expect(keys[2]).toMatch(/^[0-9a-f-]{36}$/);
  });
});
