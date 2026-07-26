"use client";

import { AuthProvider } from "@/lib/auth-context";
import { OrgProvider } from "@/lib/org-context";

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <AuthProvider>
      <OrgProvider>{children}</OrgProvider>
    </AuthProvider>
  );
}
