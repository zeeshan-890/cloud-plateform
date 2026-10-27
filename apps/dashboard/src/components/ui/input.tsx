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
          className={`rounded-lg border-2 border-slate-300 bg-white px-3 py-2 text-xs font-medium text-slate-900 placeholder:text-slate-400 shadow-2xs transition-colors duration-150 focus:border-[#1E3A8A] focus:outline-none focus:ring-2 focus:ring-blue-500/20 disabled:opacity-50 ${
            error ? "border-red-500 ring-2 ring-red-500/20" : ""
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
