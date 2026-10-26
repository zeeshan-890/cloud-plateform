import { AppShell } from "@/components/app-shell";
import { RequireAuth } from "@/components/require-auth";
import { ProjectProvider } from "@/lib/project-context";

export default function ConsoleLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <RequireAuth>
      <ProjectProvider>
        <AppShell>{children}</AppShell>
      </ProjectProvider>
    </RequireAuth>
  );
}
