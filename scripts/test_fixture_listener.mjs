import assert from "node:assert/strict";
import { test } from "node:test";
import { createRequire } from "node:module";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
const { createServer } = await import(createRequire(new URL("../web/package.json", import.meta.url)).resolve("vite"));
import { listenFixture } from "../web/fixture-listener.mjs";

async function fixture(t) {
  const cacheDir = await mkdtemp(join(tmpdir(), "factory-vite-test-"));
  let server;
  t.after(async () => { await server?.close(); await rm(cacheDir, { recursive: true, force: true }); });
  server = await createServer({ configFile: false, cacheDir, logLevel: "silent", server: { host: "127.0.0.1", port: 0 } });
  return server;
}

test("concurrent Vite fixtures retain distinct kernel allocations", async (t) => {
  const a = await fixture(t), b = await fixture(t);
  const [first, second] = await Promise.all([listenFixture(a), listenFixture(b)]);
  assert.notEqual(first, second);
  for (const origin of [first, second]) {
    assert.match(origin, /^http:\/\/127\.0\.0\.1:[1-9][0-9]*$/);
    assert.equal((await fetch(origin + "/@vite/client")).status, 200);
  }
});

test("occupied explicit Vite port rejects instead of selecting or reusing a server", async (t) => {
  const a = await fixture(t), b = await fixture(t);
  const origin = await listenFixture(a);
  await assert.rejects(listenFixture(b, +new URL(origin).port), { code: "EADDRINUSE" });
  assert.equal((await fetch(origin + "/@vite/client")).status, 200);
});

test("performance configuration spellings select the owned performance fixture", async (t) => {
  const web = fileURLToPath(new URL("../web/", import.meta.url));
  const config = "playwright.performance.config.ts";
  for (const args of [["-c", config], ["-c", "./" + config],
    ["--config", "./" + config], ["--config", join(web, config)],
    ["--config=./" + config], ["--config=" + join(web, config)],
    ["-c./" + config]]) {
    await t.test(args.join(" "), async (t) => {
      const child = spawn(process.execPath, [join(web, "fixture-runner.mjs"),
        ...args, "--list", "--reporter=json"], { cwd: web,
        env: { ...process.env, CODE_FACTORY_REAL_CODEX: "0" }, stdio: ["ignore", "pipe", "pipe"] });
      let output = "", errors = "";
      child.stdout.on("data", (chunk) => { output += chunk; });
      child.stderr.on("data", (chunk) => { errors += chunk; });
      const exited = new Promise((accept, reject) => {
        child.once("error", reject);
        child.once("exit", (code) => accept(code));
      });
      t.after(async () => { if (child.exitCode === null) child.kill("SIGKILL"); await exited; });
      const deadline = setTimeout(() => child.kill("SIGTERM"), 30000);
      const killer = setTimeout(() => child.kill("SIGKILL"), 40000);
      t.after(() => { clearTimeout(deadline); clearTimeout(killer); });
      assert.equal(await exited, 0, errors);
      assert.match(errors, /Owned fixtures: ui=unused daemon=http:\/\/127\.0\.0\.1:/);
      const report = JSON.parse(output);
      assert.equal(report.errors.length, 0);
      assert.equal(report.config.configFile, join(web, config));
    });
  }
});
