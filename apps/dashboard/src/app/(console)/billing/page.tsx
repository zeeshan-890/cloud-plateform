"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Alert, EmptyState, PageHeader } from "@/components/ui/page";
import { api, ApiError } from "@/lib/api";
import { useOrg } from "@/lib/org-context";
import type { BillingPlan, BillingUsageRow } from "@/lib/types";

export default function BillingPage() {
  const { currentOrg } = useOrg();
  const [plans, setPlans] = useState<BillingPlan[]>([]);
  const [usage, setUsage] = useState<BillingUsageRow[]>([]);
  const [planId, setPlanId] = useState("free");
  const [note, setNote] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    if (!currentOrg) return;
    let mounted = true;
    (async () => {
      try {
        const [p, u] = await Promise.all([
          api.billingPlans(currentOrg.id),
          api.billingUsage(currentOrg.id),
        ]);
        if (!mounted) return;
        setPlans(p);
        setUsage(u.usage || []);
        setPlanId(u.plan_id || "free");
        setNote(u.stub_note || "");
      } catch (err) {
        if (mounted) {
          setError(err instanceof ApiError ? err.message : "Failed to load billing");
        }
      }
    })();
    return () => {
      mounted = false;
    };
  }, [currentOrg]);

  if (!currentOrg) {
    return (
      <EmptyState
        title="Select an organization"
        description="Billing requires an active organization."
      />
    );
  }

  return (
    <div className="animate-fade-up">
      <PageHeader
        title="Billing"
        description="Plans and stub usage meters (build minutes, runtime hours)."
      />
      {error ? (
        <div className="mb-4">
          <Alert>{error}</Alert>
        </div>
      ) : null}

      <p className="mb-6 text-sm text-[var(--ink-muted)]">
        Current plan: <span className="font-medium text-[var(--ink)]">{planId}</span>
        {note ? ` — ${note}` : null}
      </p>

      <h2 className="mb-3 text-sm font-semibold uppercase tracking-wider text-[var(--ink-faint)]">
        Plans
      </h2>
      <div className="mb-10 grid gap-4 sm:grid-cols-3">
        {plans.map((p) => (
          <div
            key={p.id}
            className={`rounded-md border px-4 py-4 ${
              p.id === planId
                ? "border-[var(--ink)] bg-[var(--surface)]"
                : "border-[var(--border)]"
            }`}
          >
            <p className="text-lg font-medium text-[var(--ink)]">{p.name}</p>
            <p className="mt-1 text-sm text-[var(--ink-muted)]">
              ${p.price_usd}/mo
            </p>
            <p className="mt-3 text-xs text-[var(--ink-faint)]">
              {p.build_minutes} build min · {p.runtime_hours} runtime hrs
            </p>
            <p className="mt-2 text-sm text-[var(--ink-muted)]">{p.description}</p>
          </div>
        ))}
      </div>

      <h2 className="mb-3 text-sm font-semibold uppercase tracking-wider text-[var(--ink-faint)]">
        Usage (30 days)
      </h2>
      {usage.length === 0 ? (
        <EmptyState
          title="No usage yet"
          description="Deploy or build to emit stub usage events from Redis streams."
        />
      ) : (
        <table className="w-full max-w-md text-left text-sm">
          <thead>
            <tr className="border-b border-[var(--border)] text-[var(--ink-faint)]">
              <th className="py-2 font-medium">Metric</th>
              <th className="py-2 font-medium">Quantity</th>
              <th className="py-2 font-medium">Unit</th>
            </tr>
          </thead>
          <tbody>
            {usage.map((u) => (
              <tr key={u.metric} className="border-b border-[var(--border)]">
                <td className="py-2 text-[var(--ink)]">{u.metric}</td>
                <td className="py-2 text-[var(--ink-muted)]">{u.quantity}</td>
                <td className="py-2 text-[var(--ink-muted)]">{u.unit}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
