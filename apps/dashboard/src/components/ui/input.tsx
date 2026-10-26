import { type InputHTMLAttributes, forwardRef } from "react";

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: string;
  error?: string;
  hint?: string;
}

export const Input = forwardRef<HTMLInputElement, InputProps>(
  function Input({ label, error, hint, id, className = "", ...props }, ref) {
    const inputId =
      id || (label ? label.toLowerCase().replace(/\s+/g, "-") : undefined);
    const field = (
      <>
        <input
          ref={ref}
          id={inputId}
          className={`rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-sm text-[var(--ink)] placeholder:text-[var(--ink-faint)] shadow-2xs transition-colors duration-150 focus:border-[var(--accent)] focus:outline-none focus:ring-1 focus:ring-[var(--accent)] disabled:opacity-50 ${
            error ? "border-[var(--danger)] ring-1 ring-[var(--danger)]" : ""
          } ${className}`}
          {...props}
        />
        {hint && !error ? (
          <span className="text-xs text-[var(--ink-muted)]">{hint}</span>
        ) : null}
        {error ? (
          <span className="text-xs font-medium text-[var(--danger)]">{error}</span>
        ) : null}
      </>
    );
    if (!label) {
      return <div className="flex flex-col gap-1">{field}</div>;
    }
    return (
      <label className="flex flex-col gap-1" htmlFor={inputId}>
        <span className="text-xs font-semibold uppercase tracking-wider text-[var(--ink-secondary)]">
          {label}
        </span>
        {field}
      </label>
    );
  },
);
