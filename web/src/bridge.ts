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

type Response = {
  apiVersion: 2;
  instanceId: string;
  response: {
    state:
      | "ready"
      | "pending"
      | "busy"
      | "queued"
      | "running"
      | "waiting"
      | "succeeded"
      | "failed"
      | "uncertain"
      | "lost";
    requestId?: string;
    instanceId?: string;
    value?: unknown;
    result?: unknown;
    error?: string;
    retryAt?: string;
    retryReason?: string;
    resultUnavailable?: boolean;
    errorUnavailable?: boolean;
    [key: string]: unknown;
  };
};
type CallOptions = {
  signal?: AbortSignal;
  onPending?: () => void;
  onProgress?: (
    state: string,
    retry?: { retryAt?: string; retryReason?: string },
  ) => void;
  onInstance?: (instanceId: string) => void;
  expectedInstanceId?: string;
};
const operationTools = new Set([
  "session_chat",
  "configuration_chat",
  "session_control",
  "answer_session_question",
  "resolve_session_delivery",
  "inspect_configuration",
  "propose_configuration",
  "confirm_configuration",
  "user_action",
  "link_items",
  "sync_repositories",
  "diagnostics",
]);
const inFlight = new Map<string, Promise<Response>>();
let instanceId: string | undefined;
let generation = 0;
const instanceListeners = new Set<(instanceId: string) => void>();
export function onInstanceChange(listener: (instanceId: string) => void) {
  instanceListeners.add(listener);
  return () => {
    instanceListeners.delete(listener);
  };
}
function observeInstance(next: string) {
  if (instanceId === next) return;
  const previous = instanceId;
  instanceId = next;
  // First adoption also retires requests dispatched while identity was unknown.
  generation++;
  inFlight.clear();
  if (!previous) return;
  for (const listener of instanceListeners) listener(next);
}
function cancelled(options: CallOptions) {
  options.signal?.throwIfAborted();
}
async function pause(options: CallOptions) {
  cancelled(options);
  await new Promise((resolve) =>
    setTimeout(resolve, document.hidden ? 10000 : 250),
  );
  cancelled(options);
}
async function wire(
  name: string,
  args: Record<string, unknown>,
  retry: boolean,
): Promise<Response> {
  // Coalesce identical polls across effects; each caller retains its own cancellation.
  const key = JSON.stringify([name, args, generation]);
  const existing = inFlight.get(key);
  if (existing) return existing;
  const work = (async () => {
    for (let attempt = 0; ; attempt++) {
      try {
        await connect();
        const dispatchedGeneration = generation;
        const arguments_ = operationTools.has(name)
          ? args
          : { ...args, freshness: crypto.randomUUID() };
        const result = await app.callServerTool({
          name,
          arguments: arguments_,
        });
        if (result.isError) {
          if (dispatchedGeneration !== generation) {
            if (operationTools.has(name)) throw new ConnectionError();
            continue;
          }
          throw new ToolError(
            result.content
              ?.map((c) => ("text" in c ? c.text : ""))
              .join("\n") || "Operation failed",
          );
        }
        const value = result.structuredContent as
          | { data: Response }
          | undefined;
        if (!value || value.data.apiVersion !== 2)
          throw new Error(
            "Code Factory requires a coordinated API v2 plugin and daemon upgrade.",
          );
        if (
          dispatchedGeneration !== generation &&
          value.data.instanceId !== instanceId
        ) {
          // Late old-instance replies cannot move current ownership backward.
          // Repeat reads safely; reconcile commands by their original receipt.
          if (operationTools.has(name)) throw new ConnectionError();
          continue;
        }
        observeInstance(value.data.instanceId);
        return value.data;
      } catch (error) {
        if (!transientTransport(error)) throw error;
        if (!retry || attempt >= 2) throw new ConnectionError();
        await new Promise((resolve) =>
          setTimeout(resolve, attempt === 0 ? 250 : 750),
        );
      }
    }
  })();
  inFlight.set(key, work);
  try {
    return await work;
  } finally {
    if (inFlight.get(key) === work) inFlight.delete(key);
  }
}
async function bootstrap(): Promise<string> {
  const boot = await wire("open_factory", {}, true);
  return instanceId || boot.instanceId;
}
async function documentValue(
  value: unknown,
  options: CallOptions,
): Promise<unknown> {
  if (!value || typeof value !== "object" || !("document" in value))
    return value;
  const doc = (
    value as {
      document: {
        version: string;
        chunks: number;
        bytes: number;
        encoding: string;
      };
    }
  ).document;
  if (doc.encoding !== "base64-json" || doc.chunks < 1)
    throw new Error("Unsupported document descriptor");
  const parts: Uint8Array[] = [];
  let bytes = 0;
  for (let index = 0; index < doc.chunks; index++) {
    cancelled(options);
    let chunk: Response["response"];
    do {
      chunk = (
        await wire("document_chunk", { version: doc.version, index }, true)
      ).response;
      if (chunk.state === "busy") await pause(options);
    } while (chunk.state === "busy");
    if (
      chunk.version !== doc.version ||
      chunk.index !== index ||
      typeof chunk.chunk !== "string"
    )
      throw new Error("Document identity changed; refresh this view");
    const part = Uint8Array.from(atob(chunk.chunk), (c) => c.charCodeAt(0));
    bytes += part.length;
    parts.push(part);
  }
  if (bytes !== doc.bytes) throw new Error("Incomplete document");
  const complete = new Uint8Array(bytes);
  let offset = 0;
  for (const part of parts) {
    complete.set(part, offset);
    offset += part.length;
  }
  return JSON.parse(new TextDecoder().decode(complete));
}
export async function call<T>(
  name: string,
  args: Record<string, unknown> = {},
  options: CallOptions = {},
): Promise<T> {
  cancelled(options);
  const operation = operationTools.has(name);
  const requestId = operation ? crypto.randomUUID() : undefined;
  const submittedInstance = operation
    ? options.expectedInstanceId || instanceId || (await bootstrap())
    : undefined;
  let response: Response["response"];
  let responseInstanceId: string | undefined;
  try {
    const result = await wire(
      name,
      operation ? { ...args, requestId, instanceId: submittedInstance } : args,
      !operation,
    );
    responseInstanceId = result.instanceId;
    response = result.response;
    if (operation && response.state === "busy")
      throw new ToolError("Code Factory is busy; operation was not accepted.");
  } catch (error) {
    if (!operation || !transientTransport(error)) throw error;
    // A lost response is resolved by identity. Never repeat the submission.
    response = { state: "pending" };
    try {
      response = (
        await wire(
          "operation_status",
          { requestId, instanceId: submittedInstance },
          true,
        )
      ).response;
    } catch (error) {
      if (!isConnectionFailure(error)) throw error;
      options.onProgress?.("checking");
    }
  }
  for (;;) {
    cancelled(options);
    if (operation) {
      options.onProgress?.(response.state, {
        retryAt: response.retryAt,
        retryReason: response.retryReason,
      });
      if (response.state === "succeeded") {
        if (response.resultUnavailable)
          throw new Error(
            "Completed operation result is no longer available; inspect saved state.",
          );
        for (const key of inFlight.keys()) {
          const tool = JSON.parse(key)[0] as string;
          if (
            [
              "factory_state",
              "factory_item",
              "factory_events",
              "session_questions",
              "session_transcript",
            ].includes(tool)
          )
            inFlight.delete(key);
        }
        return (await documentValue(response.result ?? {}, options)) as T;
      }
      if (["failed", "uncertain", "lost"].includes(response.state)) {
        const ambiguous =
          response.state === "uncertain" || response.state === "lost";
        let detail: { error: string } | undefined;
        try {
          if (response.errorDocument)
            detail = (await documentValue(response.errorDocument, options)) as {
              error: string;
            };
        } catch (error) {
          if (!ambiguous) throw error;
          detail = {
            error: `${response.error || "Operation error"}; complete error detail is unavailable.`,
          };
        }
        const reason =
          detail?.error ||
          response.error ||
          (response.errorUnavailable
            ? "Complete error detail is no longer available."
            : undefined);
        const outcome = `Operation ${response.state}; inspect saved state before retrying.`;
        throw new ToolError(
          ambiguous || response.errorUnavailable
            ? `${outcome}${reason ? ` ${reason}` : ""}`
            : reason || outcome,
        );
      }
      await pause(options);
      try {
        const latest = (
          await wire(
            "operation_status",
            { requestId, instanceId: submittedInstance },
            true,
          )
        ).response;
        if (latest.state !== "busy") response = latest;
      } catch (error) {
        if (!isConnectionFailure(error)) throw error;
        // A temporary status-read outage does not replace the provider's retained
        // retry reason/deadline or release the owning action lock.
        if (response.state !== "waiting") options.onProgress?.("checking");
      }
    } else {
      if (responseInstanceId !== instanceId) {
        options.onPending?.();
        const latest = await wire(name, args, true);
        response = latest.response;
        responseInstanceId = latest.instanceId;
        continue;
      }
      if (response.state === "ready") {
        let value: unknown;
        try {
          value = await documentValue(response.value ?? response, options);
        } catch (error) {
          if (responseInstanceId === instanceId) throw error;
        }
        cancelled(options);
        if (responseInstanceId === instanceId) {
          options.onInstance?.(responseInstanceId!);
          return value as T;
        }
        options.onPending?.();
        const latest = await wire(name, args, true);
        response = latest.response;
        responseInstanceId = latest.instanceId;
        continue;
      }
      if (response.state === "failed")
        throw new ToolError(response.error || "Reading saved state failed");
      options.onPending?.();
      await pause(options);
      const latest = await wire(name, args, true);
      response = latest.response;
      responseInstanceId = latest.instanceId;
    }
  }
}

export async function openLink(url: string) {
  if (!/^https?:\/\//.test(url)) throw new Error("Unsupported URL");
  await connect();
  return app.openLink({ url });
}
