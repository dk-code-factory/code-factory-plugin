import { defineConfig } from "@playwright/test";
import { fixtureDaemonOrigin } from "./fixture-ports";
export default defineConfig({
  testDir: "./performance/tests",
  workers: 1,
  retries: 0,
  fullyParallel: false,
  timeout: 120000,
  outputDir: "performance-results/failures",
  use: {
    headless: true,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  reporter: "list",
  webServer: {
    command: "cd .. && CODE_FACTORY_PERFORMANCE=1 go run ./internal/testhost",
    url: fixtureDaemonOrigin + "/healthz",
    reuseExistingServer: false,
    timeout: 120000,
  },
});
