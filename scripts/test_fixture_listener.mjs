import assert from "node:assert/strict";
import { test } from "node:test";
import { mkdtemp, readFile, readdir, rm, writeFile, symlink } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import { createServer } from "node:http";
import { createFixtureServer, listenFixture } from "../web/fixture-listener.mjs";

async function bounded(promise, operation, milliseconds = 5000) {
  let timer;
  try {
    return await Promise.race([promise, new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(operation + " exceeded its local deadline")), milliseconds);
    })]);
  } finally { clearTimeout(timer); }
}

async function request(url, milliseconds = 5000) {
  const response = await fetch(url, { signal: AbortSignal.timeout(milliseconds) });
  await response.arrayBuffer();
  return response;
}

async function ownedCommand(t, argv, options = {}) {
  const { observeExit = false, ...spawnOptions } = options;
  const supervisor = fileURLToPath(new URL("./fixture_test_process.py", import.meta.url));
  const child = spawn("python3", [supervisor, ...(observeExit ? ["--observe-exit"] : []), ...argv], {
    ...spawnOptions, stdio: ["pipe", "pipe", "pipe", "pipe"],
  });
  let output = "", errors = "", readiness = "";
  child.stdout.on("data", (chunk) => { output += chunk; });
  child.stderr.on("data", (chunk) => { errors += chunk; });
  const exited = new Promise((accept, reject) => {
    child.once("error", reject);
    child.once("exit", (code, signal) => accept({ code, signal }));
  });
  const closed = new Promise((accept) => child.once("close", accept));
  let acceptReady, rejectReady, acceptLeader, rejectLeader;
  let readySeen = false, leaderSeen = false;
  const ready = new Promise((accept, reject) => { acceptReady = accept; rejectReady = reject; });
  const leaderExited = new Promise((accept, reject) => { acceptLeader = accept; rejectLeader = reject; });
  leaderExited.catch(() => {});
  child.once("error", (error) => { rejectReady(error); rejectLeader(error); });
  child.stdio[3].on("data", (chunk) => {
    readiness += chunk;
    while (readiness.includes("\n")) {
      const end = readiness.indexOf("\n");
      const line = readiness.slice(0, end);
      readiness = readiness.slice(end + 1);
      try {
        const event = JSON.parse(line);
        if (event.pid !== undefined) { readySeen = true; acceptReady(event); }
        else if (event.exited !== undefined) { leaderSeen = true; acceptLeader(event.exited); }
        else throw new Error("Unknown fixture supervisor event");
      } catch (error) { rejectReady(error); rejectLeader(error); }
    }
  });
  child.stdio[3].once("end", () => {
    const error = new Error("Fixture supervisor exited before observation: " + errors);
    if (!readySeen) rejectReady(error);
    if (!leaderSeen) rejectLeader(error);
  });
  child.stdin.on("error", () => {});
  exited.catch(() => {});
  t.after(async () => {
    child.stdin.end();
    await bounded(closed, "fixture command cleanup", 15000);
  });
  const { pid } = await bounded(ready, "fixture supervisor startup");
  const completion = bounded(Promise.all([exited, closed]).then(([result]) => result), "fixture command", 60000);
  completion.catch(() => {});
  return { pid, signal: () => child.stdin.write("SIGTERM\n"), exited: completion,
    leaderExited, releaseCleanup: () => child.stdin.end("CLEANUP\n"),
    abortObservation: () => child.stdin.end(),
    interruptSupervisor: () => child.kill("SIGTERM"),
    writeControl: (text) => child.stdin.write(text),
    get output() { return output; }, get errors() { return errors; } };
}

async function fixture(t, create = createFixtureServer) {
  const cacheDir = await mkdtemp(join(tmpdir(), "factory-vite-test-"));
  const creation = create({ configFile: false, cacheDir, logLevel: "silent", server: { host: "127.0.0.1", port: 0 } });
  let cleanup;
  function close() {
    return cleanup ??= (async () => {
      let server;
      try {
        server = await creation;
        await server.close();
      } finally {
        server?.httpServer.closeAllConnections();
        await rm(cacheDir, { recursive: true, force: true });
      }
    })();
  }
  t.after(() => bounded(close(), "Vite fixture cleanup"));
  try { return await bounded(creation, "Vite fixture creation"); }
  catch (error) {
    // Keep eventual ownership even if both local deadlines expire first.
    close().catch((cleanupError) => {
      if (cleanupError !== error) console.error("Late Vite fixture cleanup failed:", cleanupError);
    });
    throw error;
  }
}

test("Vite fixture cleanup preserves creation and close failures", async (t) => {
  for (const creationFailure of [true, false]) await t.test(creationFailure ? "creation" : "close", async () => {
    const hooks = [];
    const error = new Error("controlled fixture failure");
    let cache, closes = 0, connections = 0;
    const result = fixture({ after: (hook) => hooks.push(hook) }, async (options) => {
      cache = options.cacheDir;
      if (creationFailure) throw error;
      return { close: async () => { closes++; throw error; },
        httpServer: { closeAllConnections() { connections++; } } };
    });
    if (creationFailure) await assert.rejects(result, (value) => value === error);
    else await result;
    assert.equal(hooks.length, 1);
    for (let attempt = 0; attempt < 2; attempt++) await assert.rejects(hooks[0](), (value) => value === error);
    await assert.rejects(readFile(cache), { code: "ENOENT" });
    assert.equal(closes, creationFailure ? 0 : 1);
    assert.equal(connections, creationFailure ? 0 : 1);
  });
});

test("late Vite creation retains cleanup after its local deadline", async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "factory-vite-late-test-"));
  t.after(() => bounded(rm(directory, { recursive: true, force: true }), "late probe removal"));
  const preload = join(directory, "preload.mjs");
  await writeFile(preload, `import fs from "node:fs";
import { syncBuiltinESMExports } from "node:module";
const timeout = globalThis.setTimeout;
const read = fs.promises.readFile;
const watch = fs.watch;
const watchers = new Set();
let cache;
globalThis.setTimeout = (callback, milliseconds, ...args) =>
  timeout(callback, milliseconds === 5000 ? 50 : milliseconds, ...args);
fs.promises.readFile = async (file, ...args) => {
  if (String(file).includes("factory-vite-test-") && String(file).endsWith("/deps/_metadata.json")) {
    cache = String(file).slice(0, -"/deps/_metadata.json".length);
    console.log("VITE_CACHE_WAIT");
    await new Promise((accept) => timeout(accept, 250));
  }
  return read(file, ...args);
};
fs.watch = (...args) => {
  const watcher = watch(...args);
  watchers.add(watcher);
  const close = watcher.close.bind(watcher);
  watcher.close = () => { watchers.delete(watcher); return close(); };
  return watcher;
};
syncBuiltinESMExports();
process.on("beforeExit", () => {
  if (cache) console.log("VITE_LATE_RESULT=" + JSON.stringify({ watchers: watchers.size, cache: fs.existsSync(cache) }));
});
`);
  const script = fileURLToPath(import.meta.url);
  const child = await ownedCommand(t, [process.execPath, "--import", preload, "--test",
    "--test-name-pattern=^concurrent Vite fixtures retain distinct kernel allocations$", script], {
    observeExit: true, env: { ...process.env, NODE_TEST_CONTEXT: undefined },
  });
  t.after(() => child.abortObservation());
  assert.deepEqual(await bounded(child.leaderExited, "late Vite cleanup before supervisor fallback", 3000),
    { code: 1, signal: null }, child.errors);
  assert.match(child.output, /VITE_CACHE_WAIT/);
  assert.match(child.output, /Vite fixture creation exceeded its local deadline/);
  const result = JSON.parse(child.output.match(/VITE_LATE_RESULT=(\{[^\n]+\})/)[1]);
  assert.deepEqual(result, { watchers: 0, cache: false });
  child.releaseCleanup();
  assert.deepEqual(await child.exited, { code: 1, signal: null }, child.errors);
});

test("runner bounds Go startup by its maintained mode", async (t) => {
  for (const performance of [false, true]) await t.test(performance ? "performance" : "correctness", async (t) => {
    const directory = await mkdtemp(join(tmpdir(), "factory-build-timeout-test-"));
    const ready = join(directory, "build-ready");
    const timer = join(directory, "timer");
    const cleanupTimer = join(directory, "cleanup-timer");
    const preload = join(directory, "preload.mjs");
    t.after(() => bounded(rm(directory, { recursive: true, force: true }), "startup timeout probe removal"));
    await writeFile(join(directory, "go"), `#!${process.execPath}
const { writeFileSync } = require("node:fs");
process.on("SIGTERM", () => {});
writeFileSync(${JSON.stringify(ready)}, String(process.pid));
setInterval(() => {}, 1000);
`, { mode: 0o755 });
    await writeFile(preload, `import { writeFileSync } from "node:fs";
const timeout = globalThis.setTimeout;
const now = Date.now();
Date.now = () => now;
globalThis.setTimeout = (callback, milliseconds, ...args) => {
  // Exercise escalation without waiting through the production grace period.
  // The unresponsive-build cancellation test below retains the real timer.
  if (milliseconds === 5000) {
    writeFileSync(${JSON.stringify(cleanupTimer)}, String(milliseconds));
    return timeout(callback, 100, ...args);
  }
  if (milliseconds > 10000) {
    writeFileSync(${JSON.stringify(timer)}, String(milliseconds));
    return timeout(callback, 250, ...args);
  }
  return timeout(callback, milliseconds, ...args);
};
`);
    const web = fileURLToPath(new URL("../web/", import.meta.url));
    const child = await ownedCommand(t, [process.execPath, "--import", preload, join(web, "fixture-runner.mjs"),
      ...(performance ? ["-c", "playwright.performance.config.ts"] : [])], {
      observeExit: true, cwd: web,
      env: { ...process.env, PATH: directory + ":" + process.env.PATH, TMPDIR: directory,
        CODE_FACTORY_REAL_CODEX: "0", CODE_FACTORY_TEST_BASE_PORT: undefined },
    });
    t.after(() => child.abortObservation());
    await until(async () => (await readdir(directory)).includes("build-ready"), "Go build never started: " + child.errors);
    const pid = Number(await readFile(ready, "utf8"));
    assert.deepEqual(await bounded(child.leaderExited, "Go startup timeout before supervisor fallback", 7500),
      { code: 1, signal: null }, child.errors);
    const expected = performance ? 120000 : 60000;
    assert.equal(Number(await readFile(timer, "utf8")), expected);
    assert.equal(Number(await readFile(cleanupTimer, "utf8")), 5000);
    assert.match(child.errors, new RegExp("Owned Go fixture startup timed out after " + expected + "ms"));
    await assert.rejects(readFile(`/proc/${pid}/stat`), { code: "ENOENT" });
    assert.equal((await readdir(directory)).filter((name) => name.startsWith("factory-fixtures-")).length, 0);
    child.releaseCleanup();
    assert.deepEqual(await child.exited, { code: 1, signal: null }, child.errors);
  });
});

test("runner bounds owned UI startup before supervisor fallback", async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "factory-ui-timeout-test-"));
  const marker = join(directory, "cache-wait");
  const retained = join(directory, "children");
  const timer = join(directory, "timer");
  const cleanupTimer = join(directory, "cleanup-timer");
  const preload = join(directory, "preload.mjs");
  t.after(() => bounded(rm(directory, { recursive: true, force: true }), "UI timeout probe removal"));
  await writeFile(preload, `import fs from "node:fs";
import cp from "node:child_process";
import { syncBuiltinESMExports } from "node:module";
const timeout = globalThis.setTimeout;
const read = fs.promises.readFile;
const spawn = cp.spawn;
const now = Date.now();
Date.now = () => now;
let uiStarted = false;
globalThis.setTimeout = (callback, milliseconds, ...args) => {
  if (milliseconds === 5000) {
    fs.writeFileSync(${JSON.stringify(cleanupTimer)}, String(milliseconds));
    return timeout(callback, 100, ...args);
  }
  if (uiStarted && milliseconds > 10000) {
    fs.writeFileSync(${JSON.stringify(timer)}, String(milliseconds));
    return timeout(callback, 1000, ...args);
  }
  return timeout(callback, milliseconds, ...args);
};
fs.promises.readFile = (file, ...args) => {
  if (String(file).endsWith("/deps/_metadata.json")) {
    uiStarted = true;
    fs.writeFileSync(${JSON.stringify(marker)}, String(process.pid));
    return new Promise(() => {});
  }
  return read(file, ...args);
};
cp.spawn = (command, args, options) => {
  const ui = args[0]?.endsWith("/fixture-listener.mjs");
  if (ui) uiStarted = true;
  const child = spawn(command, ui ? ["--import", ${JSON.stringify(preload)}, ...args] : args, options);
  if (ui || command.endsWith("/testhost")) fs.appendFileSync(${JSON.stringify(retained)}, String(child.pid) + "\\n");
  return child;
};
syncBuiltinESMExports();
`);
  const web = fileURLToPath(new URL("../web/", import.meta.url));
  const child = await ownedCommand(t, [process.execPath, "--import", preload, join(web, "fixture-runner.mjs"), "--list"], {
    observeExit: true, cwd: web, env: { ...process.env, TMPDIR: directory,
      CODE_FACTORY_REAL_CODEX: "0", CODE_FACTORY_TEST_BASE_PORT: undefined },
  });
  t.after(() => child.abortObservation());
  await until(async () => (await readdir(directory)).includes("cache-wait"), "maintained UI cache wait was not reached: " + child.errors);
  assert.deepEqual(await bounded(child.leaderExited, "UI startup timeout before supervisor fallback", 7500),
    { code: 1, signal: null }, child.errors);
  assert.equal(Number(await readFile(timer, "utf8")), 60000);
  assert.equal(Number(await readFile(cleanupTimer, "utf8")), 5000);
  assert.match(child.errors, /Owned UI fixture startup timed out after 60000ms/);
  const pids = (await readFile(retained, "utf8")).trim().split(/\s+/).map(Number);
  assert.equal(pids.length, 2, "expected retained daemon and UI children before browser startup");
  assert.ok(pids.includes(Number(await readFile(marker, "utf8"))), "Vite initialization did not run in retained UI child");
  for (const pid of pids) await assert.rejects(readFile(`/proc/${pid}/stat`), { code: "ENOENT" });
  assert.equal((await readdir(directory)).filter((name) => name.startsWith("factory-fixtures-")).length, 0);
  child.releaseCleanup();
  assert.deepEqual(await child.exited, { code: 1, signal: null }, child.errors);
});

test("owned UI module imports and CLI aliases preserve lifecycle identity", async (t) => {
  const module = fileURLToPath(new URL("../web/fixture-listener.mjs", import.meta.url));
  for (const suffix of [[], ["--", "--help"]]) {
    const child = await ownedCommand(t, [process.execPath, "--import", module, "-e", 'console.log("IMPORTED")', ...suffix]);
    assert.deepEqual(await child.exited, { code: 0, signal: null }, child.errors);
    assert.match(child.output, /IMPORTED/);
  }
  const directory = await mkdtemp(join(tmpdir(), "factory-ui-alias-test-"));
  const alias = join(directory, "alias.mjs"), readyFile = join(directory, "ready.json");
  t.after(() => bounded(rm(directory, { recursive: true, force: true }), "UI alias removal"));
  await symlink(module, alias);
  const child = await ownedCommand(t, [process.execPath, alias, readyFile, "0"], { observeExit: true });
  t.after(() => child.abortObservation());
  await until(async () => (await readdir(directory)).includes("ready.json"), "UI alias never became ready: " + child.errors);
  const { origin } = JSON.parse(await readFile(readyFile, "utf8"));
  assert.match(origin, /^http:\/\/127\.0\.0\.1:[1-9][0-9]*$/);
  assert.equal((await request(origin + "/@vite/client")).status, 200);
  child.signal();
  assert.deepEqual(await bounded(child.leaderExited, "UI alias close", 3000), { code: 143, signal: null }, child.errors);
  child.releaseCleanup();
  assert.deepEqual(await child.exited, { code: 143, signal: null }, child.errors);
});

test("fixture requests abort stalled response headers and bodies", async (t) => {
  for (const body of [false, true]) {
    await t.test(body ? "body" : "headers", async (t) => {
      let seen;
      const accepted = new Promise((accept) => { seen = accept; });
      const server = createServer((_, response) => {
        if (body) { response.writeHead(200); response.flushHeaders(); }
        seen();
      });
      t.after(async () => {
        server.closeAllConnections();
        await bounded(new Promise((accept, reject) => server.close((error) => error ? reject(error) : accept())), "stalled HTTP close");
      });
      await bounded(new Promise((accept, reject) => {
        server.once("error", reject);
        server.listen(0, "127.0.0.1", accept);
      }), "stalled HTTP listen");
      const failed = assert.rejects(request(`http://127.0.0.1:${server.address().port}`, 100),
        (error) => error.name === "TimeoutError" || error.name === "AbortError");
      failed.catch(() => {});
      await bounded(accepted, "stalled HTTP request acceptance");
      await bounded(failed, "stalled HTTP rejection", 2000);
    });
  }
});

test("fixture supervisor cleans descendants after normal and abrupt leader exit", async (t) => {
  const unrelated = await ownedCommand(t, [process.execPath, "-e", "setInterval(() => {}, 1000)"]);
  for (const abrupt of [false, true]) {
    await t.test(abrupt ? "SIGKILL" : "normal exit", async (t) => {
      const directory = await mkdtemp(join(tmpdir(), "factory-exited-leader-test-"));
      const marker = join(directory, "ready");
      const worker = `process.on("SIGTERM", () => {});
require("node:fs").writeFileSync(${JSON.stringify(marker)}, "ready"); setInterval(() => {}, 1000);`;
      const script = `const { spawn } = require("node:child_process");
const { existsSync } = require("node:fs");
const worker = spawn(process.execPath, ["-e", ${JSON.stringify(worker)}], { stdio: ["ignore", 1, 2] });
console.log(worker.pid);
setInterval(() => { if (existsSync(${JSON.stringify(marker)})) ${abrupt ? 'process.kill(process.pid, "SIGKILL")' : 'process.exit(0)'}; }, 10);`;
      const command = ownedCommand(t, [process.execPath, "-e", script], { observeExit: true });
      t.after(() => bounded(rm(directory, { recursive: true, force: true }), "leader probe removal"));
      const child = await command;
      assert.deepEqual(await bounded(child.leaderExited, "leader exit observation"),
        abrupt ? { code: null, signal: "SIGKILL" } : { code: 0, signal: null }, child.errors);
      const workerPid = Number(child.output.trim());
      assert.ok(workerPid > 0, child.output);
      assert.ok((await readFile(`/proc/${workerPid}/stat`, "utf8")).length > 0,
        "supervisor cleaned descendants before observation was released");
      child.releaseCleanup();
      assert.deepEqual(await child.exited, { code: abrupt ? 137 : 0, signal: null }, child.errors);
      await assert.rejects(readFile(`/proc/${workerPid}/stat`), { code: "ENOENT" });
      assert.ok((await readFile(`/proc/${unrelated.pid}/stat`, "utf8")).length > 0,
        "cleanup affected the unrelated command");
    });
  }
  for (const ending of ["deadline", "EOF", "SIGTERM", "partial control"]) await t.test("observation " + ending, async (t) => {
    const script = `const { spawn } = require("node:child_process");
const worker = spawn(process.execPath, ["-e", 'process.on("SIGTERM", () => {}); setInterval(() => {}, 1000)'], { stdio: ["ignore", 1, 2] });
console.log(worker.pid); process.exit(0);`;
    const child = await ownedCommand(t, [process.execPath, "-e", script], { observeExit: true });
    assert.deepEqual(await bounded(child.leaderExited, "leader exit observation"), { code: 0, signal: null });
    await until(() => /^\d+\s*$/.test(child.output), "missing descendant PID", 1000);
    const workerPid = Number(child.output.trim());
    assert.ok((await readFile(`/proc/${workerPid}/stat`, "utf8")).length > 0);
    if (ending === "EOF") child.abortObservation();
    if (ending === "SIGTERM") child.interruptSupervisor();
    if (ending === "partial control") child.writeControl("CLEAN");
    assert.deepEqual(await bounded(child.exited, "unreleased observation cleanup", 7500),
      { code: 1, signal: null }, child.errors);
    assert.match(child.errors, ending === "EOF" ? /Fixture cleanup observation ended without acknowledgment/
      : ending === "SIGTERM" ? /Fixture cleanup observation interrupted before acknowledgment/
      : /Fixture cleanup observation exceeded 5 seconds/);
    await assert.rejects(readFile(`/proc/${workerPid}/stat`), { code: "ENOENT" });
    assert.ok((await readFile(`/proc/${unrelated.pid}/stat`, "utf8")).length > 0);
  });
});

test("concurrent Vite fixtures retain distinct kernel allocations", async (t) => {
  const a = await fixture(t), b = await fixture(t);
  const [first, second] = await bounded(Promise.all([listenFixture(a), listenFixture(b)]), "concurrent Vite listen");
  assert.notEqual(first, second);
  for (const origin of [first, second]) {
    assert.match(origin, /^http:\/\/127\.0\.0\.1:[1-9][0-9]*$/);
    assert.equal((await request(origin + "/@vite/client")).status, 200);
  }
});

test("owned Vite fixtures preserve caller signal listeners", async (t) => {
  const caller = () => {};
  process.on("SIGTERM", caller);
  t.after(() => process.off("SIGTERM", caller));
  const before = process.rawListeners("SIGTERM");
  const server = await fixture(t);
  await bounded(listenFixture(server), "Vite listen");
  assert.deepEqual(process.rawListeners("SIGTERM"), before);
  await bounded(server.close(), "Vite fixture close");
  assert.deepEqual(process.rawListeners("SIGTERM"), before);
});

async function until(predicate, message, milliseconds = 15000) {
  const deadline = Date.now() + milliseconds;
  while (Date.now() < deadline) {
    if (await predicate()) return;
    await new Promise((accept) => setTimeout(accept, 20));
  }
  assert.fail(message);
}

async function browserRunner(t) {
  const directory = await mkdtemp(join(tmpdir(), "factory-runner-signal-test-"));
  const childReady = join(directory, "child-ready");
  const retainedChildren = join(directory, "retained-children");
  const stub = join(directory, "playwright.mjs");
  const preload = join(directory, "preload.mjs");
  // Keep a child alive through TERM so the runner must await its bounded KILL cleanup.
  await writeFile(stub, `import { writeFileSync } from "node:fs";
process.on("SIGTERM", () => {});
writeFileSync(process.env.PROBE_CHILD_READY, String(process.pid));
setInterval(() => {}, 1000);
`);
  await writeFile(preload, `import cp from "node:child_process";
import { syncBuiltinESMExports } from "node:module";
import { appendFileSync } from "node:fs";
const spawn = cp.spawn;
cp.spawn = (command, args, options) => {
  const browser = args[0]?.endsWith("/@playwright/test/cli.js");
  const child = spawn(command, browser ? [${JSON.stringify(stub)}] : args, options);
  if (browser || args[0]?.endsWith("/fixture-listener.mjs") || command.endsWith("/testhost"))
    appendFileSync(process.env.PROBE_RETAINED_CHILDREN, JSON.stringify({ pid: child.pid, ui: args[0]?.endsWith("/fixture-listener.mjs") }) + "\\n");
  return child;
};
syncBuiltinESMExports();
`);
  const web = fileURLToPath(new URL("../web/", import.meta.url));
  const child = await ownedCommand(t, [process.execPath, "--import", preload, join(web, "fixture-runner.mjs")], {
    observeExit: true,
    cwd: web,
    env: { ...process.env, TMPDIR: directory, PROBE_CHILD_READY: childReady,
      PROBE_RETAINED_CHILDREN: retainedChildren,
      CODE_FACTORY_REAL_CODEX: "0", CODE_FACTORY_TEST_BASE_PORT: undefined },
  });
  t.after(async () => {
    child.abortObservation();
    try { await child.exited; }
    finally { await bounded(rm(directory, { recursive: true, force: true }), "signal probe removal"); }
  });
  await until(async () => (await readdir(directory)).includes("child-ready"), "Playwright child never started: " + child.errors);
  const pids = (await readFile(`/proc/${child.pid}/task/${child.pid}/children`, "utf8")).trim().split(/\s+/).map(Number);
  const handles = (await readFile(retainedChildren, "utf8")).trim().split("\n").map((line) => JSON.parse(line));
  const ownedPids = handles.map((handle) => handle.pid);
  assert.equal(ownedPids.length, 3, "expected retained daemon, UI and Playwright handles");
  assert.ok(ownedPids.includes(Number(await readFile(childReady, "utf8"))));
  assert.ok(ownedPids.every((pid) => pids.includes(pid)), "retained handles are not direct runner children");
  assert.ok(pids.length >= 3, "expected owned daemon, UI and Playwright children");
  const state = (await readdir(directory)).filter((name) => name.startsWith("factory-fixtures-"));
  assert.equal(state.length, 1);
  assert.equal(handles.filter((handle) => handle.ui).length, 1);
  return { child, directory, state: state[0], pids, ownedPids, uiPid: handles.find((handle) => handle.ui).pid };
}

test("runner SIGTERM retains cancellation status and cleans owned children and state", async (t) => {
  const { child, directory, state, pids, ownedPids } = await browserRunner(t);
  child.signal();
  assert.deepEqual(await bounded(child.leaderExited, "runner cancellation before supervisor fallback", 7500),
    { code: 143, signal: null }, child.errors);
  await until(async () => {
    for (const pid of ownedPids) {
      try { await readFile(`/proc/${pid}/stat`); return false; }
      catch (error) { if (error.code !== "ENOENT") throw error; }
    }
    return true;
  }, "owned child survived runner cancellation", 2000);
  assert.ok(!(await readdir(directory)).includes(state), "owned fixture directory survived cancellation");
  child.releaseCleanup();
  assert.deepEqual(await child.exited, { code: 143, signal: null }, child.errors);
  for (const pid of pids) await assert.rejects(readFile(`/proc/${pid}/stat`), { code: "ENOENT" });
});

test("owned UI exit fails active browser verification and drains retained children", async (t) => {
  const { child, directory, state, ownedPids, uiPid } = await browserRunner(t);
  process.kill(uiPid, "SIGKILL");
  assert.deepEqual(await bounded(child.leaderExited, "UI exit failure before supervisor fallback", 7500),
    { code: 1, signal: null }, child.errors);
  assert.match(child.errors, /Owned UI exited during browser verification/);
  for (const pid of ownedPids) await assert.rejects(readFile(`/proc/${pid}/stat`), { code: "ENOENT" });
  assert.ok(!(await readdir(directory)).includes(state), "fixture state survived UI failure");
  child.releaseCleanup();
  assert.deepEqual(await child.exited, { code: 1, signal: null }, child.errors);
});

test("runner SIGTERM during an unresponsive build uses one bounded cleanup window", async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "factory-runner-build-signal-test-"));
  const childReady = join(directory, "build-ready");
  await writeFile(join(directory, "go"), `#!${process.execPath}
const { writeFileSync } = require("node:fs");
process.on("SIGTERM", () => {});
writeFileSync(process.env.PROBE_BUILD_READY, String(process.pid));
setInterval(() => {}, 1000);
`, { mode: 0o755 });
  const web = fileURLToPath(new URL("../web/", import.meta.url));
  const command = ownedCommand(t, [process.execPath, join(web, "fixture-runner.mjs")], {
    cwd: web,
    env: { ...process.env, PATH: directory + ":" + process.env.PATH,
      TMPDIR: directory, PROBE_BUILD_READY: childReady,
      CODE_FACTORY_REAL_CODEX: "0", CODE_FACTORY_TEST_BASE_PORT: undefined },
  });
  t.after(() => bounded(rm(directory, { recursive: true, force: true }), "build signal probe removal"));
  const child = await command;
  await until(async () => (await readdir(directory)).includes("build-ready"), "Go build never started: " + child.errors);
  const buildPid = Number(await readFile(childReady, "utf8"));
  const pids = (await readFile(`/proc/${child.pid}/task/${child.pid}/children`, "utf8")).trim().split(/\s+/).map(Number);
  assert.deepEqual(pids, [buildPid], "only the build should exist before fixture startup");
  const state = (await readdir(directory)).filter((name) => name.startsWith("factory-fixtures-"));
  assert.equal(state.length, 1);
  child.signal();
  const cancelled = bounded(child.exited, "build cancellation", 7500);
  cancelled.catch(() => {});
  await new Promise((accept) => setTimeout(accept, 3000));
  child.signal();
  assert.deepEqual(await cancelled, { code: 143, signal: null }, child.errors);
  await until(async () => {
    try { await readFile(`/proc/${buildPid}/stat`); return false; }
    catch (error) { if (error.code !== "ENOENT") throw error; return true; }
  }, "owned build survived runner cancellation", 2000);
  assert.ok(!(await readdir(directory)).includes(state[0]), "owned fixture directory survived build cancellation");
});

test("occupied explicit Vite port rejects instead of selecting or reusing a server", async (t) => {
  const a = await fixture(t), b = await fixture(t);
  const origin = await bounded(listenFixture(a), "Vite listen");
  await assert.rejects(bounded(listenFixture(b, +new URL(origin).port), "occupied Vite listen"), { code: "EADDRINUSE" });
  assert.equal((await request(origin + "/@vite/client")).status, 200);
});

test("performance configuration spellings select the owned performance fixture", async (t) => {
  const web = fileURLToPath(new URL("../web/", import.meta.url));
  const config = "playwright.performance.config.ts";
  for (const args of [["-c", config], ["-c", "./" + config],
    ["--config", "./" + config], ["--config", join(web, config)],
    ["--config=./" + config], ["--config=" + join(web, config)],
    ["-c./" + config]]) {
    await t.test(args.join(" "), async (t) => {
      const child = await ownedCommand(t, [process.execPath, join(web, "fixture-runner.mjs"),
        ...args, "--list", "--reporter=json"], { cwd: web,
        env: { ...process.env, CODE_FACTORY_REAL_CODEX: "0" } });
      assert.deepEqual(await child.exited, { code: 0, signal: null }, child.errors);
      assert.match(child.errors, /Owned fixtures: ui=unused daemon=http:\/\/127\.0\.0\.1:/);
      const report = JSON.parse(child.output);
      assert.equal(report.errors.length, 0);
      assert.equal(report.config.configFile, join(web, config));
    });
  }
});

test("config-like option values and positional arguments retain owned correctness fixtures", async (t) => {
  const web = fileURLToPath(new URL("../web/", import.meta.url));
  const directory = await mkdtemp(join(tmpdir(), "factory-cli-options-test-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const reporter = join(directory, "probe.mjs");
  await writeFile(reporter, `export default class {
  async onEnd() {
    const ui = process.env.CODE_FACTORY_TEST_UI_ORIGIN;
    const daemon = process.env.CODE_FACTORY_TEST_DAEMON_ORIGIN;
    const health = await fetch(daemon + "/healthz", { signal: AbortSignal.timeout(5000) });
    const client = ui && await fetch(ui + "/@vite/client", { signal: AbortSignal.timeout(5000) });
    console.log("FIXTURE_PROBE=" + JSON.stringify({
      owned: process.env.CODE_FACTORY_TEST_FIXTURES_OWNED,
      ui, daemon, health: await health.text(), clientStatus: client?.status,
      client: client && await client.text(),
    }));
  }
}
`);
  for (const args of [["--grep-invert", "--config=playwright.performance.config.ts"],
    ["-G", "-cplaywright.performance.config.ts"],
    ["--", "--config=playwright.performance.config.ts"]]) {
    await t.test(args.join(" "), async (t) => {
      const child = await ownedCommand(t, [process.execPath, join(web, "fixture-runner.mjs"),
        "--list", "--reporter", reporter, ...args], { cwd: web,
        env: { ...process.env, CODE_FACTORY_REAL_CODEX: "0", CODE_FACTORY_TEST_BASE_PORT: undefined,
          CODE_FACTORY_TEST_UI_ORIGIN: undefined, CODE_FACTORY_TEST_DAEMON_ORIGIN: undefined },
      });
      assert.deepEqual(await child.exited, { code: 0, signal: null }, child.errors);
      const probe = JSON.parse(child.output.split("\n").find((line) => line.startsWith("FIXTURE_PROBE=")).slice(14));
      assert.equal(probe.owned, "1");
      assert.match(probe.ui ?? "", /^http:\/\/127\.0\.0\.1:[1-9][0-9]*$/,
        JSON.stringify({ ui: probe.ui, health: probe.health }));
      assert.notEqual(probe.ui, probe.daemon);
      assert.deepEqual(JSON.parse(probe.health), { ready: true });
      assert.equal(probe.clientStatus, 200);
      assert.match(probe.client, /WebSocket/);
    });
  }
});
