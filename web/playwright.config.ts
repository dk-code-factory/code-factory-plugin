import { defineConfig } from "@playwright/test";

function fixturePort(name: string, fallback: number) {
  const value = process.env[name];
  if (value === undefined) return fallback;
  const port = Number(value);
  if (
    !/^\d+$/.test(value) ||
    !Number.isInteger(port) ||
    port < 1 ||
    port > 65535
  )
    throw new Error(`${name} must be an integer port from 1 to 65535`);
  return port;
}
const uiPort = fixturePort("CODE_FACTORY_TEST_UI_PORT", 4173);
const hostPort = fixturePort("CODE_FACTORY_TEST_HOST_PORT", 4174);

export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  use: { baseURL: `http://127.0.0.1:${uiPort}`, headless: true },
  webServer: [
    {
      command: `npx vite --host 127.0.0.1 --port ${uiPort} --strictPort`,
      url: `http://127.0.0.1:${uiPort}`,
      reuseExistingServer: false,
    },
    {
      command: "cd .. && go run ./internal/testhost",
      url: `http://127.0.0.1:${hostPort}/healthz`,
      reuseExistingServer: false,
    },
  ],
  reporter: "list",
});
