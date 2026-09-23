/**
 * Generates a fresh idempotency key for `createCharge`/`createPayout`
 * using `crypto.randomUUID()`. Both methods call this automatically when
 * the caller omits `idempotencyKey`; call it yourself only if you want to
 * control the key explicitly (e.g. to retry the exact same charge/payout
 * attempt — retrying with the same key returns the original resource
 * rather than creating a duplicate).
 */
export function generateIdempotencyKey(): string {
  return crypto.randomUUID();
}
