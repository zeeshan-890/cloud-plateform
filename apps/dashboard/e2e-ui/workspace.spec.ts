import { expect, test } from "@playwright/test";
import { adminTokens, authHeaders, createManagedUser, createOrgAndProject, loginTokens, selectOrg, signInWith, uid } from "./helpers";

test.describe("org and project", () => {
  test("create an organization and a project through the UI", async ({ page, request }) => {
    const t = await adminTokens(request);
    await signInWith(page, t);
    const tag = uid();

    await page.goto("/orgs/new");
    await page.getByTestId("org-name").fill(`UI Org ${tag}`);
    await page.getByTestId("org-submit").click();
    await expect(page).toHaveURL(/\/projects\/new/);

    await page.getByTestId("project-name").fill(`ui-proj-${tag}`);
    await page.getByTestId("project-submit").click();
    await expect(page).toHaveURL(/\/project\/?\?id=/);
    await expect(page.getByTestId("project-name-side")).toHaveText(new RegExp(`ui-proj-${tag}`, "i"));

    await page.goto("/projects");
    await expect(page.getByRole("link", { name: `ui-proj-${tag}`, exact: true })).toBeVisible();
  });

  test("the project's system subdomain is listed on the Domains tab", async ({ page, request }) => {
    const t = await adminTokens(request);
    const { orgId, projectId } = await createOrgAndProject(request, t);
    await signInWith(page, t);
    await selectOrg(page, orgId);
    await page.goto(`/project?id=${projectId}&tab=domains`);
    await expect(page.getByText("system subdomain")).toBeVisible();
  });
});

test.describe("secrets", () => {
  test("CRUD, masked by default, reveal for admins only", async ({ page, request }) => {
    const admin = await adminTokens(request);
    const { orgId, projectId, tag } = await createOrgAndProject(request, admin);
    await signInWith(page, admin);
    await selectOrg(page, orgId);
    await page.goto(`/project?id=${projectId}&tab=secrets`);

    await page.getByTestId("secret-add").click();
    await page.getByTestId("secret-name").fill("API_TOKEN");
    await page.getByTestId("secret-value").fill("s3cr3t-value-" + tag);
    await page.getByTestId("secret-save").click();
    const row = page.getByRole("row", { name: /API_TOKEN/ });
    await expect(row).toBeVisible();
    await expect(row).not.toContainText("s3cr3t-value");

    await row.getByRole("button", { name: "Reveal API_TOKEN" }).click();
    await expect(page.getByTestId("secret-value-API_TOKEN")).toHaveText("s3cr3t-value-" + tag);

    // rename + new value
    await row.getByRole("button", { name: "Edit API_TOKEN" }).click();
    await page.getByTestId("secret-name").fill("API_TOKEN_V2");
    await page.getByTestId("secret-value").fill("rotated");
    await page.getByTestId("secret-save").click();
    await expect(page.getByRole("row", { name: /API_TOKEN_V2/ })).toBeVisible();
    await expect(page.getByRole("row", { name: /API_TOKEN_V2/ })).toContainText("v2");

    // per-environment isolation
    await page.getByRole("tab", { name: "development" }).click();
    await expect(page.getByRole("row", { name: /API_TOKEN_V2/ })).toHaveCount(0);
    await page.getByRole("tab", { name: "production" }).click();

    // delete requires typing the key
    await page.getByRole("button", { name: "Delete API_TOKEN_V2" }).click();
    const confirm = page.getByRole("button", { name: "Delete variable" });
    await expect(confirm).toBeDisabled();
    await page.getByRole("dialog").getByRole("textbox").fill("API_TOKEN_V2");
    await confirm.click();
    await expect(page.getByRole("row", { name: /API_TOKEN_V2/ })).toHaveCount(0);
  });

  test("a member cannot reveal or delete secrets", async ({ page, request }) => {
    const admin = await adminTokens(request);
    const { orgId, projectId, tag } = await createOrgAndProject(request, admin);
    await request.post(`/api/v1/orgs/${orgId}/projects/${projectId}/environments/production/secrets`, { headers: authHeaders(admin), data: { name: "SHARED", value: "x" } });
    const username = `member.${tag}`.toLowerCase().slice(0, 30);
    const made = await createManagedUser(request, admin, orgId, { username, projectIds: [projectId], preset: "developer" });
    await signInWith(page, await loginTokens(request, username, made.password));
    await selectOrg(page, orgId);
    await page.goto(`/project?id=${projectId}&tab=secrets`);
    const row = page.getByRole("row", { name: /SHARED/ });
    await expect(row).toBeVisible();
    await expect(row.getByRole("button", { name: /Reveal/ })).toHaveCount(0);
    await expect(row.getByRole("button", { name: /Delete/ })).toHaveCount(0);
  });
});

test.describe("domains guide", () => {
  test("add -> DNS records from the API -> verify reports real diagnostics -> delete asks for the hostname", async ({ page, request }) => {
    const admin = await adminTokens(request);
    const { orgId, projectId, tag } = await createOrgAndProject(request, admin);
    await signInWith(page, admin);
    await selectOrg(page, orgId);
    await page.goto(`/project?id=${projectId}&tab=domains`);
    const host = `e2e-${tag}.example.test`;

    await page.getByTestId("domain-input").fill("not a host");
    await expect(page.getByTestId("domain-add")).toBeDisabled();
    await page.getByTestId("domain-input").fill(host);
    await page.getByTestId("domain-add").click();

    const card = page.getByTestId(`domain-${host}`);
    await expect(card).toBeVisible();
    const records = card.getByTestId("dns-records");
    await expect(records).toContainText("TXT");
    await expect(records).toContainText("jp-verify=");

    await card.getByTestId("verify-domain").click();
    await expect(card.getByTestId("verify-result")).toContainText("Not verified yet");
    await expect(card.getByTestId("ssl-status")).toBeVisible();

    await card.getByRole("button", { name: `Delete ${host}` }).click();
    const del = page.getByRole("button", { name: "Delete domain" });
    await expect(del).toBeDisabled();
    await page.getByRole("dialog").getByRole("textbox").fill(host);
    await del.click();
    await expect(card).toHaveCount(0);
  });
});

test.describe("add-ons", () => {
  test("provision Redis, watch it become ready, see masked connection and injected variable, then delete", async ({ page, request }) => {
    test.setTimeout(240_000);
    const admin = await adminTokens(request);
    const { orgId, projectId } = await createOrgAndProject(request, admin);
    await signInWith(page, admin);
    await selectOrg(page, orgId);
    await page.goto(`/project?id=${projectId}&tab=addons`);

    await expect(page.getByTestId("catalog-count")).toContainText(/of \d+ engines/);
    await page.getByTestId("addon-search").fill("redis");
    await page.getByTestId("catalog-redis").getByRole("button", { name: "Provision Redis" }).click();
    await page.getByTestId("provision-name").fill("cache-e2e");
    await page.getByTestId("provision-submit").click();

    const card = page.getByTestId("addon-cache-e2e");
    await expect(card).toBeVisible();
    await expect(card).toContainText(/ready/i, { timeout: 150_000 });

    await card.getByRole("button", { name: "Connection" }).click();
    const url = page.getByTestId("conn-url");
    await expect(url).toBeVisible();
    await expect(url).not.toContainText(/:[^*•@/]{8,}@/); // password is masked
    await expect(page.getByRole("dialog")).toContainText("REDIS_URL");
    await page.getByRole("button", { name: "Close dialog" }).click();

    await card.getByRole("button", { name: "Delete" }).click();
    await page.getByRole("dialog").getByRole("textbox").fill("cache-e2e");
    await page.getByRole("button", { name: "Delete add-on and data" }).click();
    await expect(card).toHaveCount(0, { timeout: 60_000 });
  });
});
