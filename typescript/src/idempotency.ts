import { WaffleValidationError } from "./errors.js";

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

const KEY_PATTERN = /^[A-Za-z0-9._:-]{1,255}$/;

/** Throws {@link WaffleValidationError} unless `key` is 1..255 chars of `[A-Za-z0-9._:-]`. */
export function validateIdempotencyKey(key: unknown): asserts key is string {
  if (typeof key !== "string" || !KEY_PATTERN.test(key)) {
    throw new WaffleValidationError(
      "idempotencyKey must be 1-255 characters of [A-Za-z0-9._:-]",
    );
  }
}
