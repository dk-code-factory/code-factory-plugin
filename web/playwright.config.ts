import { defineConfig } from "@playwright/test";
import {
  fixtureUIPort,
  fixtureUIOrigin,
  fixtureDaemonOrigin,
} from "./fixture-ports";
export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
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
