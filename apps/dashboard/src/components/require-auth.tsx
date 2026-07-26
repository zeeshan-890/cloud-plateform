"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth-context";

export function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (!loading && !user) {
      router.replace("/login");
    }
  }, [loading, user, router]);

  if (loading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[var(--paper)]">
        <p className="font-[family-name:var(--font-display)] text-2xl text-[var(--ink)] animate-pulse-soft">
          jp
        </p>
      </div>
    );
  }

  if (!user) return null;
  return <>{children}</>;
}
