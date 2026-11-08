import { createHmac } from "node:crypto";
import { expect, test, type APIRequestContext } from "@playwright/test";
import { adminTokens, authHeaders, createOrgAndProject, selectOrg, signInWith } from "./helpers";

// A real public repository: connecting checks it on GitHub, and the worker clones it for the build below.
const REPO = process.env.E2E_GIT_REPO || "heroku/node-js-getting-started";
let SHA = "a".repeat(40); // replaced by the repository's real head commit before the real build

function signedPush(request: APIRequestContext, hookUrl: string, secret: string, opts: { files: string[]; message?: string; branch?: string }) {
  const body = JSON.stringify({
    ref: `refs/heads/${opts.branch ?? "main"}`,
    after: SHA,
    head_commit: { id: SHA, message: opts.message ?? "change", added: [], modified: opts.files, removed: [] },
    commits: [{ id: SHA, added: [], modified: opts.files, removed: [] }],
    repository: { full_name: REPO, default_branch: "main" },
  });
  const url = new URL(hookUrl);
  return request.post(`${url.pathname}${url.search}`, {
    data: body,
    headers: {
      "content-type": "application/json",
      "x-github-event": "push",
      "x-github-delivery": `e2e-${Date.now()}-${Math.random().toString(16).slice(2)}`,
      "x-hub-signature-256": "sha256=" + createHmac("sha256", secret).update(body).digest("hex"),
    },
  });
}

test.describe("continuous deployment settings", () => {
  test("monorepo project: root folder, one-time webhook secret, path filters, settings, and a real push into a missing folder", async ({ page, request }) => {
    test.setTimeout(240_000);
    const admin = await adminTokens(request);
    const { orgId, projectId } = await createOrgAndProject(request, admin);
    await signInWith(page, admin);
    await selectOrg(page, orgId);
    await page.goto(`/project?id=${projectId}&tab=repository`);

    // --- connect by URL, building the "backend" folder
    await page.getByLabel(/Root directory \(monorepo/).fill("backend");
    await page.getByLabel("Clone URL").fill(`https://github.com/${REPO}.git`);
    await page.getByRole("button", { name: "Connect URL" }).click();

    // the one-time webhook secret is shown (it used to be thrown away)
    const details = page.getByTestId("webhook-details");
    await expect(details).toBeVisible({ timeout: 30_000 });
    const hookUrl = (await page.getByTestId("webhook-url").innerText()).trim();
    const secret = (await page.getByTestId("webhook-secret").innerText()).trim();
    expect(hookUrl).toMatch(/\/api\/v1\/webhooks\/github\?c=[0-9a-f-]{36}$/);
    expect(secret).toMatch(/^[0-9a-f]{64}$/);
    await page.getByRole("button", { name: "Done" }).click();

    // --- the table summarises the CD setup
    const row = page.getByRole("row", { name: new RegExp(REPO) });
    await expect(row.getByText("Auto-deploy")).toBeVisible();
    await expect(row.getByText("/backend")).toBeVisible();

    // --- webhook deliveries: another project's folder never deploys this one; skip markers are honoured
    const other = await signedPush(request, hookUrl, secret, { files: ["frontend/src/App.tsx"] });
    expect(other.status()).toBe(200);
    const otherBody = await other.json();
    expect(otherBody.deployments_created).toBe(0);
    expect(JSON.stringify(otherBody.skipped)).toContain("folder");
    const skip = await (await signedPush(request, hookUrl, secret, { files: ["backend/main.go"], message: "wip [skip ci]" })).json();
    expect(skip.deployments_created).toBe(0);
    expect(JSON.stringify(skip.skipped)).toContain("commit message");
    expect((await signedPush(request, hookUrl, "wrong-secret", { files: ["backend/main.go"] })).status()).toBe(401);

    // --- settings dialog: values load, summary updates live, save persists
    await row.getByRole("button", { name: /Deployment settings/ }).click();
    const dlg = page.getByRole("dialog", { name: "Deployment settings" });
    await expect(dlg.getByLabel("Root directory")).toHaveValue("backend");
    await expect(dlg.getByTestId("cd-summary")).toContainText('builds the folder "backend"');
    await dlg.getByRole("switch", { name: "Auto-deploy" }).click();
    await expect(dlg.getByTestId("cd-summary")).toContainText("do not deploy by themselves");
    await dlg.getByLabel("Production branch").fill("release");
    await dlg.getByLabel("Ignore changes to").fill("**/*.md\ndocs/**");
    await dlg.getByRole("switch", { name: "Wait for CI" }).click();
    await expect(dlg.getByTestId("cd-summary")).toContainText("Changes only to");
    await dlg.getByTestId("cd-save").click();
    await expect(dlg).toBeHidden();
    await expect(row.getByText("Manual")).toBeVisible();
    await expect(row.getByText("after CI")).toBeVisible();
    await expect(row.getByText("path filters")).toBeVisible();

    // persisted server-side, and invalid input is refused with a clear message
    const saved = await (await request.get(`/api/v1/orgs/${orgId}/projects/${projectId}/repos`, { headers: authHeaders(admin) })).json();
    const r0 = saved.repos[0];
    expect(r0).toMatchObject({ root_dir: "backend", auto_deploy: false, deploy_branch: "release", require_ci: true, ignore_paths: ["**/*.md", "docs/**"] });
    await row.getByRole("button", { name: /Deployment settings/ }).click();
    await dlg.getByLabel("Root directory").fill("../outside");
    await dlg.getByTestId("cd-save").click();
    await expect(dlg.getByRole("alert")).toContainText("..");
    await dlg.getByLabel("Root directory").fill("backend");

    // folder browser against the real repository (public, no token): pick a folder
    await dlg.getByTestId("browse-folders").click();
    const browser = dlg.getByTestId("folder-browser");
    await expect(browser).toBeVisible();
    await expect(browser.getByText("Found here:")).toBeVisible({ timeout: 30_000 }); // package.json / Procfile-less Node app
    const firstFolder = browser.locator("ul button").first();
    const folderName = (await firstFolder.innerText()).trim();
    await firstFolder.click();
    await expect.poll(async () => (await browser.getByTestId("use-folder").innerText()).toLowerCase()).toContain(`"${folderName.toLowerCase()}"`); // navigated into it
    await browser.getByTestId("use-folder").click();
    await expect.poll(async () => (await dlg.getByLabel("Root directory").inputValue()).toLowerCase()).toBe(folderName.toLowerCase());

    // --- rotating the webhook secret: the old one stops working, the new one is shown once
    await dlg.getByRole("button", { name: /new secret/ }).click();
    await expect(dlg.getByTestId("webhook-secret")).toBeVisible();
    const fresh = (await dlg.getByTestId("webhook-secret").innerText()).trim();
    expect(fresh).not.toBe(secret);
    expect((await signedPush(request, hookUrl, secret, { files: ["backend/a"], branch: "release" })).status()).toBe(401);
    await dlg.getByRole("button", { name: "Cancel" }).click();

    // a real commit is needed for the worker to clone
    const head = await request.get(`https://api.github.com/repos/${REPO}/commits/main`, { headers: { accept: "application/vnd.github+json" } });
    expect(head.ok(), "GitHub must answer for the public test repository").toBeTruthy();
    SHA = (await head.json()).sha;

    // --- back to auto-deploy on branch "release", then a REAL push into a folder that does not exist in the repo:
    // webhook -> deployment -> worker clones the repository -> clear failure naming the folder (nothing is built)
    await request.patch(`/api/v1/orgs/${orgId}/projects/${projectId}/repos/${r0.id}`, {
      headers: authHeaders(admin),
      data: { auto_deploy: true, require_ci: false, root_dir: "does-not-exist", deploy_branch: "main", ignore_paths: [] },
    });
    const real = await signedPush(request, hookUrl, fresh, { files: ["does-not-exist/main.go"] });
    expect(real.status()).toBe(200);
    expect((await real.json()).deployments_created).toBe(1);
    await expect
      .poll(
        async () => {
          const l = await (await request.get(`/api/v1/orgs/${orgId}/projects/${projectId}/deployments`, { headers: authHeaders(admin) })).json();
          const d = (l.deployments as { status: string; error?: string; source?: string }[]).find((x) => x.source === "webhook");
          return d ? `${d.status}|${d.error ?? ""}` : "none";
        },
        { timeout: 180_000, intervals: [3000] },
      )
      .toMatch(/^failed\|.*root directory "does-not-exist" does not exist/);
  });
});
