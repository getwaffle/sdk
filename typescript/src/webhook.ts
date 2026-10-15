/**
 * Webhook signature verification for Waffle deliveries.
 *
 * Uses only the Web Crypto API (`crypto.subtle`), so it runs unchanged on
 * Node >=18, browsers, Cloudflare Workers, Deno, Bun and Vercel Edge. The
 * HMAC comparison is done by `crypto.subtle.verify`, which is constant-time.
 */

/** Default replay tolerance in seconds, applied in both directions. */
export const DEFAULT_WEBHOOK_TOLERANCE_SECONDS = 300;

export type WebhookErrorReason =
  | "missing_signature"
  | "malformed_header"
  | "timestamp_out_of_tolerance"
  | "signature_mismatch"
  | "invalid_json";

/**
 * Thrown by {@link verifyWebhook} on every verification failure. Branch on
 * `reason`. Neither the message nor any field contains the signing secret
 * or signature values. Respond 400 (not 2xx) so Waffle does not treat the
 * delivery as received.
 */
export class WebhookVerificationError extends Error {
  readonly reason: WebhookErrorReason;

  constructor(reason: WebhookErrorReason, message: string) {
    super(`webhook verification failed (${reason}): ${message}`);
    this.name = "WebhookVerificationError";
    this.reason = reason;
    Object.setPrototypeOf(this, WebhookVerificationError.prototype);
  }
}

/** The verified event envelope. Fields missing from the payload are `undefined`. */
export interface WebhookEvent {
  /** e.g. `charge.paid`, `payout.completed`, `webhook.test`. */
  event: string | undefined;
  /** Outbox id; equal to the `X-Waffle-Delivery` header. */
  eventId: string | undefined;
  /** RFC3339 UTC time the event was enqueued, as sent. */
  createdAt: string | undefined;
  chargeId: string | undefined;
  payoutId: string | undefined;
  status: string | undefined;
  /** Integer minor units. */
  amount: number | undefined;
  currency: string | undefined;
  mode: string | undefined;
  /**
   * The `X-Waffle-Delivery` header: stable across retries of the same
   * event. Deduplicate on this value.
   */
  deliveryId: string | undefined;
  /** The complete decoded JSON object (snake_case, including fields not modelled above). */
  raw: Record<string, unknown>;
}

export interface VerifyWebhookOptions {
  /** Max |now - t| in seconds. Default 300. */
  toleranceSeconds?: number;
  /** Clock override returning unix seconds (for tests). Default `Date.now()/1000`. */
  now?: () => number;
  /**
   * Accept the legacy `X-Waffle-Signature` header when the V2 header is
   * absent. Default `true`. The legacy signature covers the body only and
   * has NO replay protection.
   */
  allowLegacy?: boolean;
}

/** Header bag accepted by {@link verifyWebhook}: a fetch `Headers`, or a plain object (any key case). */
export type WebhookHeaders =
  | { get(name: string): string | null }
  | Record<string, string | string[] | undefined>;

const encoder = new TextEncoder();
const HEX_SHA256 = /^[0-9a-fA-F]{64}$/;

function getHeader(headers: WebhookHeaders, name: string): string | undefined {
  if (typeof (headers as { get?: unknown }).get === "function") {
    const v = (headers as { get(n: string): string | null }).get(name);
    return v === null ? undefined : v.trim();
  }
  const wanted = name.toLowerCase();
  for (const [k, v] of Object.entries(headers as Record<string, string | string[] | undefined>)) {
    if (k.toLowerCase() === wanted && v !== undefined) {
      return (Array.isArray(v) ? v.join(",") : v).trim();
    }
  }
  return undefined;
}

function hexToBytes(hex: string): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.slice(2 * i, 2 * i + 2), 16);
  return out;
}

function parseV2(value: string): { t: number; sigs: Uint8Array<ArrayBuffer>[] } {
  const bad = (msg: string) =>
    new WebhookVerificationError("malformed_header", `X-Waffle-Signature-V2: ${msg}`);
  let t: number | undefined;
  const sigs: Uint8Array<ArrayBuffer>[] = [];
  for (const rawPart of value.split(",")) {
    const part = rawPart.trim();
    const eq = part.indexOf("=");
    const k = eq < 0 ? "" : part.slice(0, eq).trim();
    const v = eq < 0 ? "" : part.slice(eq + 1).trim();
    if (k === "" || v === "") throw bad("expected comma-separated key=value pairs");
    if (k === "t") {
      if (t !== undefined) throw bad("duplicate t");
      if (!/^[0-9]+$/.test(v) || !Number.isSafeInteger(Number(v))) throw bad("t is not a unix timestamp");
      t = Number(v);
    } else if (k === "v1") {
      if (!HEX_SHA256.test(v)) throw bad("v1 is not a hex SHA-256 digest");
      sigs.push(hexToBytes(v));
    }
    // unknown keys are ignored for forward compatibility
  }
  if (t === undefined || sigs.length === 0) throw bad("requires t and at least one v1");
  return { t, sigs };
}

async function hmacMatches(secret: string, data: Uint8Array<ArrayBuffer>, sigs: Uint8Array<ArrayBuffer>[]): Promise<boolean> {
  const key = await crypto.subtle.importKey(
    "raw",
    encoder.encode(secret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["verify"],
  );
  let ok = false;
  for (const sig of sigs) {
    // crypto.subtle.verify compares in constant time. No early exit on match.
    if (await crypto.subtle.verify("HMAC", key, sig, data)) ok = true;
  }
  return ok;
}

function concat(...parts: Uint8Array[]): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let off = 0;
  for (const p of parts) {
    out.set(p, off);
    off += p.length;
  }
  return out;
}

/**
 * Authenticates a Waffle webhook delivery and returns the typed event.
 *
 * `body` MUST be the exact raw request body (read it as text/bytes before
 * any JSON parsing; a re-serialised body will not verify). A `string` is
 * encoded as UTF-8.
 *
 * Verifies `X-Waffle-Signature-V2` (`t=<unix>,v1=<hex>`, HMAC-SHA256 over
 * `"<t>.<body>"`) with a constant-time compare and a timestamp tolerance
 * in both directions. Only when that header is absent and `allowLegacy` is
 * true is the legacy `X-Waffle-Signature` (`sha256=<hex>` over the body)
 * accepted — it carries no replay protection. A present-but-invalid V2
 * header never falls back to legacy.
 *
 * @throws {WebhookVerificationError} with a machine-readable `reason`.
 * @throws {TypeError} when `secret` is empty.
 */
export async function verifyWebhook(
  body: string | Uint8Array | ArrayBuffer,
  headers: WebhookHeaders,
  secret: string,
  options: VerifyWebhookOptions = {},
): Promise<WebhookEvent> {
  if (!secret) throw new TypeError("verifyWebhook requires a non-empty signing secret");
  const tolerance = options.toleranceSeconds ?? DEFAULT_WEBHOOK_TOLERANCE_SECONDS;
  const now = options.now ?? (() => Date.now() / 1000);
  const allowLegacy = options.allowLegacy ?? true;

  const bodyBytes =
    typeof body === "string" ? encoder.encode(body) : new Uint8Array(body);

  const v2 = getHeader(headers, "X-Waffle-Signature-V2");
  if (v2 !== undefined) {
    const { t, sigs } = parseV2(v2);
    const signed = concat(encoder.encode(`${t}.`), bodyBytes);
    if (!(await hmacMatches(secret, signed, sigs))) {
      throw new WebhookVerificationError("signature_mismatch", "signature does not match payload and secret");
    }
    const skew = Math.abs(Math.floor(now()) - t);
    if (skew > tolerance) {
      throw new WebhookVerificationError(
        "timestamp_out_of_tolerance",
        `timestamp is ${skew}s from now, tolerance is ${tolerance}s`,
      );
    }
  } else {
    const legacy = getHeader(headers, "X-Waffle-Signature");
    if (legacy === undefined || !allowLegacy) {
      throw new WebhookVerificationError(
        "missing_signature",
        legacy === undefined
          ? "no X-Waffle-Signature-V2 header"
          : "no X-Waffle-Signature-V2 header (legacy X-Waffle-Signature present but allowLegacy is false)",
      );
    }
    const m = /^sha256=(.*)$/.exec(legacy);
    const hex = m?.[1]?.trim() ?? "";
    if (!m || !HEX_SHA256.test(hex)) {
      throw new WebhookVerificationError("malformed_header", "X-Waffle-Signature: expected sha256=<64 hex chars>");
    }
    if (!(await hmacMatches(secret, bodyBytes, [hexToBytes(hex)]))) {
      throw new WebhookVerificationError("signature_mismatch", "signature does not match payload and secret");
    }
  }

  let raw: unknown;
  try {
    raw = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bodyBytes));
  } catch {
    throw new WebhookVerificationError("invalid_json", "body is not valid JSON");
  }
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) {
    throw new WebhookVerificationError("invalid_json", "body is not a JSON object");
  }
  const obj = raw as Record<string, unknown>;
  const str = (k: string) => (typeof obj[k] === "string" ? (obj[k] as string) : undefined);
  return {
    event: str("event"),
    eventId: str("event_id"),
    createdAt: str("created_at"),
    chargeId: str("charge_id"),
    payoutId: str("payout_id"),
    status: str("status"),
    amount: typeof obj.amount === "number" ? obj.amount : undefined,
    currency: str("currency"),
    mode: str("mode"),
    deliveryId: getHeader(headers, "X-Waffle-Delivery"),
    raw: obj,
  };
}
