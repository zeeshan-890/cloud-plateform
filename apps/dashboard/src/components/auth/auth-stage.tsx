"use client";

import { Suspense } from "react";
import { Wordmark } from "@/components/brand";
import { Panel } from "@/components/ui/panel";
import { ThemeToggle } from "@/components/shell/app-shell";
import { Splash } from "@/components/shell/require-auth";

export function AuthStage({ title, subtitle, children, footer }: { title: string; subtitle?: string; children: React.ReactNode; footer?: React.ReactNode }) {
  return (
    <div className="stage">
      <div className="absolute right-4 top-4">
        <ThemeToggle />
      </div>
      <div className="page-enter w-full max-w-[460px]">
        <div className="mb-6 flex justify-center">
          <Wordmark />
        </div>
        <Panel variant="metal" className="!px-8 !py-8">
          <h1 className="engrave mb-1 text-[28px] uppercase tracking-[0.06em] !text-[var(--ink)]">{title}</h1>
          {subtitle && <p className="muted mb-6 mt-1 text-[15px]">{subtitle}</p>}
          <Suspense fallback={<Splash label="Loading" />}>{children}</Suspense>
        </Panel>
        {footer && <div className="muted mt-5 text-center text-[14px]">{footer}</div>}
      </div>
    </div>
  );
}
