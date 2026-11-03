import { once } from "node:events";
import { createServer as createHTTPServer } from "node:http";
import { createServer } from "vite";
import { realpathSync } from "node:fs";
import { writeFile, rename } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

export async function createFixtureServer(options) {
  const httpServer = createHTTPServer();
  // Middleware mode leaves signals and process exit to the fixture owner.
  // HTTP and HMR share this listener, including its kernel-assigned port.
  const vite = await createServer({ ...options, server: { ...options.server,
    middlewareMode: true, hmr: { server: httpServer },
  } });
  httpServer.on("request", vite.middlewares);
  let closing;
  return { httpServer, close() {
    return closing ??= (async () => {
      try { await vite.close(); }
      finally {
        await new Promise((accept, reject) => {
          httpServer.close((error) => error && error.code !== "ERR_SERVER_NOT_RUNNING" ? reject(error) : accept());
          httpServer.closeAllConnections();
        });
      }
    })();
  } };
}

export async function listenFixture(server, port = 0) {
  // Vite's listen() substitutes its default for port zero. Bind the owned HTTP
  // server directly; the actual listener owns the kernel allocation throughout.
  const listening = once(server.httpServer, "listening");
  server.httpServer.listen({ host: "127.0.0.1", port });
  await listening;
  return `http://127.0.0.1:${server.httpServer.address().port}`;
}

async function main([readyFile, configuredPort, ...extra]) {
  if (!readyFile || extra.length || !/^\d+$/.test(configuredPort ?? "") || +configuredPort > 65535)
    throw new Error("Owned UI fixture requires a readiness path and decimal port from 0 to 65535");
  const creation = createFixtureServer({ root: dirname(fileURLToPath(import.meta.url)),
    cacheDir: join(dirname(readyFile), "ui-cache"), logLevel: "silent",
    server: { host: "127.0.0.1", port: +configuredPort, strictPort: true } });
  let interrupted, closing;
  const close = () => closing ??= creation.then((server) => server.close());
  function signal(signum) {
    interrupted ??= signum;
    process.exitCode = interrupted === "SIGINT" ? 130 : 143;
    close().catch((error) => console.error(error.message));
  }
  process.on("SIGINT", () => signal("SIGINT"));
  process.on("SIGTERM", () => signal("SIGTERM"));
  async function publish(value) {
    await writeFile(readyFile + ".tmp", JSON.stringify(value), { mode: 0o600 });
    await rename(readyFile + ".tmp", readyFile);
  }
  try {
    const server = await creation;
    if (interrupted) return await close();
    const origin = await listenFixture(server, +configuredPort);
    if (interrupted) return await close();
    await publish({ origin });
  } catch (error) {
    console.error(error.message);
    process.exitCode ||= 1;
    await publish({ error: error.message }).catch((failure) => console.error(failure.message));
    await close().catch((failure) => console.error(failure.message));
  }
}

function isMain() {
  const entry = process.argv[1];
  if (typeof entry !== "string" || entry.startsWith("-")) return false;
  try { return realpathSync(entry) === realpathSync(fileURLToPath(import.meta.url)); }
  catch { return false; }
}
if (isMain()) await main(process.argv.slice(2)).catch((error) => {
  console.error(error.message);
  process.exitCode = 1;
});
