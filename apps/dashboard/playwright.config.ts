import { defineConfig } from "@playwright/test";

/**
 * Real end-to-end tests. They run against a deployed JP Cloud (gateway + dashboard + backends):
 *   BASE_URL=http://host:3000 E2E_ADMIN_EMAIL=... E2E_ADMIN_PASSWORD=... npx playwright test
 * Nothing here mocks the network. (Mocked screenshots live in e2e-ui/visual, run with playwright.visual.config.ts.)
 */
export default defineConfig({
  testDir: "./e2e-ui",
  testIgnore: ["visual/**"],
  timeout: 180_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [["list"], ["html", { open: "never", outputFolder: "playwright-report" }]],
  use: {
    baseURL: process.env.BASE_URL || "http://localhost:3000",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "desktop", use: { viewport: { width: 1440, height: 900 } } }],
});
