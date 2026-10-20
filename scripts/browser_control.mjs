#!/usr/bin/env node
// Local stdio control of an owner-approved, already-running Chrome session.
// No HTTP listener, profile copying, tracing, or credential export.
import { access } from 'node:fs/promises';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
import { Client } from '../web/node_modules/@modelcontextprotocol/client/dist/index.mjs';
import { StdioClientTransport } from '../web/node_modules/@modelcontextprotocol/client/dist/stdio.mjs';

const binary = join(homedir(), '.local/lib/code-factory/browser-tools/node_modules/chrome-devtools-mcp/build/src/bin/chrome-devtools-mcp.js');
await access(binary);
const transport = new StdioClientTransport({
  command: process.execPath,
  args: [binary, '--autoConnect', '--no-usage-statistics', '--no-performance-crux', '--redact-network-headers'],
  stderr: 'pipe',
});
// Upstream error text can contain complete URLs; do not emit it automatically.
transport.stderr?.resume();
const client = new Client({ name: 'code-factory-browser-acceptance', version: '1' });
await client.connect(transport);
const tools = (await client.listTools()).tools;
const permitted = new Set(['list_pages', 'select_page', 'new_page', 'navigate_page',
  'take_snapshot', 'click', 'fill', 'fill_form', 'press_key', 'wait_for', 'evaluate_script']);
process.stdout.write(JSON.stringify({ ready: true, attached: false,
  tools: tools.filter(t => permitted.has(t.name)).map(t => ({ name: t.name, inputSchema: t.inputSchema })) }) + '\n');
const input = createInterface({ input: process.stdin, terminal: false });
try {
  for await (const line of input) {
    let request;
    try {
      if (line.length > 65536) throw new Error('Input limit');
      request = JSON.parse(line);
      if (request.name === 'exit') break;
      if (!permitted.has(request.name)) throw new Error('Tool not permitted');
      const result = await client.callTool({ name: request.name, arguments: request.arguments ?? {} }, undefined, { timeout: 120000 });
      process.stdout.write(JSON.stringify({ id: request.id, result }) + '\n');
    } catch {
      process.stdout.write(JSON.stringify({ id: request?.id, failed: true,
        reason: 'Browser operation failed; check Chrome remote-debugging switch and native permission prompt.' }) + '\n');
    }
  }
} finally {
  await client.close();
}
