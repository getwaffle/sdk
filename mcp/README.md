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

## Tools

Every write tool that needs an `Idempotency-Key`
(`waffle_create_charge`, `waffle_create_payout`) mints its own per
call — an LLM caller has no retry state of its own to key against, so
calling the tool twice creates two separate resources, never a
deduplicated retry. This is the same choice the merchant dashboard's own
"Buat link bayar" / "Tarik dana" quick-action forms make, for the same
reason.

| Tool | Maps to | Read-only | Notes |
| --- | --- | --- | --- |
| `waffle_list_banks` | `GET /v1/banks` | yes | Call first for a valid `bankCode`/`vaBank` — codes are not hardcodable. |
| `waffle_calculate_fee` | `POST /v1/fees/calculate` | yes | Preview only; no charge, no ledger write, no provider call. |
| `waffle_create_charge` | `POST /v1/charges` | no | Creates a payment link / QRIS / virtual-account charge. |
| `waffle_get_balance` | `GET /v1/balance` | yes | Withdrawable balance: settled paid charges minus non-failed payouts. |
| `waffle_get_bank_account` | `GET /v1/bank-accounts` | yes | Returns a recoverable hint (not a generic error) when none is registered yet — registration itself is dashboard-only, deliberately not a tool. |
| `waffle_create_payout` | `POST /v1/payouts` | no | Withdraws to the registered bank account. |
| `waffle_healthz` | `GET /healthz` | yes | No auth. |

There is no `provider` argument on any tool: Waffle always auto-routes
a charge or payout to the merchant's highest-priority connected PSP, and
which PSP handled it is never surfaced — see `docs/BUILD_PLAN.md`'s
white-label rule.

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
larger MCP server or for in-process testing (see `src/server.test.ts`):

```ts
import { createServer } from "@waffle/mcp";

const server = createServer({ apiKey: process.env.WAFFLE_API_KEY! });
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
