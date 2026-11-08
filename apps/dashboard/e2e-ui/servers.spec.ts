import { expect, test } from "@playwright/test";
import { adminTokens, authHeaders, createManagedUser, createOrgAndProject, loginTokens, selectOrg, signInWith, uid } from "./helpers";

/** Cluster page: the admin records servers (public/private address, preferred path, ports) entirely from the UI. */
test.describe("server inventory", () => {
  test("add, validate, edit, persist across reload and remove a server", async ({ page, request }) => {
    const admin = await adminTokens(request);
    const name = `db-${uid()}`.toLowerCase().slice(0, 30);
    await signInWith(page, admin);
    await page.goto("/cluster");

    await page.getByTestId("server-add").click();
    await page.getByTestId("server-manual").click();
    await expect(page.getByTestId("server-save")).toBeDisabled(); // empty form is invalid

    await page.getByTestId("server-name").fill(name);
    await page.getByTestId("server-prefer").selectOption("private");
    await page.getByTestId("server-public").fill("92.4.70.208");
    // prefer=private without a private address is rejected before any request is sent
    await expect(page.getByRole("note")).toContainText("private address is required");
    await expect(page.getByTestId("server-save")).toBeDisabled();
    await page.getByTestId("server-private").fill("10.0.0.88");
    await page.getByTestId("server-add-port").click();
    await page.getByTestId("server-port-0").fill("5432");
    await page.getByTestId("server-scope-0").selectOption("private");
    await expect(page.getByTestId("server-save")).toBeEnabled();
    await page.getByTestId("server-save").click();

    const row = page.getByRole("row", { name: new RegExp(name) });
    await expect(row).toContainText("10.0.0.88");
    await expect(row).toContainText("92.4.70.208");
    await expect(row).toContainText("5432 - private");

    // what the UI saved is what the API stores
    const api = await request.get("/api/v1/cluster/servers", { headers: authHeaders(admin) });
    const stored = ((await api.json()) as { servers: { name: string; private_addr: string; prefer: string; ports: { port: number; scope: string }[] }[] }).servers.find((s) => s.name === name);
    expect(stored).toMatchObject({ private_addr: "10.0.0.88", prefer: "private", ports: [{ port: 5432, scope: "private" }] });

    // edit: switch the preferred path to public
    await page.getByTestId(`server-edit-${name}`).click();
    await page.getByTestId("server-prefer").selectOption("public");
    await page.getByTestId("server-save").click();
    await page.reload();
    await expect(page.getByRole("row", { name: new RegExp(name) })).toContainText("public");

    // remove with confirmation
    await page.getByTestId(`server-delete-${name}`).click();
    await page.getByRole("button", { name: "Remove server" }).click();
    await expect(page.getByRole("row", { name: new RegExp(name) })).toHaveCount(0);
    const after = await request.get("/api/v1/cluster/servers", { headers: authHeaders(admin) });
    expect(((await after.json()) as { servers: { name: string }[] }).servers.some((s) => s.name === name)).toBeFalsy();
  });

  test("join command: generate in the UI, run it (as the script would), see clear errors, then a healthy re-check", async ({ page, request }) => {
    const admin = await adminTokens(request);
    const name = `api-${uid()}`.toLowerCase().slice(0, 30);
    await signInWith(page, admin);
    await page.goto("/cluster");

    await page.getByTestId("server-add").click();
    await page.getByTestId("server-name").fill(name);
    await page.getByTestId("server-add-port").click();
    await page.getByTestId("server-port-0").fill("8080");
    await page.getByTestId("server-generate").click();

    const cmd = page.getByTestId("join-command");
    await expect(cmd).toContainText("curl -fsSL");
    await expect(cmd).toContainText("| sudo bash");
    await expect(page.getByTestId("join-waiting")).toContainText("Waiting for the server");
    const command = (await cmd.innerText()).trim();
    const token = /join\/([A-Za-z0-9_-]{40,})\/script/.exec(command)![1];

    // the script URL really serves a script, and the plan lists what to open
    const script = await request.get(`/api/v1/cluster/join/${token}/script`);
    expect(script.ok()).toBeTruthy();
    expect(await script.text()).toContain("#!/usr/bin/env bash");
    expect(await (await request.get(`/api/v1/cluster/join/${token}/plan`)).text()).toContain("open=8080 private");

    // the new server reports: its private network does not contain a peer -> clear mismatch error
    const peer = `peer-${uid()}`.toLowerCase().slice(0, 30);
    const seed = await request.put(`/api/v1/cluster/servers/${peer}`, { headers: authHeaders(admin), data: { name: peer, private_addr: "192.168.77.5", prefer: "private", ports: [{ port: 5432, scope: "private" }] } });
    expect(seed.ok(), await seed.text()).toBeTruthy();
    const rep = await request.post(`/api/v1/cluster/join/${token}/report`, {
      data: { hostname: name, private_ip: "10.0.0.5", private_cidr: "10.0.0.5/24", provider: "oracle", os: "Oracle Linux 9", firewall: "firewalld", opened_ports: [8080], listening_ports: [],
        checks: [{ peer, path: "private", addr: "192.168.77.5", port: 5432, ok: false, error: "timeout" }] },
    });
    expect(rep.ok(), await rep.text()).toBeTruthy();

    // the open dialog flips to the findings by itself
    const findings = page.getByTestId("findings");
    await expect(findings).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId("findings-status")).toContainText("Problems found");
    await expect(findings).toContainText("not on the same private network");
    await expect(findings).toContainText("192.168.77.5");
    await expect(findings).toContainText("Security List");
    await expect(findings).toContainText("What to do");
    await page.getByRole("button", { name: "Close", exact: true }).click();

    const row = page.getByRole("row", { name: new RegExp(name) });
    await expect(row).toContainText("Problems");
    await expect(row).toContainText("10.0.0.5");

    // the token was single use
    const again = await request.post(`/api/v1/cluster/join/${token}/report`, { data: { hostname: name, private_ip: "10.0.0.5", checks: [] } });
    expect(again.status()).toBe(404);

    // remove the peer: the same server now re-checks clean
    expect((await request.delete(`/api/v1/cluster/servers/${peer}`, { headers: authHeaders(admin) })).status()).toBe(204);
    await page.getByTestId(`server-recheck-${name}`).click();
    const cmd2 = (await page.getByTestId("join-command").innerText()).trim();
    const token2 = /join\/([A-Za-z0-9_-]{40,})\/script/.exec(cmd2)![1];
    const ok = await request.post(`/api/v1/cluster/join/${token2}/report`, { data: { hostname: name, private_ip: "10.0.0.5", private_cidr: "10.0.0.5/24", provider: "oracle", firewall: "none", listening_ports: [8080], checks: [] } });
    expect(ok.ok(), await ok.text()).toBeTruthy();
    await expect(page.getByTestId("findings")).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId("findings-status")).not.toContainText("Problems found");
    await page.getByRole("button", { name: "Close", exact: true }).click();

    // clean up
    await page.getByTestId(`server-delete-${name}`).click();
    await page.getByRole("button", { name: "Remove server" }).click();
    await expect(page.getByRole("row", { name: new RegExp(name) })).toHaveCount(0);
  });

  test("run-projects join command: the checkbox makes a node join (agent install in the plan), and Re-check keeps the intent", async ({ page, request }) => {
    const admin = await adminTokens(request);
    const name = `node-${uid()}`.toLowerCase().slice(0, 30);
    await signInWith(page, admin);
    await page.goto("/cluster");
    await page.getByTestId("server-add").click();
    await page.getByTestId("server-name").fill(name);
    await page.getByTestId("server-run-projects").check();
    await page.getByTestId("server-generate").click();
    const command = (await page.getByTestId("join-command").innerText()).trim();
    const token = /join\/([A-Za-z0-9_-]{40,})\/script/.exec(command)![1];

    const plan = await (await request.get(`/api/v1/cluster/join/${token}/plan`)).text();
    expect(plan).toContain("node=1");
    expect(plan).toContain("agent_port=2377");
    expect(plan).toMatch(/agent_token=[A-Za-z0-9_-]{32,}/);
    expect(plan).toContain(`/join/${token}/agent`);
    // the script itself contains the agent installation, listening on the private address only
    const script = await (await request.get(`/api/v1/cluster/join/${token}/script`)).text();
    expect(script).toContain("jp-node-agent");
    expect(script).toContain("NODE_LISTEN=%s:%s");

    // a plain join (checkbox off) never receives an agent token
    await page.getByRole("button", { name: /^Close \(the command/ }).click();
    await page.getByTestId("server-add").click();
    const plain = `plain-${uid()}`.toLowerCase().slice(0, 30);
    await page.getByTestId("server-name").fill(plain);
    await page.getByTestId("server-generate").click();
    const plainCmd = (await page.getByTestId("join-command").innerText()).trim();
    const plainTok = /join\/([A-Za-z0-9_-]{40,})\/script/.exec(plainCmd)![1];
    expect(await (await request.get(`/api/v1/cluster/join/${plainTok}/plan`)).text()).not.toContain("agent_token");
    expect((await request.get(`/api/v1/cluster/join/${plainTok}/agent?arch=amd64`)).status()).toBe(404);
    await page.getByRole("button", { name: /^Close \(the command/ }).click();
  });

  test("project settings: admins see a Runs on selector; members do not; the choice persists", async ({ page, request }) => {
    const admin = await adminTokens(request);
    const { orgId, projectId } = await createOrgAndProject(request, admin);
    await signInWith(page, admin);
    await selectOrg(page, orgId);
    await page.goto(`/project?id=${projectId}&tab=settings`);
    const select = page.getByTestId("placement-select");
    await expect(select).toBeVisible();
    await expect(select).toHaveValue("");
    const nodes = await select.locator("option").evaluateAll((os) => os.map((o) => (o as HTMLOptionElement).value).filter(Boolean));
    if (nodes.length === 0) {
      await expect(page.getByText("No other server runs projects yet")).toBeVisible();
    } else {
      await select.selectOption(nodes[0]);
      await page.getByTestId("placement-save").click();
      await expect(page.getByText(/New deployments run on/)).toBeVisible();
      await page.reload();
      await expect(page.getByTestId("placement-select")).toHaveValue(nodes[0]);
      const stored = await (await request.get(`/api/v1/cluster/placements/${projectId}`, { headers: authHeaders(admin) })).json();
      expect(stored.node).toBe(nodes[0]);
      // put it back so other runs start clean
      await page.getByTestId("placement-select").selectOption("");
      await page.getByTestId("placement-save").click();
      await expect(page.getByText(/control plane/).first()).toBeVisible();
    }

    // a project member never sees the selector (and the API refuses them)
    const created = await createManagedUser(request, admin, orgId, { username: `pl.${uid()}`.toLowerCase().slice(0, 30), projectIds: [projectId], preset: "developer" });
    const tokens = await loginTokens(request, created.username, created.password);
    const denied = await request.get(`/api/v1/cluster/placements/${projectId}`, { headers: authHeaders(tokens) });
    expect(denied.status()).toBe(403);
  });

  test("the server API refuses bad input and non-administrators", async ({ request }) => {
    const admin = await adminTokens(request);
    const expired = await request.get(`/api/v1/cluster/join/${"A".repeat(43)}/script`);
    expect(expired.status()).toBe(404);
    const bad = await request.put("/api/v1/cluster/servers/x1", { headers: authHeaders(admin), data: { name: "x1", private_addr: "10.0.0.1; reboot" } });
    expect(bad.status()).toBe(400);
    const orgSlug = uid();
    const o = await request.post("/api/v1/orgs", { headers: authHeaders(admin), data: { name: `Srv ${orgSlug}`, slug: `srv-${orgSlug}` } });
    const ob = (await o.json()) as { org?: { id: string }; id?: string };
    const created = await createManagedUser(request, admin, (ob.org?.id ?? ob.id)!, { username: `srv.${orgSlug}`.toLowerCase().slice(0, 30) });
    const user = await loginTokens(request, created.username, created.password);
    const denied = await request.get("/api/v1/cluster/servers", { headers: authHeaders(user) });
    expect(denied.status()).toBe(403);
    const deniedWrite = await request.put("/api/v1/cluster/servers/x2", { headers: authHeaders(user), data: { name: "x2", private_addr: "10.0.0.1" } });
    expect(deniedWrite.status()).toBe(403);
  });
});
