// Start owned fixtures before loading Playwright. Port zero is allocated by the
// real listener, never by a probe that closes before the server starts.
import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import { listenFixture } from "./fixture-listener.mjs";

const web = dirname(fileURLToPath(import.meta.url));
const root = resolve(web, "..");
const args = process.argv.slice(2);
let config;
for (let index = 0; index < args.length; index++) {
  const argument = args[index];
  if (argument === "-c" || argument === "--config") config = args[++index];
  else if (argument.startsWith("--config=")) config = argument.slice(9);
  else if (argument.startsWith("-c") && argument.length > 2) config = argument.slice(2);
}
const performance = config !== undefined && resolve(web, config) === join(web, "playwright.performance.config.ts");
const configured = process.env.CODE_FACTORY_TEST_BASE_PORT;
if (configured !== undefined && (!/^\d+$/.test(configured) || +configured < 1 || +configured > 65534))
  throw new Error("CODE_FACTORY_TEST_BASE_PORT must be a decimal integer from 1 to 65534");
const state = await mkdtemp(join(tmpdir(), "factory-fixtures-"));
const children = new Set();
let vite;
let interrupted;
function signal(signum) {
  interrupted = signum;
  for (const child of children) child.kill(signum);
}
const onInt = () => signal("SIGINT");
const onTerm = () => signal("SIGTERM");
process.on("SIGINT", onInt);
process.on("SIGTERM", onTerm);
function child(command, argv, options = {}) {
  if (interrupted) throw new Error("Fixture invocation interrupted");
  const handle = spawn(command, argv, { cwd: root, stdio: ["ignore", "inherit", "inherit"], ...options });
  children.add(handle);
  handle.done = new Promise((accept, reject) => {
    handle.once("error", reject);
    handle.once("exit", (code, sig) => {
      children.delete(handle);
      accept(code ?? (sig === "SIGINT" ? 130 : 143));
    });
  });
  return handle;
}
async function ready(handle, file) {
  const deadline = Date.now() + 120000;
  while (Date.now() < deadline) {
    if (interrupted || handle.exitCode !== null || handle.signalCode !== null)
      throw new Error("Owned fixture exited before startup completed");
    try { return JSON.parse(await readFile(file, "utf8")).origin; }
    catch (error) { if (error.code !== "ENOENT") throw error; }
    await new Promise((accept) => setTimeout(accept, 20));
  }
  throw new Error("Owned fixture startup timed out");
}
let exitCode = 1;
try {
  const binary = join(state, "testhost");
  const build = child("go", ["build", "-o", binary, "./internal/testhost"], { stdio: ["ignore", 2, 2] });
  if (await build.done) throw new Error("Fixture build failed");
  const env = { ...process.env, CODE_FACTORY_REAL_CODEX: "0" };
  if (!performance) {
    vite = await createServer({ root: web, logLevel: "silent", server: {
      host: "127.0.0.1", port: configured === undefined ? 0 : +configured, strictPort: true,
    } });
    env.CODE_FACTORY_TEST_UI_ORIGIN = await listenFixture(vite, configured === undefined ? 0 : +configured);
  }
  const readyFile = join(state, "ready.json");
  const daemon = child(binary, [], { env: { ...env,
    CODE_FACTORY_PERFORMANCE: performance ? "1" : "0",
    CODE_FACTORY_TEST_DAEMON_PORT: configured === undefined ? "0" : String(+configured + 1),
    CODE_FACTORY_TEST_READY_FILE: readyFile,
  }, stdio: ["ignore", 2, 2] });
  env.CODE_FACTORY_TEST_DAEMON_ORIGIN = await ready(daemon, readyFile);
  console.error(`Owned fixtures: ui=${env.CODE_FACTORY_TEST_UI_ORIGIN ?? "unused"} daemon=${env.CODE_FACTORY_TEST_DAEMON_ORIGIN}`);
  env.CODE_FACTORY_TEST_FIXTURES_OWNED = "1";
  const test = child(process.execPath, [join(web, "node_modules/@playwright/test/cli.js"), "test", ...args],
                     { cwd: web, env });
  exitCode = await Promise.race([
    test.done,
    daemon.done.then(() => { throw new Error("Owned daemon exited during browser verification"); }),
  ]);
} catch (error) {
  console.error(error.message);
} finally {
  // Only handles spawned by this invocation are targeted. A timeout bounds a
  // fixture that cannot shut down cooperatively; no port/PID scan is involved.
  const owned = [...children];
  for (const handle of owned) handle.kill("SIGTERM");
  const killer = setTimeout(() => { for (const handle of owned) if (children.has(handle)) handle.kill("SIGKILL"); }, 5000);
  await Promise.allSettled(owned.map((handle) => handle.done));
  clearTimeout(killer);
  await vite?.close();
  await rm(state, { recursive: true, force: true });
  process.off("SIGINT", onInt);
  process.off("SIGTERM", onTerm);
}
process.exitCode = interrupted === "SIGINT" ? 130 : interrupted ? 143 : exitCode;
