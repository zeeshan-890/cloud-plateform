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
          className={`rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-2.5 text-sm text-[var(--ink)] placeholder:text-[var(--ink-faint)] transition-colors duration-200 focus:border-[var(--accent)] focus:outline-none focus:ring-2 focus:ring-[var(--accent)]/30 disabled:opacity-50 ${error ? "border-[var(--danger)]" : ""} ${className}`}
          {...props}
        />
        {hint && !error ? (
          <span className="text-xs text-[var(--ink-muted)]">{hint}</span>
        ) : null}
        {error ? <span className="text-xs text-[var(--danger)]">{error}</span> : null}
      </>
    );
    if (!label) {
      return <div className="flex flex-col gap-1.5">{field}</div>;
    }
    return (
      <label className="flex flex-col gap-1.5" htmlFor={inputId}>
        <span className="text-sm font-medium text-[var(--ink)]">{label}</span>
        {field}
      </label>
    );
  },
);
