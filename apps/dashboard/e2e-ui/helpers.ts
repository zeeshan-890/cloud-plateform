import { createHmac } from "node:crypto";
import { expect, type APIRequestContext, type Page } from "@playwright/test";

export const ADMIN = {
  email: process.env.E2E_ADMIN_EMAIL || "admin@e2e.test",
  password: process.env.E2E_ADMIN_PASSWORD || "E2e-Admin-Passw0rd!",
  name: "E2E Admin",
};

export const uid = () => `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;

interface Auth {
  access_token: string;
  refresh_token: string;
  user: { id: string; email: string };
}

/** Make sure the installation has an administrator and return tokens for it. */
export async function adminTokens(request: APIRequestContext): Promise<Auth> {
  const st = await request.get("/api/v1/auth/bootstrap/status");
  expect(st.ok(), "gateway must be reachable").toBeTruthy();
  const { initialized } = (await st.json()) as { initialized: boolean };
  if (!initialized) {
    const r = await request.post("/api/v1/auth/bootstrap", { data: ADMIN });
    expect(r.ok(), `bootstrap failed: ${await r.text()}`).toBeTruthy();
    return (await r.json()) as Auth;
  }
  const r = await request.post("/api/v1/auth/login", { data: { email: ADMIN.email, password: ADMIN.password } });
  expect(r.ok(), `admin login failed (set E2E_ADMIN_EMAIL / E2E_ADMIN_PASSWORD): ${await r.text()}`).toBeTruthy();
  const body = (await r.json()) as Auth & { mfa_required?: boolean };
  expect(body.mfa_required, "e2e admin must not have MFA enabled").toBeFalsy();
  return body;
}

export async function signInWith(page: Page, tokens: { access_token: string; refresh_token: string }) {
  await page.addInitScript((t) => {
    localStorage.setItem("jp_access_token", t.access_token);
    localStorage.setItem("jp_refresh_token", t.refresh_token);
  }, tokens);
}

export async function signInAsAdmin(page: Page, request: APIRequestContext) {
  const t = await adminTokens(request);
  await signInWith(page, t);
  return t;
}

export function authHeaders(t: { access_token: string }) {
  return { Authorization: `Bearer ${t.access_token}` };
}

export async function createOrgAndProject(request: APIRequestContext, t: { access_token: string }, tag = uid()) {
  const o = await request.post("/api/v1/orgs", { headers: authHeaders(t), data: { name: `Org ${tag}`, slug: `org-${tag}` } });
  expect(o.ok(), await o.text()).toBeTruthy();
  const ob = (await o.json()) as { org?: { id: string }; id?: string };
  const orgId = ob.org?.id ?? ob.id!;
  const p = await request.post(`/api/v1/orgs/${orgId}/projects`, { headers: authHeaders(t), data: { name: `Proj ${tag}`, slug: `proj-${tag}` } });
  expect(p.ok(), await p.text()).toBeTruthy();
  const pb = (await p.json()) as { project?: { id: string }; id?: string };
  return { orgId, projectId: pb.project?.id ?? pb.id!, tag };
}

/** RFC 6238 TOTP (SHA1, 6 digits, 30s) so tests can complete real MFA enrollment. */
export function totp(secretBase32: string, atMs = Date.now()): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const ch of secretBase32.replace(/=+$/, "").toUpperCase()) bits += alphabet.indexOf(ch).toString(2).padStart(5, "0");
  const bytes = Buffer.from(bits.match(/.{8}/g)!.map((b) => parseInt(b, 2)));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(atMs / 30000)));
  const h = createHmac("sha1", bytes).update(counter).digest();
  const off = h[h.length - 1] & 0xf;
  const code = ((h[off] & 0x7f) << 24) | (h[off + 1] << 16) | (h[off + 2] << 8) | h[off + 3];
  return String(code % 1_000_000).padStart(6, "0");
}

/** Select the org created by the test in the shell (the UI remembers the current org in localStorage). */
export async function selectOrg(page: Page, orgId: string) {
  await page.addInitScript((id) => localStorage.setItem("jp_current_org", id), orgId);
}

/** Create a restricted user through the admin API (registration is closed on real stacks). */
export async function createManagedUser(
  request: APIRequestContext,
  admin: { access_token: string },
  orgId: string,
  opts: { username: string; name?: string; projectIds?: string[]; preset?: string; mustChange?: boolean },
) {
  const r = await request.post(`/api/v1/orgs/${orgId}/users`, {
    headers: authHeaders(admin),
    data: {
      name: opts.name ?? "E2E User",
      username: opts.username,
      role: "member",
      must_change_password: opts.mustChange ?? false,
      grants: (opts.projectIds ?? []).map((project_id) => ({ project_id, preset: opts.preset ?? "developer" })),
    },
  });
  expect(r.ok(), `create user failed: ${await r.text()}`).toBeTruthy();
  const b = (await r.json()) as { user: { id?: string; user_id?: string }; generated_password?: string; password?: string };
  const password = b.generated_password ?? b.password ?? "";
  expect(password, "backend must return the generated password once").not.toBe("");
  return { id: (b.user.id ?? b.user.user_id)!, username: opts.username, password };
}

export async function loginTokens(request: APIRequestContext, login: string, password: string) {
  const r = await request.post("/api/v1/auth/login", { data: { login, password } });
  expect(r.ok(), `login failed: ${await r.text()}`).toBeTruthy();
  return (await r.json()) as { access_token: string; refresh_token: string };
}

/** Wait until the console shell is hydrated (keyboard shortcuts and clicks are live). */
export async function shellReady(page: Page) {
  await expect(page.getByRole("navigation", { name: "Organization" }).or(page.getByRole("navigation", { name: "Project" }))).toBeVisible();
}
