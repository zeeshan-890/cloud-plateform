"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { api } from "@/lib/api";
import { useAuth } from "@/lib/auth-context";
import {
  getCurrentOrgId,
  setCurrentOrgId,
  clearCurrentOrgId,
} from "@/lib/storage";
import type { Org } from "@/lib/types";

interface OrgContextValue {
  orgs: Org[];
  currentOrg: Org | null;
  loading: boolean;
  setOrg: (orgId: string) => void;
  refreshOrgs: () => Promise<void>;
  createOrg: (name: string) => Promise<Org>;
}

const OrgContext = createContext<OrgContextValue | null>(null);

export function OrgProvider({ children }: { children: ReactNode }) {
  const { user } = useAuth();
  const [orgs, setOrgs] = useState<Org[]>([]);
  const [currentOrg, setCurrentOrg] = useState<Org | null>(null);
  const [loading, setLoading] = useState(true);

  const refreshOrgs = useCallback(async () => {
    if (!user) {
      setOrgs([]);
      setCurrentOrg(null);
      return;
    }
    const list = await api.listOrgs();
    setOrgs(list);
    const saved = getCurrentOrgId();
    const match = list.find((o) => o.id === saved) || list[0] || null;
    if (match) {
      setCurrentOrgId(match.id);
      setCurrentOrg(match);
    } else {
      clearCurrentOrgId();
      setCurrentOrg(null);
    }
  }, [user]);

  useEffect(() => {
    let mounted = true;
    (async () => {
      setLoading(true);
      try {
        await refreshOrgs();
      } catch {
        if (mounted) {
          setOrgs([]);
          setCurrentOrg(null);
        }
      } finally {
        if (mounted) setLoading(false);
      }
    })();
    return () => {
      mounted = false;
    };
  }, [refreshOrgs]);

  const setOrg = useCallback(
    (orgId: string) => {
      const match = orgs.find((o) => o.id === orgId) || null;
      if (match) {
        setCurrentOrgId(match.id);
        setCurrentOrg(match);
      }
    },
    [orgs],
  );

  const createOrg = useCallback(async (name: string) => {
    const org = await api.createOrg({ name });
    setOrgs((prev) => [...prev, org]);
    setCurrentOrgId(org.id);
    setCurrentOrg(org);
    return org;
  }, []);

  const value = useMemo(
    () => ({
      orgs,
      currentOrg,
      loading,
      setOrg,
      refreshOrgs,
      createOrg,
    }),
    [orgs, currentOrg, loading, setOrg, refreshOrgs, createOrg],
  );

  return <OrgContext.Provider value={value}>{children}</OrgContext.Provider>;
}

export function useOrg() {
  const ctx = useContext(OrgContext);
  if (!ctx) throw new Error("useOrg must be used within OrgProvider");
  return ctx;
}
