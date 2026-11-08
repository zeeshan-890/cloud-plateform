import { expect, test } from "@playwright/test";
import { adminTokens, createOrgAndProject, selectOrg, shellReady, signInWith } from "./helpers";

test.describe("two-level navigation", () => {
  test("level 1 has org pages only; opening a project swaps the sidebar for the project workspace", async ({ page, request }) => {
    const t = await adminTokens(request);
    const { orgId, projectId } = await createOrgAndProject(request, t);
    await signInWith(page, t);
    await selectOrg(page, orgId);

    await page.goto("/");
    const org = page.getByRole("navigation", { name: "Organization" });
    for (const name of ["Overview", "Projects", "Team & access", "Git connections", "Access keys", "Sessions", "Account settings"]) {
      await expect(org.getByRole("link", { name })).toBeVisible();
    }
    await expect(org.getByRole("link", { name: "Domains" })).toHaveCount(0);
    await expect(page.getByLabel("Active project")).toHaveCount(0); // no global project dropdown

    await page.goto("/projects");
    await page.getByRole("link", { name: /^Open$/ }).first().click();
    await expect(page).toHaveURL(/\/project\/?\?id=/);

    const proj = page.getByRole("navigation", { name: "Project" });
    await expect(proj).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Organization" })).toHaveCount(0);
    await expect(proj.getByRole("link", { name: "All projects" })).toBeVisible();
    for (const name of ["Overview", "Deployments", "Builds", "Runtime", "Domains", "Environment & secrets", "Add-ons", "Databases", "Storage", "Logs", "Metrics", "Repository", "Settings"]) {
      await expect(proj.getByRole("link", { name: new RegExp(`^${name.replace("&", "\\&")}`) })).toBeVisible();
    }
    await expect(page.getByRole("navigation", { name: "Breadcrumb" })).toContainText("Overview");

    // browser history works between workspace pages and back to level 1
    await proj.getByRole("link", { name: "Domains" }).click();
    await expect(page).toHaveURL(/tab=domains/);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(/domains/i);
    await page.goBack();
    await expect(page).toHaveURL(/\/project\/?\?id=/);
    await page.goForward();
    await expect(page).toHaveURL(/tab=domains/);

    // deep link
    await page.goto(`/project?id=${projectId}&tab=secrets`);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(/environment & secrets/i);
    await expect(page.getByRole("navigation", { name: "Project" }).getByRole("link", { name: /Environment/ })).toHaveAttribute("aria-current", "page");

    await proj.getByRole("link", { name: "All projects" }).click();
    await expect(page).toHaveURL(/\/projects/);
    await expect(page.getByRole("navigation", { name: "Organization" })).toBeVisible();
  });

  test("Ctrl+K lists both levels and projects", async ({ page, request }) => {
    const t = await adminTokens(request);
    const { orgId, tag } = await createOrgAndProject(request, t);
    await signInWith(page, t);
    await selectOrg(page, orgId);
    await page.goto("/");
    await shellReady(page);
    await page.keyboard.press("Control+k");
    const box = page.getByRole("combobox", { name: "Search commands" });
    await box.fill("team");
    await expect(page.getByRole("option", { name: /Team/ })).toBeVisible();
    await box.fill(`proj ${tag}`);
    await expect(page.getByRole("option", { name: new RegExp(`Proj ${tag}`, "i") }).first()).toBeVisible();
    await box.fill("domains");
    await expect(page.getByRole("option", { name: /Domains/ }).first()).toBeVisible();
  });

  test("mobile: bottom bar for the main items and a drawer with the full menu", async ({ browser, request }) => {
    const t = await adminTokens(request);
    const { orgId, projectId } = await createOrgAndProject(request, t);
    const ctx = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
    const page = await ctx.newPage();
    await signInWith(page, t);
    await selectOrg(page, orgId);
    await page.goto(`/project?id=${projectId}`);
    const bar = page.getByRole("navigation", { name: "Quick navigation" });
    await expect(bar.getByRole("link")).toHaveCount(4);
    await bar.getByRole("button", { name: "Open full menu" }).click();
    const drawer = page.getByRole("dialog", { name: "Menu" });
    await expect(drawer.getByRole("link", { name: "All projects" })).toBeVisible();
    await expect(drawer.getByRole("link", { name: "Add-ons" })).toBeVisible();
    await ctx.close();
  });
});
