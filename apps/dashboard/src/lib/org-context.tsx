"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useRef } from "react";
import { orgs, type Org, type Role } from "@/lib/api";
import { useAuth } from "@/lib/auth-context";
import { useStoredValue } from "@/lib/persist";
import { useResource } from "@/lib/use-resource";
import type { ApiError } from "@/lib/api/http";
import { atLeast } from "@/lib/rbac";
import { orgAccess, type OrgAccess } from "@/lib/access";

interface OrgContextValue {
  orgs: Org[];
  currentOrg: Org | null;
  /** caller's role in the current org (undefined while unknown) */
  role: Role | undefined;
  /** platform administrator (first bootstrapped user) */
  isPlatformAdmin: boolean;
  can: (min: Role) => boolean;
  /** what this user may see: built from /auth/me memberships */
  access: OrgAccess;
  /** member/viewer with explicit per-project grants */
  restricted: boolean;
  loading: boolean;
  error: ApiError | undefined;
  setCurrentOrgId: (id: string) => void;
  reload: () => void;
}

const Ctx = createContext<OrgContextValue | null>(null);

export function OrgProvider({ children }: { children: React.ReactNode }) {
  const { user, refreshUser } = useAuth();
  const [storedId, setStoredId] = useStoredValue("jp_current_org");
  const list = useResource(user ? `orgs:${user.id}` : null, (signal) => orgs.list({ signal }));

  const orgList = list.data;
  const currentOrg = useMemo(() => {
    if (!orgList || orgList.length === 0) return null;
    return orgList.find((o) => o.id === storedId) ?? orgList[0];
  }, [orgList, storedId]);

  // Role may be missing from the list payload; fall back to the members endpoint.
  const needMembers = !!currentOrg && !currentOrg.role && !!user;
  const members = useResource(needMembers && currentOrg ? `members:${currentOrg.id}` : null, (signal) =>
    orgs.members(currentOrg!.id, { signal }),
  );
  const role: Role | undefined =
    currentOrg?.role ?? members.data?.find((m) => m.user_id === user?.id)?.role;

  const access = useMemo(() => orgAccess(user, currentOrg?.id), [user, currentOrg?.id]);

  // The cached user can predate an org/project created or granted after sign-in. Access stays closed until the
  // membership data arrives, so refresh it (once per org id and per project count change) when it is missing.
  const refreshedFor = useRef<string | null>(null);
  const orgId = currentOrg?.id;
  const hasMembership = !!user?.memberships?.some((m) => m.org_id === orgId);
  useEffect(() => {
    if (!user || !orgId || hasMembership || refreshedFor.current === orgId) return;
    refreshedFor.current = orgId;
    void refreshUser().catch(() => undefined);
  }, [user, orgId, hasMembership, refreshUser]);
  const can = useCallback((min: Role) => atLeast(role, min), [role]);

  const value = useMemo<OrgContextValue>(
    () => ({
      orgs: orgList ?? [],
      currentOrg,
      role,
      isPlatformAdmin: !!user?.is_admin,
      can,
      access,
      restricted: access.known && !access.full,
      loading: list.loading,
      error: list.error,
      setCurrentOrgId: setStoredId,
      reload: list.reload,
    }),
    [orgList, currentOrg, role, user?.is_admin, can, access, list.loading, list.error, list.reload, setStoredId],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useOrg(): OrgContextValue {
  const v = useContext(Ctx);
  if (!v) throw new Error("useOrg must be used inside OrgProvider");
  return v;
}
