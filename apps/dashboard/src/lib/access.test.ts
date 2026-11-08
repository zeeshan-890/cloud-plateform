import { describe, expect, it } from "vitest";
import type { User } from "@/lib/api/types";
import { AREAS, grantAreas, levelOf, orgAccess, presetAreas, revealsSecrets, summarizeGrant, visibleAreas } from "./access";

const restricted: User = {
  id: "u",
  name: "R",
  memberships: [{ org_id: "o1", role: "member", full: false, projects: [{ project_id: "p1", areas: { overview: "view", logs: "view", deployments: "manage" }, secrets_reveal: false }] }],
};
const admin: User = { id: "a", name: "A", memberships: [{ org_id: "o1", role: "admin", full: true }] };

describe("access model", () => {
  it("restricted users only get granted areas on granted projects", () => {
    const acc = orgAccess(restricted, "o1");
    expect(acc.full).toBe(false);
    expect(visibleAreas(acc, "p1")).toEqual(["overview", "deployments", "logs"]);
    expect(levelOf(acc, "p1", "secrets")).toBe("none");
    expect(levelOf(acc, "p1", "deployments")).toBe("manage");
    expect(visibleAreas(acc, "p2")).toEqual([]);
    expect(revealsSecrets(acc, "p1")).toBe(false);
  });

  it("owners and admins see every area and may reveal", () => {
    const acc = orgAccess(admin, "o1");
    expect(visibleAreas(acc, "anything")).toEqual(AREAS);
    expect(revealsSecrets(acc, "anything")).toBe(true);
  });

  it("fails closed (no privileged access) when the API sends no memberships", () => {
    expect(orgAccess({ id: "x", name: "x" }, "o1")).toMatchObject({ known: false, full: false });
  });

  it("presets follow the contract", () => {
    const v = presetAreas("viewer");
    expect(v.domains).toBe("view");
    expect(v.secrets).toBe("none");
    expect(v.settings).toBe("none");
    const d = presetAreas("developer");
    expect(d.deployments).toBe("manage");
    expect(d.secrets).toBe("view");
    expect(d.domains).toBe("view");
    const o = presetAreas("operator");
    expect(o.domains).toBe("manage");
    expect(o.secrets).toBe("manage");
    expect(o.settings).toBe("none");
    const p = presetAreas("project_admin");
    expect(AREAS.every((a) => p[a] === "manage")).toBe(true);
  });

  it("custom grants expand to a full 13-area map and summarize", () => {
    const g = { project_id: "p", areas: { logs: "view", storage: "manage" } as const };
    const a = grantAreas({ ...g, areas: { ...g.areas }, preset: "custom" });
    expect(Object.keys(a)).toHaveLength(13);
    expect(a.logs).toBe("view");
    expect(a.runtime).toBe("none");
    expect(summarizeGrant({ project_id: "p", preset: "custom", areas: { logs: "view", storage: "manage" } })).toBe("Custom (1 manage, 1 view)");
    expect(summarizeGrant({ project_id: "p", preset: "operator" })).toBe("Operator");
  });
});
