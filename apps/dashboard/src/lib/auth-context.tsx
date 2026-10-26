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
import {
  clearCurrentOrgId,
  clearTokens,
  getAccessToken,
  setTokens,
} from "@/lib/storage";
import type { User } from "@/lib/types";

interface AuthContextValue {
  user: User | null;
  loading: boolean;
  login: (identifier: string, password: string) => Promise<void>;
  register: (name: string, identifier: string, password: string) => Promise<void>;
  bootstrap: (identifier: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  refreshUser: () => Promise<void>;
}

function toEmail(identifier: string): string {
  const clean = identifier.trim().toLowerCase();
  if (clean.includes("@")) return clean;
  return `${clean}@local`;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);

  const refreshUser = useCallback(async () => {
    const token = getAccessToken();
    if (!token) {
      setUser(null);
      return;
    }
    try {
      const me = await api.me();
      setUser(me);
    } catch {
      clearTokens();
      setUser(null);
    }
  }, []);

  useEffect(() => {
    let mounted = true;
    (async () => {
      try {
        await refreshUser();
      } finally {
        if (mounted) setLoading(false);
      }
    })();
    return () => {
      mounted = false;
    };
  }, [refreshUser]);

  const login = useCallback(async (identifier: string, password: string) => {
    const email = toEmail(identifier);
    const res = await api.login({ email, password });
    setTokens(res.access_token, res.refresh_token);
    setUser(res.user);
  }, []);

  const register = useCallback(
    async (name: string, identifier: string, password: string) => {
      const email = toEmail(identifier);
      const res = await api.register({ name: name || identifier, email, password });
      setTokens(res.access_token, res.refresh_token);
      setUser(res.user);
    },
    [],
  );

  const bootstrap = useCallback(
    async (identifier: string, password: string) => {
      const email = toEmail(identifier);
      const name = identifier.split("@")[0] || "Administrator";
      const res = await api.bootstrap({ name, email, password });
      setTokens(res.access_token, res.refresh_token);
      setUser(res.user);
    },
    [],
  );

  const logout = useCallback(async () => {
    try {
      await api.logout();
    } catch {
      // ignore network errors on logout
    } finally {
      clearTokens();
      clearCurrentOrgId();
      setUser(null);
    }
  }, []);

  const value = useMemo(
    () => ({ user, loading, login, register, bootstrap, logout, refreshUser }),
    [user, loading, login, register, bootstrap, logout, refreshUser],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
