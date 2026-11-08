import type { Role } from "@/lib/api";

const RANK: Record<Role, number> = { viewer: 1, member: 2, admin: 3, owner: 4 };

export function atLeast(role: Role | undefined | null, min: Role): boolean {
  if (!role) return false;
  return RANK[role] >= RANK[min];
}

export const ROLES: Role[] = ["viewer", "member", "admin", "owner"];

export const ROLE_BLURB: Record<Role, string> = {
  viewer: "Read-only access",
  member: "Deploy, edit and upload",
  admin: "Secrets, domains, add-ons, invites",
  owner: "Everything, including roles and org delete",
};
