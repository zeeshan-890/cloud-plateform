"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useSyncExternalStore } from "react";
import { auth, isMfaChallenge, localTokenStore, onSessionExpired, type AuthResponse, type User } from "@/lib/api";

/* Small external store so auth state never needs setState-in-effect. */
type Status = "unknown" | "anon" | "authed";
interface Snapshot {
  status: Status;
  user: User | null;
}

let snap: Snapshot = { status: "unknown", user: null };
const SERVER: Snapshot = { status: "unknown", user: null };
const subs = new Set<() => void>();
let started = false;

function set(next: Snapshot) {
  snap = next;
  subs.forEach((f) => f());
}

function start() {
  if (started) return;
  started = true;
  if (!localTokenStore.getAccess() && !localTokenStore.getRefresh()) {
    set({ status: "anon", user: null });
    return;
  }
  auth
    .me()
    .then((user) => set({ status: "authed", user }))
    .catch(() => {
      // 401 already cleared tokens inside the client; network failures keep tokens but show login
      set({ status: "anon", user: null });
    });
}

/** test helper */
export function __resetAuthStore() {
  started = false;
  snap = { status: "unknown", user: null };
}

export type LoginOutcome = { kind: "ok"; user: User } | { kind: "mfa"; mfaToken: string } | { kind: "pwchange"; user: User };

interface AuthContextValue {
  user: User | null;
  loading: boolean;
  login: (login: string, password: string) => Promise<LoginOutcome>;
  completeMfa: (login: string, password: string, mfaToken: string, code: string) => Promise<User>;
  register: (input: { email: string; password: string; name: string; invite_token?: string }) => Promise<User>;
  bootstrap: (input: { email: string; password: string; name: string }) => Promise<User>;
  logout: () => Promise<void>;
  refreshUser: () => Promise<void>;
}

const Ctx = createContext<AuthContextValue | null>(null);

function accept(res: AuthResponse): User {
  localTokenStore.set(res.access_token, res.refresh_token);
  const user: User = res.must_change_password ? { ...res.user, must_change_password: true } : res.user;
  set({ status: "authed", user });
  return user;
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const s = useSyncExternalStore(
    (cb) => {
      subs.add(cb);
      return () => subs.delete(cb);
    },
    () => snap,
    () => SERVER,
  );

  useEffect(() => {
    start();
    return onSessionExpired(() => {
      localTokenStore.clear();
      set({ status: "anon", user: null });
    });
  }, []);

  const login = useCallback(async (loginName: string, password: string): Promise<LoginOutcome> => {
    const res = await auth.login({ login: loginName, password });
    if (isMfaChallenge(res)) return { kind: "mfa", mfaToken: res.mfa_token };
    const user = accept(res);
    return user.must_change_password ? { kind: "pwchange", user } : { kind: "ok", user };
  }, []);

  const completeMfa = useCallback(async (loginName: string, password: string, mfaToken: string, code: string) => {
    const res = await auth.login({ login: loginName, password, mfa_token: mfaToken, totp_code: code.trim() });
    if (isMfaChallenge(res)) throw new Error("Verification failed. Try again.");
    return accept(res);
  }, []);

  const register = useCallback(async (input: { email: string; password: string; name: string; invite_token?: string }) => {
    return accept(await auth.register(input));
  }, []);

  const bootstrap = useCallback(async (input: { email: string; password: string; name: string }) => {
    return accept(await auth.bootstrap(input));
  }, []);

  const logout = useCallback(async () => {
    try {
      await auth.logout();
    } catch {
      /* token may already be dead; local sign-out still happens */
    }
    localTokenStore.clear();
    set({ status: "anon", user: null });
  }, []);

  const refreshUser = useCallback(async () => {
    const user = await auth.me();
    set({ status: "authed", user });
  }, []);

  const value = useMemo<AuthContextValue>(
    () => ({
      user: s.user,
      loading: s.status === "unknown",
      login,
      completeMfa,
      register,
      bootstrap,
      logout,
      refreshUser,
    }),
    [s, login, completeMfa, register, bootstrap, logout, refreshUser],
  );

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useAuth(): AuthContextValue {
  const v = useContext(Ctx);
  if (!v) throw new Error("useAuth must be used inside AuthProvider");
  return v;
}
