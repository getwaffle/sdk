/**
 * Thrown for every non-2xx response from the Waffle merchant API.
 *
 * Per the frozen API contract every non-2xx body is `{ "error": "message" }`
 * with the HTTP status code as the only machine-readable signal (there is
 * no error-code field). Callers should branch on `status`:
 *
 *   - 400: malformed request body / missing required field or header.
 *   - 401: missing or invalid API key.
 *   - 403: the API key's scopes don't cover this operation — thrown as the
 *          more specific {@link WafflePermissionError} subclass, which
 *          `instanceof WaffleError` still matches.
 *   - 404: the requested resource doesn't exist (or doesn't belong to this
 *          merchant).
 *   - 409: the resource exists but isn't in a state that allows this
 *          operation (e.g. a receipt requested for a charge that hasn't
 *          been paid yet).
 *   - 422: well-formed request rejected by business logic (e.g. no fee
 *          rule configured, insufficient balance, unknown provider).
 *   - 429: rate limit (per-IP/per-merchant) or fraud velocity limit on
 *          `POST /v1/charges`. No `Retry-After` header exists today —
 *          retry with backoff.
 *   - 500: internal error.
 */
export class WaffleError extends Error {
  /** HTTP status code of the failed response. */
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "WaffleError";
    this.status = status;
    Object.setPrototypeOf(this, WaffleError.prototype);
  }
}

/**
 * Matches the literal shape of a scope-related 403 body, e.g.
 * `"this API key lacks the payouts:write permission"`. Exported so callers
 * that inspect raw error bodies themselves (rather than catching
 * {@link WafflePermissionError}) can reuse the same pattern.
 */
export const PERMISSION_ERROR_PATTERN = /lacks the (\S+) permission/;

/**
 * Thrown instead of the base {@link WaffleError} for 403 responses caused
 * by a missing API key scope (as opposed to some other 403 cause). Callers
 * can `instanceof WafflePermissionError`-check and read `.scope` off it
 * without parsing the message themselves:
 *
 * ```ts
 * try {
 *   await client.createPayout(params, key);
 * } catch (err) {
 *   if (err instanceof WafflePermissionError) {
 *     console.error(`API key is missing scope: ${err.scope}`);
 *   }
 *   throw err;
 * }
 * ```
 */
export class WafflePermissionError extends WaffleError {
  /**
   * The missing scope, e.g. `"payouts:write"`. Undefined only if the 403
   * body didn't match the expected `"lacks the <scope> permission"` shape
   * (still thrown as a `WafflePermissionError`, since the status is 403,
   * but with `scope` unset).
   */
  readonly scope: string | undefined;

  constructor(message: string, scope?: string) {
    super(403, message);
    this.name = "WafflePermissionError";
    this.scope = scope;
    Object.setPrototypeOf(this, WafflePermissionError.prototype);
  }
}

/**
 * Builds either a {@link WafflePermissionError} (for a 403 whose message
 * matches the missing-scope pattern) or a plain {@link WaffleError}. Used
 * internally by every request path so all methods raise permission errors
 * consistently.
 */
export function errorFromResponse(status: number, message: string): WaffleError {
  if (status === 403) {
    const match = PERMISSION_ERROR_PATTERN.exec(message);
    if (match) return new WafflePermissionError(message, match[1]);
  }
  return new WaffleError(status, message);
}
