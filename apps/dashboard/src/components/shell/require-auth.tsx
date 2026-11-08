"use client";

import { useEffect } from "react";
import { usePathname, useRouter } from "next/navigation";
import { BrandMark } from "@/components/brand";
import { useAuth } from "@/lib/auth-context";
import { useOrg } from "@/lib/org-context";

export function Splash({ label = "Powering up" }: { label?: string }) {
  return (
    <div className="stage" role="status" aria-live="polite">
      <div className="flex flex-col items-center gap-4">
        <BrandMark size={64} />
        <div className="flex items-center gap-3">
          <span className="lamp" data-state="warn" data-pulse="true" />
          <span className="label">{label}</span>
        </div>
      </div>
    </div>
  );
}

const ORG_FREE = ["/orgs/new", "/invite/accept", "/settings", "/sessions", "/keys"];

export function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth();
  const { orgs, loading: orgsLoading, error } = useOrg();
  const router = useRouter();
  const pathname = usePathname().replace(/\/+$/, "") || "/";

  useEffect(() => {
    if (!loading && !user) {
      const next = pathname === "/" ? "" : `?next=${encodeURIComponent(pathname + window.location.search)}`;
      router.replace(`/login${next}`);
    }
  }, [loading, user, router, pathname]);

  const mustChange = !!user?.must_change_password;
  useEffect(() => {
    if (mustChange) router.replace("/change-password");
  }, [mustChange, router]);

  const needsOrg = !!user && !orgsLoading && !error && orgs.length === 0 && !ORG_FREE.includes(pathname);
  useEffect(() => {
    if (needsOrg) router.replace("/orgs/new");
  }, [needsOrg, router]);

  if (loading || !user || mustChange) return <Splash />;
  if (orgsLoading && orgs.length === 0 && !error) return <Splash label="Loading workspace" />;
  if (needsOrg) return <Splash label="Setting up" />;
  return <>{children}</>;
}
