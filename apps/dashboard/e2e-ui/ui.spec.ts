import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { adminTokens, shellReady, signInWith } from "./helpers";

test.describe("theme", () => {
  test("toggle switches theme, persists across reload, and is applied before first paint", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "light" });
    await page.goto("/login");
    const html = page.locator("html");
    await expect(html).toHaveAttribute("data-theme", "light");
    await page.getByRole("switch", { name: "Night shift theme" }).click();
    await expect(html).toHaveAttribute("data-theme", "dark");
    await page.reload();
    await expect(html).toHaveAttribute("data-theme", "dark");
    expect(await page.evaluate(() => localStorage.getItem("jp_theme"))).toBe("dark");

    // no flash: the attribute is already present when the document starts rendering
    const early = await page.evaluate(
      () =>
        new Promise<string | null>((resolve) => {
          const w = window.open(location.href)!;
          const t = setInterval(() => {
            const v = w.document.documentElement.getAttribute("data-theme");
            if (v) {
              clearInterval(t);
              w.close();
              resolve(v);
            }
          }, 1);
        }),
    );
    expect(early).toBe("dark");
  });

  test("respects prefers-color-scheme when nothing is stored", async ({ browser }) => {
    const ctx = await browser.newContext({ colorScheme: "dark" });
    const p = await ctx.newPage();
    await p.goto("/login");
    await expect(p.locator("html")).toHaveAttribute("data-theme", "dark");
    await ctx.close();
  });
});

test.describe("mobile layout", () => {
  test("sidebar becomes a bottom rack and a drawer", async ({ browser, request }) => {
    const ctx = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
    const page = await ctx.newPage();
    await signInWith(page, await adminTokens(request));
    await page.goto("/");
    await expect(page.getByRole("navigation", { name: "Quick navigation" })).toBeVisible();
    await expect(page.getByRole("complementary", { name: "Sidebar" })).toBeHidden();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await page.getByRole("button", { name: "Open full menu" }).click();
    await expect(page.getByRole("dialog", { name: "Menu" })).toBeVisible();
    await page.screenshot({ path: "playwright-report/mobile-drawer.png" });
    await ctx.close();
  });
});

test.describe("command palette", () => {
  test("Ctrl+K opens, filters and navigates", async ({ page, request }) => {
    await signInWith(page, await adminTokens(request));
    await page.goto("/");
    await shellReady(page);
    await page.keyboard.press("Control+k");
    const dialog = page.getByRole("dialog", { name: "Command palette" });
    await expect(dialog).toBeVisible();
    await page.getByRole("combobox", { name: "Search commands" }).fill("sessions");
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/sessions/);
  });
});

test.describe("accessibility smoke (axe)", () => {
  const publicPages = ["/login"];
  const authed = ["/", "/projects", "/team", "/settings", "/keys", "/sessions", "/git", "/platform"];

  for (const path of publicPages) {
    test(`no serious violations on ${path}`, async ({ page }) => {
      await page.goto(path);
      await page.waitForLoadState("networkidle");
      const r = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
      expect(r.violations.filter((v) => v.impact === "serious" || v.impact === "critical"), JSON.stringify(r.violations.map((v) => v.id))).toEqual([]);
    });
  }
  for (const theme of ["light", "dark"]) {
    for (const path of authed) {
      test(`no serious violations on ${path} (${theme})`, async ({ page, request }) => {
        await page.addInitScript((t) => localStorage.setItem("jp_theme", t), theme);
        await signInWith(page, await adminTokens(request));
        await page.goto(path);
        await page.waitForLoadState("networkidle");
        const r = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa"]).analyze();
        expect(r.violations.filter((v) => v.impact === "serious" || v.impact === "critical"), JSON.stringify(r.violations.map((v) => `${v.id}: ${v.nodes[0]?.html.slice(0, 80)}`))).toEqual([]);
      });
    }
  }
});
