import { expect, test } from "@playwright/test";
import { mockApi, P1 } from "./mock-api";

// Mocked-network check of the restricted-user experience (the real one is e2e-ui/access.spec.ts).
const SHOTS = process.env.SHOT_DIR || "shots";

test.describe("restricted user (mocked)", () => {
  test.beforeEach(async ({ page }) => {
    await mockApi(page, { restricted: true });
  });

  test("sidebar shows only granted areas; forbidden area is a friendly state", async ({ page }, info) => {
    await page.goto("/");
    const org = page.getByRole("navigation", { name: "Organization" });
    await expect(org.getByRole("link", { name: "Team & access" })).toHaveCount(0);
    await expect(org.getByRole("link", { name: "Git connections" })).toHaveCount(0);
    await page.goto("/projects");
    await expect(page.getByRole("heading", { name: /storefront-api/i })).toBeVisible();
    await expect(page.getByText(/marketing-site/i)).toHaveCount(0);

    await page.goto(`/project?id=${P1}&tab=deployments`);
    const proj = page.getByRole("navigation", { name: "Project" });
    await expect(proj.getByRole("link", { name: "Deployments" })).toBeVisible();
    await expect(proj.getByRole("link", { name: "Storage" })).toHaveCount(0);
    await expect(proj.getByRole("link", { name: /Settings/ })).toHaveCount(0);
    await expect(page.getByTestId("view-only")).toBeVisible();
    await expect(page.getByTestId("deploy-open")).toHaveCount(0);
    await page.screenshot({ path: `${SHOTS}/restricted-${info.project.name}-deployments.png` });

    await page.goto(`/project?id=${P1}&tab=storage`);
    await expect(page.getByText("No access to this section").first()).toBeVisible();

    await page.goto(`/project?id=${P1}&tab=domains`);
    await expect(page.getByTestId("domain-add")).toBeVisible(); // domains = manage
    await page.goto(`/project?id=${P1}&tab=secrets`);
    await expect(page.getByRole("button", { name: /Reveal/ })).toHaveCount(0); // no secrets_reveal
  });
});

test("team & access page (mocked)", async ({ page }, info) => {
  await mockApi(page);
  await page.goto("/team");
  await expect(page.getByRole("row", { name: /grace/ })).toBeVisible();
  await page.getByTestId("user-create").click();
  await page.getByTestId("grant-add-select").selectOption(P1);
  await page.getByTestId("grant-add").click();
  await page.getByTestId("preset-select").selectOption("custom");
  await expect(page.getByRole("radiogroup")).toHaveCount(13);
  await page.screenshot({ path: `${SHOTS}/team-create-${info.project.name}.png` });
});
