import { App } from "@modelcontextprotocol/ext-apps";
const app = new App({ name: "Code Factory", version: "0.1.0" }, {});
let connected: Promise<void> | undefined;
export function connect() {
  if (!connected) {
    app.onhostcontextchanged = (ctx) => {
      if (ctx.theme) document.documentElement.dataset.theme = ctx.theme;
    };
    connected = app.connect().then(() => {
      const ctx = app.getHostContext();
      if (ctx?.theme) document.documentElement.dataset.theme = ctx.theme;
    });
  }
  return connected;
}
export async function call<T>(
  name: string,
  args: Record<string, unknown> = {},
): Promise<T> {
  await connect();
  const result = await app.callServerTool({ name, arguments: args });
  if (result.isError)
    throw new Error(
      result.content?.map((c) => ("text" in c ? c.text : "")).join("\n") ||
        "Operation failed",
    );
  const value = result.structuredContent as { data: T } | undefined;
  if (!value)
    throw new Error("The host returned no data. Reconnect Code Factory.");
  return value.data;
}

export async function openLink(url: string) {
  if (!/^https?:\/\//.test(url)) throw new Error("Unsupported URL");
  await connect();
  return app.openLink({ url });
}
