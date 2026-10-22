import { App } from "@modelcontextprotocol/ext-apps";

class ConnectionError extends Error {
  constructor() {
    super("Connection to Code Factory interrupted.");
    this.name = "ConnectionError";
  }
}
class ToolError extends Error {}

export function isConnectionFailure(error: unknown): boolean {
  return error instanceof ConnectionError;
}
function transientTransport(error: unknown): boolean {
  // The host can wrap its own MCP transport failure in a tool-result envelope.
  // Match that exact host exception, never arbitrary business-error wording.
  if (error instanceof ToolError)
    return /^(?:Error:\s*)?McpServerError:\s*Connection failed\.?$/i.test(
      error.message.trim(),
    );
  if (isConnectionFailure(error)) return true;
  const message = error instanceof Error ? error.message : String(error);
  return /connection failed|connection (?:closed|reset)|not connected|network (?:error|request failed)|failed to fetch|fetch failed|socket hang up|request timed out|transport closed/i.test(
    message,
  );
}
function createApp() {
  const instance = new App({ name: "Code Factory", version: "0.1.0" }, {});
  instance.onhostcontextchanged = (ctx) => {
    if (ctx.theme) document.documentElement.dataset.theme = ctx.theme;
  };
  return instance;
}
let app = createApp();
let connected: Promise<void> | undefined;
export function connect() {
  if (!connected) {
    const instance = app;
    connected = instance
      .connect(undefined, { timeout: 10000 })
      .then(() => {
        const ctx = instance.getHostContext();
        if (ctx?.theme) document.documentElement.dataset.theme = ctx.theme;
      })
      .catch((error: unknown) => {
        // A rejected initialization must not poison this document forever. The SDK
        // closes a failed connection; a later attempt needs a fresh App instance.
        if (app === instance) {
          connected = undefined;
          app = createApp();
        }
        throw transientTransport(error) ? new ConnectionError() : error;
      });
  }
  return connected;
}

// Only explicitly known reads may be repeated. A lost mutation response can
// already represent accepted work, so Start/Send/answers/saves are never retried.
const retryableReads = new Set([
  "open_factory",
  "open_settings",
  "read_snapshot",
  "read_changes",
  "read_item",
  "read_run",
  "read_transcript",
  "list_runs",
  "get_settings",
]);
export async function call<T>(
  name: string,
  args: Record<string, unknown> = {},
): Promise<T> {
  for (let attempt = 0; ; attempt++) {
    try {
      await connect();
      const result = await app.callServerTool({ name, arguments: args });
      if (result.isError)
        throw new ToolError(
          result.content?.map((c) => ("text" in c ? c.text : "")).join("\n") ||
            "Operation failed",
        );
      const value = result.structuredContent as { data: T } | undefined;
      if (!value)
        throw new Error("The host returned no data. Reconnect Code Factory.");
      return value.data;
    } catch (error) {
      if (!transientTransport(error)) throw error;
      if (!retryableReads.has(name) || attempt >= 2)
        throw new ConnectionError();
      await new Promise((resolve) =>
        setTimeout(resolve, attempt === 0 ? 250 : 750),
      );
    }
  }
}

export async function openLink(url: string) {
  if (!/^https?:\/\//.test(url)) throw new Error("Unsupported URL");
  await connect();
  return app.openLink({ url });
}
