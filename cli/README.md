# paybridge CLI

A developer CLI for the Paybridge **merchant API** (the `:8080`
listener; see `docs/api-contract.md` in the main repo for the frozen
contract). Standalone Go module (`github.com/very-good-labs/paybridge-cli`),
sibling to `sdk/go`, `sdk/typescript`, `sdk/php`, and `sdk/mcp`. Every
money-moving call goes through `paybridge-go` — the CLI is a cobra
front end over it plus a small private client for the dashboard-session
surface, never a second HTTP implementation.

All money amounts are integers in the currency's minor unit (IDR has no
minor-unit subdivision in paybridge's model: `12500` **is** Rp12.500),
always `int64`, never `float64`. Human output renders IDR as `Rp49.360`;
`--json` always emits the API's raw response shape untouched.

## Install

From this repo (local dev):

```sh
go build -o paybridge-cli ./sdk/cli   # the ../go replace directive applies
```

## Quick start

```sh
paybridge login                      # email+password -> dashboard session
paybridge keys create                # mint + store a sandbox API key
paybridge balance
paybridge charges create --amount 50000 --channel qris
```

Already hold a key? Skip login entirely:

```sh
paybridge configure --api-key sk_sandbox_...
# or per-invocation:
PAYBRIDGE_API_KEY=sk_sandbox_... paybridge balance
paybridge --api-key sk_sandbox_... balance
```

## The two credentials (never interchangeable)

Per the frozen contract, a dashboard **session token** can never create
a charge or payout, and an **API key** is the only credential the
money-moving surface accepts. The CLI keeps them strictly separate:

| Credential | Source | Authorizes |
|---|---|---|
| API key | `--api-key` flag > `PAYBRIDGE_API_KEY` env > stored by `login`/`keys create`/`configure` | `balance`, `charges`, `bank-accounts`, `payouts` |
| Session token | stored by `login` | `whoami`, `keys` (`/v1/merchants/me/*` only) |

Precedence for the base URL: `--base-url` flag > `PAYBRIDGE_BASE_URL`
env > stored profile value > `http://localhost:8080` (the SDK default).

## Config file

`$XDG_CONFIG_HOME/paybridge/config.json` (default
`~/.config/paybridge/config.json`), mode `0600`, dir `0700`, written
atomically. Same plaintext-at-rest posture as the Stripe CLI, `gh`, and
aws-cli; an OS-keychain integration is a possible future enhancement,
deliberately not built now. Named profiles (`--profile prod`) are just
a top-level map — no separate credential-store subsystem.

A corrupt config file is an error, never silently reset — that would
destroy stored credentials.

## Commands

```
paybridge login [--email x] [--api-key sk_...] [--open]   # session flow, or store a key directly
paybridge logout                                          # clears session + key
paybridge whoami                                          # GET /v1/merchants/me/profile
paybridge configure --api-key k [--base-url u] [--dashboard-url u]

paybridge keys list                                       # session-authenticated
paybridge keys create [--name label] [--no-store]         # sandbox-only (live keys are admin-issued)
paybridge keys revoke <id>

paybridge balance [--currency IDR]                        # withdrawable balance
paybridge banks list                                      # public bank directory

paybridge charges create --amount 50000 [--currency IDR]
    [--channel qris|virtual_account] [--va-bank BCA]
    [--description ...] [--customer-ref ...] [--return-url ...]
    [--expires-in-minutes 60] [--metadata k=v ...]
    [--idempotency-key ...]                               # auto-generated when omitted
paybridge charges calculate-fee --amount 100000 [--channel ...] [--va-bank ...]

paybridge bank-accounts register --bank-code BCA --account-number ... --account-holder-name ...
paybridge bank-accounts get

paybridge payouts create --bank-account-id <uuid> --amount 20000 [--currency IDR]
    [--idempotency-key ...]

paybridge healthz                                         # no auth

Global flags: --api-key, --base-url, --json, --profile <name>
```

### What the CLI does *not* do

- No browser device-pairing flow: that would need a new
  unauthenticated credential-minting endpoint on the backend. `login`
  plus `keys create` covers the same path with zero new attack surface.
- No admin-API coverage (`:8081`), no live-key minting (admin-only by
  design), no KYC submission / webhook registration / fee-rule
  management — those stay dashboard surfaces.

## Idempotency keys

`charges create` and `payouts create` require an `Idempotency-Key` per
the contract. The CLI generates one per invocation when you don't pass
`--idempotency-key` (the CLI keeps no retry state of its own, same
reasoning as the MCP server) and prints the key it used, so a scripted
retry can pin the same key. Pass your own (e.g. your internal order id)
when you want retry-safety across CLI runs.

## Error handling

Non-2xx responses print as `error: HTTP <status>: <message>` where
`<message>` is the server's `{"error": ...}` body, and the process exits
`1`. `422` is a business rejection (insufficient balance, no fee rule,
zero connected PSPs), `429` a rate/fraud-velocity limit (retry with
backoff), `401` a bad/missing credential. Missing local credentials
produce an actionable hint naming the exact command or env var to fix it.

## Development

```sh
cd sdk/cli
go build ./... && go vet ./... && go test ./...
go build -o paybridge-cli . && ./paybridge-cli --help
```

The `replace github.com/very-good-labs/paybridge-go => ../go` directive
in `go.mod` is for local development against the sibling SDK; a real
release pins a version. Tests are `httptest`-backed — including the
cross-contamination guard that a session token is never sent where an
API key is expected, and vice versa. An end-to-end transcript against
the full local stack (`make start && make seed`) is in `e2e-proof.md`.
