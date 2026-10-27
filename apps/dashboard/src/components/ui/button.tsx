import { type ButtonHTMLAttributes, type ReactNode } from "react";

type Variant = "primary" | "secondary" | "ghost" | "danger" | "success";
type Size = "sm" | "md" | "lg";

const variants: Record<Variant, string> = {
  primary:
    "bg-[#1E3A8A] text-white font-bold hover:bg-[#162456] shadow-sm transition-colors",
  secondary:
    "bg-white text-slate-700 border-2 border-slate-300 font-bold hover:bg-slate-50 hover:border-slate-400 hover:text-slate-900 shadow-sm transition-colors",
  ghost:
    "bg-transparent text-slate-700 font-semibold hover:bg-slate-200/70 hover:text-slate-900 transition-colors",
  danger:
    "bg-red-50 text-red-700 border-2 border-red-200 font-bold hover:bg-red-600 hover:text-white hover:border-red-600 shadow-sm transition-colors",
  success:
    "bg-emerald-50 text-emerald-800 border-2 border-emerald-300 font-bold hover:bg-emerald-600 hover:text-white hover:border-emerald-600 shadow-sm transition-colors",
};

const sizes: Record<Size, string> = {
  sm: "h-8 px-3 text-xs rounded-lg",
  md: "h-9 px-4 text-xs rounded-lg",
  lg: "h-11 px-6 text-sm rounded-lg",
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
      className={`inline-flex items-center justify-center gap-1.5 whitespace-nowrap transition-all duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/40 focus-visible:ring-offset-1 disabled:pointer-events-none disabled:opacity-50 cursor-pointer ${variants[variant]} ${sizes[size]} ${className}`}
      disabled={disabled}
      {...props}
    >
      {children}
    </button>
  );
}
