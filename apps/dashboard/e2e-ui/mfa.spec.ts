import { expect, test } from "@playwright/test";
import { authHeaders, createManagedUser, loginTokens, selectOrg, signInWith, totp, uid } from "./helpers";
import { adminTokens } from "./helpers";

/** Uses a throw-away user so the shared admin never gets MFA. Needs registration or admin-created users: we register via a fresh org invite. */
test("MFA enrollment: QR + secret, verify code, backup codes, two-step login with a backup code", async ({ page, request }) => {
  const admin = await adminTokens(request);
  const tag = uid();
  const org = await request.post("/api/v1/orgs", { headers: authHeaders(admin), data: { name: `MFA ${tag}`, slug: `mfa-${tag}` } });
  expect(org.ok()).toBeTruthy();
  const ob = (await org.json()) as { org?: { id: string }; id?: string };
  const orgId = ob.org?.id ?? ob.id;
  const username = `mfa.${tag}`.toLowerCase().slice(0, 30);
  const made = await createManagedUser(request, admin, orgId!, { username });
  const password = made.password;
  const email = username;
  await signInWith(page, await loginTokens(request, username, password));
  await selectOrg(page, orgId!);

  await page.goto("/settings");
  await page.getByTestId("mfa-begin").click();
  const secret = (await page.getByTestId("mfa-secret").innerText()).trim();
  await expect(page.getByRole("img", { name: "Authenticator QR code" })).toBeVisible();
  expect(secret).toMatch(/^[A-Z2-7]{16,}$/);

  await page.getByTestId("mfa-code").fill("000000");
  await page.getByTestId("mfa-enable").click();
  await expect(page.locator("[role=alert]:not(#__next-route-announcer__)")).toBeVisible(); // wrong code is rejected

  await page.getByTestId("mfa-code").fill(totp(secret));
  await page.getByTestId("mfa-enable").click();
  const codes = page.getByTestId("backup-codes");
  await expect(codes).toBeVisible();
  const backup = (await codes.locator("code").first().innerText()).trim();
  await page.getByRole("button", { name: "I have stored them" }).click();

  // sign out through the UI (revokes the session) and back in: the second step must appear
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login/);
  await page.getByTestId("login-email").fill(email);
  await page.getByTestId("login-password").fill(password);
  await page.getByTestId("login-submit").click();
  await expect(page.getByTestId("login-mfa-code")).toBeVisible();
  await page.getByTestId("login-mfa-code").fill("111111");
  await page.getByTestId("login-submit").click();
  await expect(page.locator("[role=alert]:not(#__next-route-announcer__)")).toBeVisible();
  await page.getByTestId("login-mfa-code").fill(backup);
  await page.getByTestId("login-submit").click();
  await expect(page.getByRole("navigation", { name: "Organization" })).toBeVisible();
});
