/**
 * Generates a fresh idempotency key for `createCharge`/`createPayout`
 * using `crypto.randomUUID()`. The SDK never generates these
 * automatically inside request calls — callers must pass an explicit
 * key so they retain control over retry semantics (retrying the same
 * key returns the original charge/payout rather than creating a
 * duplicate).
 */
export function generateIdempotencyKey(): string {
  return crypto.randomUUID();
}
