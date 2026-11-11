import type { App } from "@modelcontextprotocol/ext-apps";

class ConnectionError extends Error {
  constructor() {
    super(
      "Connection to Code Factory interrupted; request outcome may be unknown. Inspect saved state before retrying.",
    );
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
  if (/\boutcome_unknown\b|\bHTTP[\s:(]+503\b/i.test(message)) return true;
  return /connection failed|connection (?:closed|reset)|not connected|network (?:error|request failed)|failed to fetch|fetch failed|socket hang up|request timed out|transport closed/i.test(
    message,
  );
}
async function createApp() {
  const { App } = await import("@modelcontextprotocol/ext-apps");
  const instance = new App({ name: "Code Factory", version: "0.1.0" }, {});
  instance.onhostcontextchanged = (ctx) => {
    if (ctx.theme) document.documentElement.dataset.theme = ctx.theme;
  };
  return instance;
}
let app: App;
let connected: Promise<void> | undefined;
export function connect() {
  if (!connected) {
    connected = createApp()
      .then(async (instance) => {
        app = instance;
        await instance.connect(undefined, { timeout: 10000 });
        const ctx = instance.getHostContext();
        if (ctx?.theme) document.documentElement.dataset.theme = ctx.theme;
      })
      .catch((error: unknown) => {
        // A rejected initialization must not poison this document forever. The SDK
        // closes a failed connection; a later attempt needs a fresh App instance.
        connected = undefined;
        throw transientTransport(error) ? new ConnectionError() : error;
      });
  }
  return connected;
}

// Only explicitly known reads may be repeated. A lost mutation response can
// already represent accepted work, so Start/Send/answers/saves are never retried.
const retryableReads = new Set([
  "factory_state",
  "factory_item",
  "factory_events",
  "session_questions",
  "session_transcript",
  "inspect_configuration",
  "open_factory",
  "open_settings",
  "diagnostics",
  "operation_result",
]);
async function request<T>(
  name: string,
  args: Record<string, unknown> = {},
): Promise<ToolResult<T>> {
  for (let attempt = 0; ; attempt++) {
    try {
      await connect();
      // Some hosts memoize identical read arguments, including an empty journal
      // page. A fresh read must reach the server even when its cursor is unchanged.
      // Mutation arguments stay exact and are never automatically repeated.
      const readArgs = retryableReads.has(name)
        ? { ...args, freshness: crypto.randomUUID() }
        : args;
      const result = await app.callServerTool({ name, arguments: readArgs });
      if (result.isError)
        throw new ToolError(
          result.content?.map((c) => ("text" in c ? c.text : "")).join("\n") ||
            "Operation failed",
        );
      const value = result.structuredContent as ToolResult<T> | undefined;
      if (!value)
        throw new Error("The host returned no data. Reconnect Code Factory.");
      return value;
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

type ToolResult<T> = {
  data?: T;
  operation?: { id: string; tool: string; state: "running" };
};

export async function call<T>(
  name: string,
  args: Record<string, unknown> = {},
): Promise<T> {
  let result = await request<T>(name, args);
  const accepted = result.operation;
  while (accepted && result.operation) {
    const id = accepted.id;
    await new Promise((resolve) => setTimeout(resolve, 100));
    // Only this read is retryable. In particular, losing its response must
    // never resend the original message, approval or configuration change.
    try {
      result = await request<T>("operation_result", { id });
    } catch (error) {
      // An accepted action still owns this wait and its draft during an outage.
      // Keep the known identity after a read's retry budget is exhausted.
      if (!isConnectionFailure(error)) throw error;
    }
  }
  if (!("data" in result))
    throw new Error("The host returned no operation result.");
  return result.data as T;
}

export async function openLink(url: string) {
  if (!/^https?:\/\//.test(url)) throw new Error("Unsupported URL");
  await connect();
  return app.openLink({ url });
}
