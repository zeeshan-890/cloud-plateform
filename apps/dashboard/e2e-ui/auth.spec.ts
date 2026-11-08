import { expect, test } from "@playwright/test";
import { ADMIN, uid } from "./helpers";

test.describe("bootstrap and login", () => {
  test("first run goes to /setup; afterwards /login signs the admin in", async ({ page, request }) => {
    const st = await request.get("/api/v1/auth/bootstrap/status");
    const { initialized } = (await st.json()) as { initialized: boolean };

    if (!initialized) {
      await page.goto("/login");
      await expect(page).toHaveURL(/\/setup/);
      await page.getByTestId("setup-name").fill(ADMIN.name);
      await page.getByTestId("setup-email").fill(ADMIN.email);
      await page.getByTestId("setup-password").fill(ADMIN.password);
      await page.getByTestId("setup-submit").click();
      await expect(page).toHaveURL(/\/orgs\/new/);
      return;
    }

    await page.goto("/setup");
    await expect(page).toHaveURL(/\/login/);
    await page.getByTestId("login-email").fill(ADMIN.email);
    await page.getByTestId("login-password").fill(ADMIN.password);
    await page.getByTestId("login-submit").click();
    await expect(page.getByRole("navigation", { name: "Organization" })).toBeVisible();
  });

  test("a wrong password shows the server error and stays on the form", async ({ page }) => {
    await page.goto("/login");
    await page.getByTestId("login-email").fill(`nobody-${uid()}@e2e.test`);
    await page.getByTestId("login-password").fill("definitely-wrong");
    await page.getByTestId("login-submit").click();
    await expect(page.locator("[role=alert]:not(#__next-route-announcer__)")).toBeVisible();
    await expect(page).toHaveURL(/\/login/);
  });

  test("registration without an invitation is not offered when registration is invite-only", async ({ page, request }) => {
    const { registration } = (await (await request.get("/api/v1/auth/bootstrap/status")).json()) as { registration?: string };
    await page.goto("/login");
    if (registration === "invite") {
      await expect(page.getByRole("link", { name: "Create one" })).toHaveCount(0);
      await expect(page.getByText(/invitation/i)).toBeVisible();
    } else if (registration === "closed") {
      await expect(page.getByText(/closed/i)).toBeVisible();
    }
  });

  test("protected pages redirect anonymous visitors to /login with a return path", async ({ page }) => {
    await page.goto("/projects");
    await expect(page).toHaveURL(/\/login\/?\?next=%2Fprojects/);
  });
});
