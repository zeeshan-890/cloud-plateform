"use client";

import { GitInstallations } from "@/features/git";
import { PageHeader } from "@/components/ui/panel";

export default function Page() {
  return (
    <>
      <PageHeader title="Git connections" description="Connect GitHub once for the whole organization. Each project then picks its own repository." />
      <GitInstallations />
    </>
  );
}
