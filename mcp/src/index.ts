#!/usr/bin/env node
import { serveStdio } from "@modelcontextprotocol/server/stdio";
import { createServer } from "./server.js";
import { validateBaseUrl } from "./safety.js";

const apiKey = process.env["WAFFLE_API_KEY"];
if (!apiKey) {
  console.error(
    'waffle-mcp: WAFFLE_API_KEY is not set. Get a key from the Waffle merchant dashboard (Integrations -> API keys) and pass it in your MCP host\'s server config, e.g.:\n' +
      '  { "command": "npx", "args": ["-y", "@getwaffle/mcp"], "env": { "WAFFLE_API_KEY": "wf_sandbox_..." } }',
  );
  process.exit(1);
}

// stdout is the MCP protocol channel: everything human-readable goes to stderr, and the key is never printed.
let baseUrl: string | undefined;
try {
  const rawBaseUrl = process.env["WAFFLE_BASE_URL"];
  baseUrl = rawBaseUrl !== undefined && rawBaseUrl !== "" ? validateBaseUrl(rawBaseUrl) : undefined;
} catch (err) {
  console.error(err instanceof Error ? err.message : String(err));
  process.exit(1);
}

// Built eagerly (not inside the serveStdio factory) so a bad key or an
// unreachable API fails the process loudly at startup, before it ever
// accepts a client connection — never silently serving zero or all tools.
let server;
try {
  server = await createServer({ apiKey, ...(baseUrl !== undefined ? { baseUrl } : {}) });
} catch (err) {
  console.error(err instanceof Error ? err.message : String(err));
  process.exit(1);
}

void serveStdio(() => server);
console.error(`waffle MCP server running on stdio (base URL: ${baseUrl ?? "https://api.getwaffle.id (default)"})`);
