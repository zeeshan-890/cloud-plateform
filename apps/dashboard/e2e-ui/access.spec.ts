import { expect, test, type Page } from "@playwright/test";
import { adminTokens, authHeaders, createManagedUser, selectOrg, signInWith, uid } from "./helpers";

/** Admin creates a restricted user through the UI; that user then signs in and sees only what was granted. */
test.describe("admin-managed restricted users", () => {
  test("create user with a viewer grant, forced password change, restricted sidebar and no-access states", async ({ page, request, browser }) => {
    const admin = await adminTokens(request);
    const tag = uid();
    const o = await request.post("/api/v1/orgs", { headers: authHeaders(admin), data: { name: `Access ${tag}`, slug: `acc-${tag}` } });
    expect(o.ok(), await o.text()).toBeTruthy();
    const ob = (await o.json()) as { org?: { id: string }; id?: string };
    const orgId = (ob.org?.id ?? ob.id)!;
    const mk = async (name: string) => {
      const r = await request.post(`/api/v1/orgs/${orgId}/projects`, { headers: authHeaders(admin), data: { name, slug: name.toLowerCase().replace(/\s+/g, "-") } });
      expect(r.ok(), await r.text()).toBeTruthy();
      const b = (await r.json()) as { project?: { id: string }; id?: string };
      return (b.project?.id ?? b.id)!;
    };
    const granted = await mk(`Granted ${tag}`);
    const hidden = await mk(`Hidden ${tag}`);

    // ---- admin: Team & access -> Create user
    await signInWith(page, admin);
    await selectOrg(page, orgId);
    await page.goto("/team");
    await page.getByTestId("user-create").click();
    const username = `rita.${tag}`.toLowerCase().slice(0, 30);
    await page.getByTestId("user-name").fill("Rita Restricted");
    await page.getByTestId("user-username").fill(username);
    await page.getByTestId("grant-add-select").selectOption(granted);
    await page.getByTestId("grant-add").click();
    await expect(page.getByTestId("preset-select")).toHaveValue("viewer");
    await page.getByTestId("user-create-submit").click();

    const creds = page.getByTestId("credentials");
    await expect(creds).toBeVisible();
    await expect(creds).toContainText("will not be shown again");
    const password = (await page.getByTestId("cred-password").innerText()).trim();
    expect(password.length).toBeGreaterThanOrEqual(12);
    await expect(page.getByTestId("cred-username")).toHaveText(username);
    await page.getByTestId("cred-done").click();
    await expect(page.getByRole("row", { name: new RegExp(username) })).toBeVisible();

    // ---- restricted user: login with USERNAME, forced password change
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
    const rp = await ctx.newPage();
    await rp.goto("/login");
    await rp.getByTestId("login-email").fill(username);
    await rp.getByTestId("login-password").fill(password);
    await rp.getByTestId("login-submit").click();
    await expect(rp).toHaveURL(/\/change-password/);
    await rp.getByTestId("pw-current").fill(password);
    await rp.getByTestId("pw-new").fill("Brand-New-Passw0rd!" + tag);
    await rp.getByTestId("pw-again").fill("Brand-New-Passw0rd!" + tag);
    await rp.getByTestId("pw-submit").click();
    await expect(rp.getByRole("navigation", { name: "Organization" })).toBeVisible();

    // level 1: no Team / Git connections / Platform, only the granted project
    const org = rp.getByRole("navigation", { name: "Organization" });
    await expect(org.getByRole("link", { name: "Team & access" })).toHaveCount(0);
    await expect(org.getByRole("link", { name: "Git connections" })).toHaveCount(0);
    await expect(org.getByText("Platform", { exact: true })).toHaveCount(0);
    await rp.goto("/projects");
    await expect(rp.getByRole("heading", { name: new RegExp(`Granted ${tag}`, "i") })).toBeVisible();
    await expect(rp.getByText(new RegExp(`Hidden ${tag}`, "i"))).toHaveCount(0);
    await expect(rp.getByRole("link", { name: "New project" })).toHaveCount(0);

    // level 2: only viewer areas
    await rp.goto(`/project?id=${granted}`);
    const proj = rp.getByRole("navigation", { name: "Project" });
    for (const name of ["Overview", "Deployments", "Builds", "Runtime", "Domains", "Logs", "Metrics"]) await expect(proj.getByRole("link", { name })).toBeVisible();
    for (const name of ["Environment & secrets", "Storage", "Add-ons", "Databases", "Repository", "Settings"]) await expect(proj.getByRole("link", { name: new RegExp(name.replace("&", "\\&")) })).toHaveCount(0);

    // view-only: banner shown and manage actions hidden
    await rp.goto(`/project?id=${granted}&tab=deployments`);
    await expect(rp.getByTestId("view-only")).toBeVisible();
    await expect(rp.getByTestId("deploy-open")).toHaveCount(0);
    await rp.goto(`/project?id=${granted}&tab=domains`);
    await expect(rp.getByTestId("domain-add")).toHaveCount(0);

    // forbidden area by direct URL: friendly state, not an error page
    await rp.goto(`/project?id=${granted}&tab=secrets`);
    await expect(rp.getByText("No access to this section").first()).toBeVisible();
    // a project that was never granted
    await rp.goto(`/project?id=${hidden}`);
    await expect(rp.getByText(/No access to this/).first()).toBeVisible();

    // ---- admin upgrades the user to a custom grant: secrets manage + reveal
    await page.goto("/team");
    await page.getByRole("button", { name: new RegExp(`Edit access for ${username}`) }).click();
    await page.getByTestId("preset-select").selectOption("custom");
    await page.getByRole("radiogroup", { name: "Environment & secrets access" }).getByRole("radio", { name: "manage" }).click();
    await page.getByRole("switch", { name: /reveal secret values/i }).click();
    await page.getByTestId("user-save").click();
    await expect(page.getByRole("dialog")).toHaveCount(0);

    await rp.reload();
    await expectSecretsVisible(rp, granted);
    await ctx.close();
  });

  test("a wrong temporary password is rejected and disabled users cannot sign in", async ({ request, page }) => {
    const admin = await adminTokens(request);
    const tag = uid();
    const o = await request.post("/api/v1/orgs", { headers: authHeaders(admin), data: { name: `Dis ${tag}`, slug: `dis-${tag}` } });
    const ob = (await o.json()) as { org?: { id: string }; id?: string };
    const orgId = (ob.org?.id ?? ob.id)!;
    const username = `dis.${tag}`.toLowerCase().slice(0, 30);
    const made = await createManagedUser(request, admin, orgId, { username });
    await page.goto("/login");
    await page.getByTestId("login-email").fill(username);
    await page.getByTestId("login-password").fill("not-the-password");
    await page.getByTestId("login-submit").click();
    await expect(page.locator("[role=alert]:not(#__next-route-announcer__)")).toBeVisible();

    const d = await request.patch(`/api/v1/orgs/${orgId}/users/${made.id}`, { headers: authHeaders(admin), data: { disabled: true } });
    expect(d.ok()).toBeTruthy();
    await page.getByTestId("login-password").fill(made.password);
    await page.getByTestId("login-submit").click();
    await expect(page.locator("[role=alert]:not(#__next-route-announcer__)")).toBeVisible();
    await expect(page).toHaveURL(/\/login/);
  });
});

async function expectSecretsVisible(rp: Page, projectId: string) {
  await rp.goto(`/project?id=${projectId}&tab=secrets`);
  await expect(rp.getByRole("heading", { level: 1 })).toHaveText(/environment & secrets/i);
  await expect(rp.getByTestId("secret-add")).toBeVisible();
}
