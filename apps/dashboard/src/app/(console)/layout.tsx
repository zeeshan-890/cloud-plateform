import { Suspense } from "react";
import { AppShell } from "@/components/shell/app-shell";
import { RequireAuth, Splash } from "@/components/shell/require-auth";
import { ProjectProvider } from "@/lib/project-context";

export default function ConsoleLayout({ children }: { children: React.ReactNode }) {
  return (
    <RequireAuth>
      <ProjectProvider>
        <Suspense fallback={<Splash label="Loading" />}>
          <AppShell>{children}</AppShell>
        </Suspense>
      </ProjectProvider>
    </RequireAuth>
  );
}
