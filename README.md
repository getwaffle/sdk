# Waffle SDKs

Official client libraries for [Waffle](https://getwaffle.id), a payment
infrastructure/orchestration platform. Extracted from the main (private)
backend monorepo — full history preserved via `git subtree split` — so
merchants can install/depend on these without needing access to the
backend repo.

| Language | Package | Path |
|---|---|---|
| Go | `github.com/getwaffle/sdk/go` | [`go/`](go/) |
| TypeScript | [`@waffle/sdk`](https://www.npmjs.com/package/@waffle/sdk) | [`typescript/`](typescript/) |
| PHP | [`waffle/sdk`](https://packagist.org/packages/waffle/sdk) | [`php/`](php/) |
| MCP server | [`@waffle/mcp`](https://www.npmjs.com/package/@waffle/mcp) | [`mcp/`](mcp/) |
| CLI | `github.com/getwaffle/sdk/cli` | [`cli/`](cli/) |

Each subdirectory is independently versioned/published — see its own
README for install and usage instructions. API reference:
[docs.getwaffle.id](https://getwaffle.id/docs) (`docs/api-contract.md` in
the backend repo is the ground-truth contract these SDKs implement).

## Releasing

- **Go / CLI**: tag a release (`go/v1.2.3`, `cli/v1.2.3`) — no publish step,
  `go get` resolves directly from this repo.
- **TypeScript / MCP**: `.github/workflows/publish-npm.yml`, triggered by a
  push to `main` that bumps the respective `package.json` version.
- **PHP**: Packagist auto-updates via its GitHub webhook on every push to
  `main` (no separate publish step once the package is registered on
  packagist.org).
