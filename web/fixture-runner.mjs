// Start owned fixtures before loading Playwright. Port zero is allocated by the
// real listener, never by a probe that closes before the server starts.
import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import playwright from "playwright/lib/program";

const web = dirname(fileURLToPath(import.meta.url));
const root = resolve(web, "..");
const args = process.argv.slice(2);
// Reuse the installed CLI's option model without invoking its test action.
const testCommand = playwright.program.commands.find((command) => command.name() === "test");
testCommand.parseOptions(args);
const config = testCommand.opts().config;
const performance = config !== undefined && resolve(web, config) === join(web, "playwright.performance.config.ts");
const configured = process.env.CODE_FACTORY_TEST_BASE_PORT;
if (configured !== undefined && (!/^\d+$/.test(configured) || +configured < 1 || +configured > 65534))
  throw new Error("CODE_FACTORY_TEST_BASE_PORT must be a decimal integer from 1 to 65534");
const state = await mkdtemp(join(tmpdir(), "factory-fixtures-"));
const children = new Set();
let interrupted;
let killer;
function stopChildren(signum = "SIGTERM") {
  for (const handle of children) handle.kill(signum);
  // Cancellation must bound a pending startup wait too. Keep the first deadline
  // through repeated signals and finally; only retained owned handles are used.
  killer ??= setTimeout(() => {
    for (const handle of children) handle.kill("SIGKILL");
  }, 5000);
}
function signal(signum) {
  interrupted ??= signum;
  stopChildren(signum);
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
  handle.done.catch(() => {});
  return handle;
}
const startupTimeout = performance ? 120000 : 60000;
function startupError(fixture, timeout) {
  return new Error(`Owned ${fixture} fixture startup timed out after ${timeout}ms`);
}
async function beforeStartupDeadline(promise, deadline, fixture, timeout) {
  let timer;
  try {
    return await Promise.race([promise, new Promise((_, reject) => {
      timer = setTimeout(() => reject(startupError(fixture, timeout)), Math.max(0, deadline - Date.now()));
    })]);
  } finally { clearTimeout(timer); }
}
async function ready(handle, file, deadline, fixture, timeout) {
  let exited = false;
  handle.done.then(() => { exited = true; }, () => { exited = true; });
  while (Date.now() < deadline) {
    if (interrupted || exited || handle.exitCode !== null || handle.signalCode !== null)
      throw new Error("Owned fixture exited before startup completed");
    try {
      const value = JSON.parse(await readFile(file, "utf8"));
      if (value.error !== undefined) throw new Error(`Owned ${fixture} fixture startup failed: ${value.error}`);
      if (typeof value.origin !== "string") throw new Error("Owned fixture published no origin");
      return value.origin;
    }
    catch (error) { if (error.code !== "ENOENT") throw error; }
    await new Promise((accept) => setTimeout(accept, 20));
  }
  throw startupError(fixture, timeout);
}
let exitCode = 1;
try {
  const env = { ...process.env, CODE_FACTORY_REAL_CODEX: "0" };
  // Go compilation and readiness share the former Playwright webServer budget.
  const deadline = Date.now() + startupTimeout;
  const binary = join(state, "testhost");
  const build = child("go", ["build", "-o", binary, "./internal/testhost"], { stdio: ["ignore", 2, 2] });
  if (await beforeStartupDeadline(build.done, deadline, "Go", startupTimeout)) throw new Error("Fixture build failed");
  const readyFile = join(state, "ready.json");
  const daemon = child(binary, [], { env: { ...env,
    CODE_FACTORY_PERFORMANCE: performance ? "1" : "0",
    CODE_FACTORY_TEST_DAEMON_PORT: configured === undefined ? "0" : String(+configured + 1),
    CODE_FACTORY_TEST_READY_FILE: readyFile,
  }, stdio: ["ignore", 2, 2] });
  env.CODE_FACTORY_TEST_DAEMON_ORIGIN = await beforeStartupDeadline(
    ready(daemon, readyFile, deadline, "Go", startupTimeout), deadline, "Go", startupTimeout);
  let ui;
  if (!performance) {
    const uiFile = join(state, "ui-ready.json");
    const uiDeadline = Date.now() + 60000;
    ui = child(process.execPath, [join(web, "fixture-listener.mjs"), uiFile, configured ?? "0"],
      { env, stdio: ["ignore", 2, 2] });
    env.CODE_FACTORY_TEST_UI_ORIGIN = await beforeStartupDeadline(
      ready(ui, uiFile, uiDeadline, "UI", 60000), uiDeadline, "UI", 60000);
  }
  console.error(`Owned fixtures: ui=${env.CODE_FACTORY_TEST_UI_ORIGIN ?? "unused"} daemon=${env.CODE_FACTORY_TEST_DAEMON_ORIGIN}`);
  env.CODE_FACTORY_TEST_FIXTURES_OWNED = "1";
  if ([daemon, ui].some((handle) => handle && (handle.exitCode !== null || handle.signalCode !== null)))
    throw new Error("Owned fixture exited before browser verification");
  const test = child(process.execPath, [join(web, "node_modules/@playwright/test/cli.js"), "test", ...args],
                     { cwd: web, env });
  exitCode = await Promise.race([
    test.done,
    daemon.done.then(() => { throw new Error("Owned daemon exited during browser verification"); }),
    ...(ui ? [ui.done.then(() => { throw new Error("Owned UI exited during browser verification"); })] : []),
  ]);
} catch (error) {
  console.error(error.message);
} finally {
  // Only handles spawned by this invocation are targeted. A timeout bounds a
  // fixture that cannot shut down cooperatively; no port/PID scan is involved.
  const owned = [...children];
  stopChildren();
  await Promise.allSettled(owned.map((handle) => handle.done));
  clearTimeout(killer);
  await rm(state, { recursive: true, force: true });
  process.off("SIGINT", onInt);
  process.off("SIGTERM", onTerm);
}
process.exitCode = interrupted === "SIGINT" ? 130 : interrupted ? 143 : exitCode;
