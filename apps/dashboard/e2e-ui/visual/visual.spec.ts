import { test } from "@playwright/test";
import { mockApi, P1 } from "./mock-api";

const SHOTS = process.env.SHOT_DIR || "shots";

const tab = (t: string) => `/project?id=${P1}&tab=${t}`;
const pages: Array<[string, string]> = [
  ["home", "/"],
  ["projects", "/projects"],
  ["project", tab("overview")],
  ["domains", tab("domains")],
  ["addons", tab("addons")],
  ["secrets", tab("secrets")],
  ["settings", "/settings"],
  ["team", "/team"],
  ["cluster", "/cluster"],
  ["security", "/security"],
  ["runtime", tab("runtime")],
  ["builds", tab("builds")],
  ["metrics", tab("metrics")],
  ["storage", tab("storage")],
];

for (const theme of ["light", "dark"] as const) {
  test.describe(`visual ${theme}`, () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((t) => localStorage.setItem("jp_theme", t), theme);
      await mockApi(page);
    });
    for (const [name, path] of pages) {
      test(`${name}`, async ({ page }, info) => {
        await page.goto(path);
        await page.waitForTimeout(1200);
        await page.screenshot({ path: `${SHOTS}/${theme}-${info.project.name}-${name}.png`, fullPage: false });
      });
    }
    test("login", async ({ page }, info) => {
      await page.unrouteAll();
      await page.addInitScript(() => localStorage.clear());
      await page.route("**/api/v1/auth/bootstrap/status", (r) => r.fulfill({ json: { initialized: true, registration: "invite" } }));
      await page.goto("/login");
      await page.waitForTimeout(800);
      await page.screenshot({ path: `${SHOTS}/${theme}-${info.project.name}-login.png` });
    });
  });
}
