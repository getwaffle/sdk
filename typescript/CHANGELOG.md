# Changelog

## Unreleased

### Security
- **BREAKING (behaviour):** `verifyWebhook` now defaults `allowLegacy` to `false`. A delivery carrying only the legacy `X-Waffle-Signature` header is rejected with `missing_signature`. Pass `{ allowLegacy: true }` to opt back in (this disables replay protection).
- Every request now has a timeout (`timeoutMs`, default 30000) and an optional `signal`. Timeouts throw `WaffleTimeoutError`; a timed-out POST has an unknown outcome, so retry with the same idempotency key.
- Every request uses `redirect: "error"`; redirects are never followed.
- `baseUrl` is validated: https only (http for localhost, 127.0.0.1, [::1]), no userinfo/query/fragment, and no path (normalised to `scheme://host[:port]`; behind a gateway path prefix use a custom `fetch`). Invalid values throw from the constructor.
- Response bodies are capped (8 MiB JSON/text, 16 MiB receipt PDFs); exceeding it throws `WaffleResponseTooLargeError`.
- Server error messages are truncated to 512 characters and control/format characters are replaced with spaces.
- The API key is held in a private field and redacted from `JSON.stringify` and `util.inspect`.
- `createCharge`, `createPayout` and `calculateFee` reject amounts that are not positive safe integers; caller-supplied idempotency keys must match `^[A-Za-z0-9._:-]{1,255}$` (`WaffleValidationError`).

- `verifyWebhook` throws `TypeError` (fail closed) unless `toleranceSeconds` is a finite number >= 0 and `now()` returns a finite number; previously `NaN` silently disabled the timestamp check.

### Added
- Exports: `WaffleTimeoutError`, `WaffleResponseTooLargeError`, `WaffleValidationError`, `validateIdempotencyKey`, `normalizeBaseUrl`, `DEFAULT_TIMEOUT_MS`.
- README: warning against bundling secret keys in browser code; documentation of the options above.
