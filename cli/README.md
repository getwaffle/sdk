# waffle CLI

A developer CLI for the Waffle **merchant API** (the `:8080`
listener; see `docs/api-contract.md` in the main repo for the frozen
contract). Standalone Go module (`github.com/getwaffle/sdk/cli`),
sibling to `sdk/go`, `sdk/typescript`, `sdk/php`, and `sdk/mcp`. Every
money-moving call goes through `waffle-go` — the CLI is a cobra
front end over it plus a small private client for the dashboard-session
surface, never a second HTTP implementation.

All money amounts are integers in the currency's minor unit (IDR has no
minor-unit subdivision in waffle's model: `12500` **is** Rp12.500),
always `int64`, never `float64`. Human output renders IDR as `Rp49.360`;
`--json` always emits the API's raw response shape untouched.

## Install

```sh
curl -fsSL https://getwaffle.id/install.sh | sh
```

Downloads the latest release binary for your OS/arch (Linux/macOS;
Windows: grab a `.zip` from [this repo's
Releases](https://github.com/getwaffle/sdk/releases) instead) to
`~/.local/bin/waffle`. Override the install directory with
`WAFFLE_INSTALL_DIR=/somewhere/else`.

From this repo (local dev, builds from source instead):

```sh
go build -o waffle-cli ./cli   # the ../go replace directive applies
```

## Quick start

```sh
waffle login                      # email+password -> dashboard session
waffle keys create                # mint + store a sandbox API key
waffle balance
waffle charges create --amount 50000 --channel qris
```

Already hold a key? Skip login entirely:

```sh
waffle configure --api-key wf_sandbox_...
# or per-invocation:
WAFFLE_API_KEY=wf_sandbox_... waffle balance
waffle --api-key wf_sandbox_... balance
```

## The two credentials (never interchangeable)

Per the frozen contract, a dashboard **session token** can never create
a charge or payout, and an **API key** is the only credential the
money-moving surface accepts. The CLI keeps them strictly separate:

| Credential | Source | Authorizes |
|---|---|---|
| API key | `--api-key` flag > `WAFFLE_API_KEY` env > stored by `login`/`keys create`/`configure` | `balance`, `charges`, `bank-accounts`, `payouts`, `keys whoami` |
| Session token | stored by `login` | `whoami`, `keys list/create/revoke` (`/v1/merchants/me/*` only) |

Precedence for the base URL: `--base-url` flag > `WAFFLE_BASE_URL`
env > stored profile value > `https://api.getwaffle.id` (the SDK
default, production — pass `http://localhost:8080` for local dev).

## Scopes and presets

API keys carry a scope grant, independent of the dashboard session:
`charges:read`, `charges:write`, `payouts:read`, `payouts:write`,
`balance:read`. A `*:write` scope does **not** imply the matching
`*:read` scope. Keys are usually minted from a preset:

| Preset | Scopes |
|---|---|
| `read_only` | `charges:read`, `payouts:read`, `balance:read` |
| `accept_payments` | `charges:read`, `charges:write`, `balance:read` |
| `full` | all five |
| `custom` | whatever was explicitly granted |

`waffle keys whoami` shows the *configured* key's own mode/preset/scopes
(`GET /v1/whoami`, no scope required — this is different from top-level
`waffle whoami`, which shows the *dashboard session's* merchant account
via `GET /v1/merchants/me/profile`). A `403` from a scoped command names
the missing scope, e.g. `error: HTTP 403: this API key lacks the
charges:write permission`.

A `read_only` key can run `waffle charges get`, `charges list`,
`payouts get`, `payouts list`, `balance`, `bank-accounts get`, and `keys
whoami` — but not `charges create` or `payouts create` (those need the
`*:write` scopes `read_only` doesn't grant):

```sh
waffle --api-key sk_sandbox_readonly_xxx charges list --status paid
waffle --api-key sk_sandbox_readonly_xxx charges create --amount 1000
# error: HTTP 403: this API key lacks the charges:write permission
```

KYC and withdrawal-account management are **dashboard-only by design** —
there is no `bank-accounts register` command and never will be; see
"What the CLI does not do" below.

## Config file

`$XDG_CONFIG_HOME/waffle/config.json` (default
`~/.config/waffle/config.json`), mode `0600`, dir `0700`, written
atomically. Same plaintext-at-rest posture as the Stripe CLI, `gh`, and
aws-cli; an OS-keychain integration is a possible future enhancement,
deliberately not built now. Named profiles (`--profile prod`) are just
a top-level map — no separate credential-store subsystem.

A corrupt config file is an error, never silently reset — that would
destroy stored credentials.

## Commands

```
waffle login [--email x] [--api-key wf_...] [--open]   # session flow, or store a key directly
waffle logout                                          # clears session + key
waffle whoami                                          # GET /v1/merchants/me/profile (dashboard session)
waffle configure --api-key k [--base-url u] [--dashboard-url u]

waffle keys list                                       # session-authenticated
waffle keys create [--name label] [--no-store]         # sandbox-only (live keys are admin-issued)
waffle keys revoke <id>
waffle keys whoami                                     # GET /v1/whoami (API key) — mode/preset/scopes

waffle balance [--currency IDR]                        # withdrawable balance, scope balance:read
waffle banks list                                      # public bank directory, no auth

waffle charges create --amount 50000 [--currency IDR]  # scope charges:write
    [--channel qris|virtual_account] [--va-bank BCA]
    [--checkout-channel-selection merchant|payer]
    [--description ...] [--customer-ref ...] [--return-url ...]
    [--expires-in-minutes 60] [--metadata k=v ...]
    [--idempotency-key ...]                               # auto-generated when omitted
waffle charges calculate-fee --amount 100000 [--channel ...] [--va-bank ...]  # scope charges:read
waffle charges get <charge-id>                          # scope charges:read
waffle charges list [--status ...] [--limit N] [--starting-after ID]
    [--created-gte ...] [--created-lte ...] [--all]      # scope charges:read
waffle charges receipt <charge-id> [--output file.pdf]  # scope charges:read; 409 if not paid

waffle bank-accounts get                                # scope payouts:read; masked, read-only
                                                          # (registration is dashboard-only, no CLI command)

waffle payouts create --bank-account-id <uuid> --amount 20000 [--currency IDR]
    [--idempotency-key ...]                               # scope payouts:write
waffle payouts get <payout-id>                           # scope payouts:read
waffle payouts list [--status ...] [--limit N] [--starting-after ID] [--all]  # scope payouts:read
waffle payouts receipt <payout-id> [--output file.pdf]   # scope payouts:read; 409 if not completed

waffle healthz                                         # no auth

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
cd cli
go build ./... && go vet ./... && go test ./...
go build -o waffle-cli . && ./waffle-cli --help
```

The `replace github.com/getwaffle/sdk/go => ../go` directive
in `go.mod` is for local development against the sibling SDK; a real
release pins a version. Tests are `httptest`-backed — including the
cross-contamination guard that a session token is never sent where an
API key is expected, and vice versa. An end-to-end transcript against
the full local stack (`make start && make seed`) is in `e2e-proof.md`.
