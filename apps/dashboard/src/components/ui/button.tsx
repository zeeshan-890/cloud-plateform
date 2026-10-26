import { type ButtonHTMLAttributes, type ReactNode } from "react";

type Variant = "primary" | "secondary" | "ghost" | "danger" | "success";
type Size = "sm" | "md" | "lg";

const variants: Record<Variant, string> = {
  primary:
    "bg-[var(--accent)] text-white font-semibold hover:bg-[var(--accent-hover)] shadow-sm",
  secondary:
    "bg-[var(--surface)] text-[var(--ink)] border border-[var(--border)] font-medium hover:bg-[var(--surface-hover)] hover:border-[var(--border-strong)] shadow-xs",
  ghost:
    "bg-transparent text-[var(--ink-secondary)] font-medium hover:bg-[var(--surface-hover)] hover:text-[var(--ink)]",
  danger:
    "bg-[var(--danger-soft)] text-[var(--danger)] border border-[var(--danger-border)] font-medium hover:bg-[var(--danger)] hover:text-white shadow-xs",
  success:
    "bg-[var(--ok-soft)] text-[var(--ok)] border border-[var(--ok-border)] font-medium hover:bg-[var(--ok)] hover:text-white shadow-xs",
};

const sizes: Record<Size, string> = {
  sm: "px-2.5 py-1 text-xs rounded",
  md: "px-3.5 py-1.5 text-sm rounded-md",
  lg: "px-4 py-2 text-base rounded-md",
};

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  children: ReactNode;
}

export function Button({
  variant = "primary",
  size = "md",
  className = "",
  children,
  disabled,
  ...props
}: ButtonProps) {
  return (
    <button
      className={`inline-flex items-center justify-center gap-1.5 font-medium transition-all duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent)] focus-visible:ring-offset-1 disabled:pointer-events-none disabled:opacity-50 cursor-pointer ${variants[variant]} ${sizes[size]} ${className}`}
      disabled={disabled}
      {...props}
    >
      {children}
    </button>
  );
}
