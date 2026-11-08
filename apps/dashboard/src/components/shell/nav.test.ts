import { describe, expect, it } from "vitest";
import { ORG_NAV, PROJECT_NAV_GROUPS, PROJECT_NAV_ITEMS, PROJECT_TABS, flatOrgNav, inProjectWorkspace, isActive, orgNavFor, orgPageTitle, projectHref, projectTabFrom } from "./nav";

describe("two-level navigation", () => {
  it("keeps every sidebar group small", () => {
    for (const g of ORG_NAV) expect(g.items.length).toBeLessThanOrEqual(8);
    for (const g of PROJECT_NAV_GROUPS) expect(g.items.length).toBeLessThanOrEqual(8);
  });

  it("level 1 holds only organization-level pages, never project pages", () => {
    const hrefs = flatOrgNav(true).map((i) => i.href);
    for (const h of ["/", "/projects", "/team", "/git", "/keys", "/sessions", "/settings", "/cluster", "/security", "/platform"]) expect(hrefs).toContain(h);
    for (const h of ["/domains", "/secrets", "/addons", "/deployments", "/runtime", "/logs", "/storage"]) expect(hrefs).not.toContain(h);
  });

  it("hides the Platform group from non-admins", () => {
    expect(orgNavFor(false).map((g) => g.label)).not.toContain("Platform");
    expect(orgNavFor(true).map((g) => g.label)).toContain("Platform");
  });

  it("level 2 lists every project page exactly once (settings is pinned separately)", () => {
    const grouped = PROJECT_NAV_GROUPS.flatMap((g) => g.items);
    expect(new Set(grouped).size).toBe(grouped.length);
    expect([...grouped, "settings"].sort()).toEqual([...PROJECT_TABS].sort());
    for (const t of PROJECT_TABS) expect(PROJECT_NAV_ITEMS[t].description.length).toBeGreaterThan(5);
    expect(PROJECT_NAV_ITEMS.secrets.label).toBe("Environment & secrets");
    expect(PROJECT_NAV_ITEMS.repository.label).toBe("Repository");
  });

  it("builds stable deep links and falls back to overview for unknown tabs", () => {
    expect(projectHref("abc", "domains")).toBe("/project?id=abc&tab=domains");
    expect(projectTabFrom("addons")).toBe("addons");
    expect(projectTabFrom("nope")).toBe("overview");
    expect(projectTabFrom(null)).toBe("overview");
  });

  it("tells the two levels apart by path", () => {
    expect(inProjectWorkspace("/project")).toBe(true);
    expect(inProjectWorkspace("/project/")).toBe(true);
    expect(inProjectWorkspace("/projects")).toBe(false);
    expect(inProjectWorkspace("/projects/new")).toBe(false);
  });

  it("marks the right sidebar item active and names breadcrumb pages", () => {
    expect(isActive("/projects/new", "/projects")).toBe(true);
    expect(isActive("/projects", "/")).toBe(false);
    expect(orgPageTitle("/")).toBe("Overview");
    expect(orgPageTitle("/projects/new")).toBe("New project");
    expect(orgPageTitle("/settings/")).toBe("Account settings");
    expect(orgPageTitle("/nowhere")).toBeNull();
  });
});
