#!/usr/bin/env node
import { serveStdio } from "@modelcontextprotocol/server/stdio";
import { createServer } from "./server.js";

const apiKey = process.env["PAYBRIDGE_API_KEY"];
if (!apiKey) {
  console.error(
    'paybridge-mcp: PAYBRIDGE_API_KEY is not set. Get a key from the Paybridge merchant dashboard (Integrations -> API keys) and pass it in your MCP host\'s server config, e.g.:\n' +
      '  { "command": "npx", "args": ["-y", "@paybridge/mcp"], "env": { "PAYBRIDGE_API_KEY": "sk_sandbox_..." } }',
  );
  process.exit(1);
}

const baseUrl = process.env["PAYBRIDGE_BASE_URL"];

void serveStdio(() => createServer({ apiKey, ...(baseUrl !== undefined ? { baseUrl } : {}) }));
console.error(`paybridge MCP server running on stdio (base URL: ${baseUrl ?? "http://localhost:8080 (default)"})`);
