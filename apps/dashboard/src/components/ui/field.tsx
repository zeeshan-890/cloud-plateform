"use client";

import { forwardRef, useId } from "react";
import { cn } from "@/lib/cn";

interface FieldShellProps {
  label?: React.ReactNode;
  hint?: React.ReactNode;
  error?: React.ReactNode;
  id: string;
  children: React.ReactNode;
  hideLabel?: boolean;
}

export function FieldShell({ label, hint, error, id, children, hideLabel }: FieldShellProps) {
  return (
    <div className="min-w-0">
      {label && (
        <label htmlFor={id} className={cn("label field-label", hideLabel && "sr-only")}>
          {label}
        </label>
      )}
      {children}
      {hint && !error && (
        <p id={`${id}-hint`} className="hint">
          {hint}
        </p>
      )}
      {error && (
        <p id={`${id}-err`} role="alert" className="error-text">
          {error}
        </p>
      )}
    </div>
  );
}

type Shared = { label?: React.ReactNode; hint?: React.ReactNode; error?: React.ReactNode; hideLabel?: boolean };

function describedBy(id: string, hint: unknown, error: unknown) {
  if (error) return `${id}-err`;
  if (hint) return `${id}-hint`;
  return undefined;
}

export const Input = forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement> & Shared>(function Input(
  { label, hint, error, hideLabel, className, id: idProp, ...rest },
  ref,
) {
  const auto = useId();
  const id = idProp ?? auto;
  return (
    <FieldShell label={label} hint={hint} error={error} id={id} hideLabel={hideLabel}>
      <input
        ref={ref}
        id={id}
        className={cn("input", className)}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy(id, hint, error)}
        {...rest}
      />
    </FieldShell>
  );
});

export const Textarea = forwardRef<HTMLTextAreaElement, React.TextareaHTMLAttributes<HTMLTextAreaElement> & Shared>(function Textarea(
  { label, hint, error, hideLabel, className, id: idProp, ...rest },
  ref,
) {
  const auto = useId();
  const id = idProp ?? auto;
  return (
    <FieldShell label={label} hint={hint} error={error} id={id} hideLabel={hideLabel}>
      <textarea
        ref={ref}
        id={id}
        className={cn("input", className)}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy(id, hint, error)}
        {...rest}
      />
    </FieldShell>
  );
});

export const Select = forwardRef<HTMLSelectElement, React.SelectHTMLAttributes<HTMLSelectElement> & Shared>(function Select(
  { label, hint, error, hideLabel, className, id: idProp, children, ...rest },
  ref,
) {
  const auto = useId();
  const id = idProp ?? auto;
  return (
    <FieldShell label={label} hint={hint} error={error} id={id} hideLabel={hideLabel}>
      <select
        ref={ref}
        id={id}
        className={cn("input", className)}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy(id, hint, error)}
        {...rest}
      >
        {children}
      </select>
    </FieldShell>
  );
});
