import { defineConfig } from "@playwright/test";
import {
  fixtureUIPort,
  fixtureUIOrigin,
  fixtureDaemonOrigin,
} from "./fixture-ports";
export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  // Bound concurrency independently of the machine's reported CPU count.
  workers: 2,
  retries: 0,
  projects: [
    {
      name: "mocked",
      testIgnore: "assembled.spec.ts",
      fullyParallel: true,
    },
    {
      // These scenarios share one daemon database and Git fixture. Keep their
      // original order and never execute them concurrently, even with -j.
      name: "assembled",
      testMatch: "assembled.spec.ts",
      fullyParallel: false,
      workers: 1,
    },
  ],
  use: { baseURL: fixtureUIOrigin, headless: true },
  webServer: [
    {
      command: `npx vite --host 127.0.0.1 --port ${fixtureUIPort} --strictPort`,
      url: fixtureUIOrigin,
      reuseExistingServer: false,
    },
    {
      command: "cd .. && go run ./internal/testhost",
      url: `${fixtureDaemonOrigin}/healthz`,
      reuseExistingServer: false,
    },
  ],
  reporter: "list",
});
