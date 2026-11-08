"use client";

import Link from "next/link";
import { forwardRef } from "react";
import { cn } from "@/lib/cn";
import { Spinner } from "./states";

type Variant = "default" | "primary" | "danger" | "ghost";

interface CommonProps {
  variant?: Variant;
  size?: "md" | "sm";
  icon?: boolean;
  loading?: boolean;
}

function classes({ variant = "default", size = "md", icon }: CommonProps, extra?: string) {
  return cn(
    "btn",
    variant === "primary" && "btn--primary",
    variant === "danger" && "btn--danger",
    variant === "ghost" && "btn--ghost",
    size === "sm" && "btn--sm",
    icon && "btn--icon",
    extra,
  );
}

export const Button = forwardRef<HTMLButtonElement, React.ButtonHTMLAttributes<HTMLButtonElement> & CommonProps>(function Button(
  { variant, size, icon, loading, className, children, disabled, type = "button", ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={classes({ variant, size, icon }, className)}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      {...rest}
    >
      {loading && <Spinner size={16} />}
      {children}
    </button>
  );
});

export function LinkButton({
  variant,
  size,
  icon,
  className,
  external,
  ...rest
}: React.ComponentProps<typeof Link> & CommonProps & { external?: boolean }) {
  const cls = classes({ variant, size, icon }, className);
  if (external) {
    const { href, children, ...others } = rest as { href: string; children: React.ReactNode } & React.AnchorHTMLAttributes<HTMLAnchorElement>;
    return (
      <a href={href} className={cls} target="_blank" rel="noopener noreferrer" {...others}>
        {children}
      </a>
    );
  }
  return <Link className={cls} {...rest} />;
}
