# @waffle/mcp

An [MCP](https://modelcontextprotocol.io) server that exposes the
Waffle **merchant API** (the `:8080` listener; see
`docs/api-contract.md` in the main repo) as tools an AI coding agent can
call directly — create a test charge, check a withdrawable balance,
withdraw to an already-registered bank account, preview a fee — the
same surface [`@waffle/sdk`](../typescript) wraps for application code,
not the admin API, not the dashboard-session-authenticated
`/v1/merchants/me/*` routes, and not KYC/onboarding/branding — this
server is deliberately scoped to money-movement only. Registering (or
replacing) a withdrawal bank account is intentionally **not** a tool
here, and never will be: it is dashboard-only, behind a human — an
`Authorization` header (which is all an LLM host ever holds) must never
be enough on its own to redirect where a merchant's payouts go.

It speaks **stdio**: an MCP host (Claude Code, Cursor, VS Code Copilot,
...) launches it as a local child process per the [Model Context
Protocol](https://modelcontextprotocol.io) specification. It is not a
`docker-compose.yml` service — nothing else in the stack talks to it over
the network.

## Install and register with a host

No global install needed; every host below launches it with `npx`.

### Claude Code

```sh
claude mcp add waffle -e WAFFLE_API_KEY=sk_sandbox_your_key -- npx -y @waffle/mcp
```

### Cursor / VS Code (`.cursor/mcp.json` / `.vscode/mcp.json`)

```json
{
  "mcpServers": {
    "waffle": {
      "command": "npx",
      "args": ["-y", "@waffle/mcp"],
      "env": {
        "WAFFLE_API_KEY": "sk_sandbox_your_key"
      }
    }
  }
}
```

(VS Code's `mcp.json` uses a `"servers"` key instead of `"mcpServers"`;
the `command`/`args`/`env` shape is otherwise identical.)

## Configuration

| Env var | Required | Default | Notes |
| --- | --- | --- | --- |
| `WAFFLE_API_KEY` | yes | — | Sent as `Authorization: Bearer <key>` on every request. The key's mode (`live` vs `sandbox`) is fixed **server-side** at issuance — a sandbox key can never move real money no matter what a tool call asks for. Get one from the merchant dashboard's Integrations page, or `POST /v1/merchants/register` for a fresh sandbox key. |
| `WAFFLE_BASE_URL` | no | `https://api.getwaffle.id` | Set to `http://localhost:8080` for local dev against `docker-compose.yml`. |

The process exits immediately with a one-line stderr message if
`WAFFLE_API_KEY` is unset — a host sees a failed launch, not a hang.

## Startup scope-gating

On start, this server calls `whoami()` once with the configured key and
registers **only** the tools that key's scopes allow — never zero tools,
never every tool regardless of what the key can actually do. If
`whoami()` fails (bad key, unreachable API), the process fails loudly
(non-zero exit, stderr message) rather than silently starting with no
tools or with every tool.

| Scope | Registers |
| --- | --- |
| `charges:read` | `waffle_get_charge`, `waffle_list_charges`, `waffle_get_charge_receipt`, `waffle_calculate_fee` |
| `charges:write` | `waffle_create_charge` |
| `payouts:read` | `waffle_get_payout`, `waffle_list_payouts`, `waffle_get_bank_account` |
| `payouts:write` | `waffle_create_payout` |
| `balance:read` | `waffle_get_balance` |

`waffle_list_banks`, `waffle_whoami`, and `waffle_healthz` need no scope
and are always registered.

A `read_only`-preset key (`charges:read` + `payouts:read` +
`balance:read`) sees `waffle_get_charge`, `waffle_list_charges`,
`waffle_get_charge_receipt`, `waffle_calculate_fee`,
`waffle_get_payout`, `waffle_list_payouts`, `waffle_get_bank_account`,
`waffle_get_balance`, `waffle_list_banks`, `waffle_whoami`, and
`waffle_healthz` — **never** `waffle_create_charge` or
`waffle_create_payout`. The calling model can't even see those tools
exist, let alone attempt (and 403 on) calling them.

KYC, bank-account registration, team/member management, and other
account settings are **dashboard-only by design** — there is
intentionally no tool for any of them, scopes notwithstanding; the
merchant dashboard is the only place to perform onboarding/KYC and
manage the account.

## Tools

Every write tool that needs an `Idempotency-Key`
(`waffle_create_charge`, `waffle_create_payout`) mints its own per
call — an LLM caller has no retry state of its own to key against, so
calling the tool twice creates two separate resources, never a
deduplicated retry. This is the same choice the merchant dashboard's own
"Buat link bayar" / "Tarik dana" quick-action forms make, for the same
reason. Both also require an explicit `confirm: true` argument — the
call is refused (without touching the API) if it's false or omitted —
and their tool descriptions state the configured key's mode (`LIVE` /
`SANDBOX`) so the calling model knows whether the action moves real
money before it confirms.

| Tool | Maps to | Read-only | Scope | Notes |
| --- | --- | --- | --- | --- |
| `waffle_whoami` | `GET /v1/whoami` | yes | none | Merchant, mode, preset, scopes for the configured key. |
| `waffle_healthz` | `GET /healthz` | yes | none | No auth. |
| `waffle_list_banks` | `GET /v1/banks` | yes | none | Call first for a valid `bankCode`/`vaBank` — codes are not hardcodable. |
| `waffle_get_charge` | `GET /v1/charges/{id}` | yes | `charges:read` | 404s if the charge doesn't exist or isn't this merchant's. |
| `waffle_list_charges` | `GET /v1/charges` | yes | `charges:read` | Cursor-paginated, filterable by status/created-at. |
| `waffle_get_charge_receipt` | `GET /v1/charges/{id}/receipt.pdf` | yes | `charges:read` | Confirms the receipt exists and returns its path — never the raw PDF bytes, since those aren't for a model to read. 409s if the charge isn't paid yet. |
| `waffle_calculate_fee` | `POST /v1/fees/calculate` | yes | `charges:read` | Preview only; no charge, no ledger write, no provider call. |
| `waffle_create_charge` | `POST /v1/charges` | no | `charges:write` | Creates a payment link / QRIS / virtual-account charge. Requires `confirm: true`. |
| `waffle_get_payout` | `GET /v1/payouts/{id}` | yes | `payouts:read` | 404s if the payout doesn't exist or isn't this merchant's. |
| `waffle_list_payouts` | `GET /v1/payouts` | yes | `payouts:read` | Cursor-paginated, filterable by status/created-at. |
| `waffle_get_bank_account` | `GET /v1/bank-accounts` | yes | `payouts:read` | Returns a recoverable hint (not a generic error) when none is registered yet — registration itself is dashboard-only, deliberately not a tool. |
| `waffle_create_payout` | `POST /v1/payouts` | no | `payouts:write` | Withdraws to the registered bank account. Requires `confirm: true`. |
| `waffle_get_balance` | `GET /v1/balance` | yes | `balance:read` | Withdrawable balance: settled paid charges minus non-failed payouts. |

There is no `provider` argument on any tool: Waffle always auto-routes
a charge or payout to the merchant's highest-priority connected PSP, and
which PSP handled it is never surfaced — see `docs/BUILD_PLAN.md`'s
white-label rule. There is also no `waffle_get_payout_receipt` tool —
the SDK's `getPayoutReceipt` deliberately isn't exposed here, to keep
this server's surface to the tools above only.

## Errors

A non-2xx API response never throws a protocol-level error out of a tool
call — it comes back as an ordinary MCP tool result with `isError: true`
and a `text` block reading `Waffle API error (HTTP <status>):
<message>`, so the calling model can read the failure and decide whether
to retry, ask for different arguments, or give up. This mirrors
`@waffle/sdk`'s `WaffleError` (`status` + `message`), just rendered
as text instead of a thrown exception, since a tool handler's only error
channel is its result content.

## Use as a library

`createServer` is also exported for embedding this tool set into a
larger MCP server or for in-process testing (see `src/server.test.ts`).
It's `async` — it calls `whoami()` before returning so it can scope-gate
which tools get registered — so `await` it (or handle the rejection if
the key is bad):

```ts
import { createServer } from "@waffle/mcp";

const server = await createServer({ apiKey: process.env.WAFFLE_API_KEY! });
```

## Local development

`@waffle/sdk` is a `file:../typescript` dependency — this repo has no
workspace tool (no pnpm/npm workspaces), so build it first, once:

```sh
(cd ../typescript && npm install && npm run build)
npm install
npm run dev   # runs src/index.ts directly with tsx
npx @modelcontextprotocol/inspector npm run dev   # exercise every tool from a local web UI
```

Re-run the first line whenever `../typescript/src` changes — `npm
install` here does **not** rebuild it automatically (deliberately: a
nested `npm install` inside a lifecycle script inherits the parent's
`npm_config_local_prefix` and silently targets the wrong directory, so
this stays a manual, explicit step rather than a fragile one).

```sh
npm run typecheck
npm test
npm run build
```
