import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { mockApi, P1 } from "./mock-api";

// Mocked-network accessibility pass over every screen, both themes (iteration aid; the real run is in e2e-ui/ui.spec.ts).
const tabs = ["overview", "deployments", "builds", "runtime", "domains", "secrets", "addons", "databases", "storage", "logs", "metrics", "repository", "settings"];
const paths = ["/", "/projects", "/git", "/ai", "/team", "/keys", "/sessions", "/security", "/cluster", "/platform", "/settings", ...tabs.map((t) => `/project?id=${P1}&tab=${t}`)];

for (const theme of ["light", "dark"]) {
  for (const path of paths) {
    test(`axe ${theme} ${path}`, async ({ page }) => {
      await page.addInitScript((t) => localStorage.setItem("jp_theme", t), theme);
      await mockApi(page);
      await page.goto(path);
      await page.waitForTimeout(900);
      const r = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
      const bad = r.violations.filter((v) => v.impact === "serious" || v.impact === "critical");
      expect(bad.map((v) => `${v.id} x${v.nodes.length}: ${v.nodes[0]?.html.slice(0, 120)} ${JSON.stringify(v.nodes[0]?.any?.[0]?.data ?? "").slice(0, 150)}`)).toEqual([]);
    });
  }
}
