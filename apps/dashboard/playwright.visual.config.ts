import { defineConfig } from "@playwright/test";

// Visual iteration only: serves the static export and mocks the API in-browser.
export default defineConfig({
  testDir: "./e2e-ui/visual",
  timeout: 30000,
  workers: 2,
  use: { baseURL: "http://localhost:3100" },
  webServer: { command: "node scripts/serve-out.mjs", env: { PORT: "3100" }, url: "http://localhost:3100/login/", reuseExistingServer: true },
  projects: [
    { name: "desktop", use: { viewport: { width: 1440, height: 900 } } },
    { name: "tablet", use: { viewport: { width: 820, height: 1100 } } },
    { name: "mobile", use: { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } },
  ],
});
