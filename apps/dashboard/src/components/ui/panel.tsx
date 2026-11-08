import { cn } from "@/lib/cn";

/** Corner screw. Rotation is derived from a seed so every screw looks hand-seated. */
export function Screw({ seed = 0 }: { seed?: number }) {
  const rot = ((seed * 47 + 13) % 180) - 90;
  return <span className="screw" aria-hidden="true" style={{ "--rot": `${rot}deg` } as React.CSSProperties} />;
}

interface PanelProps extends Omit<React.HTMLAttributes<HTMLElement>, "title"> {
  variant?: "metal" | "paper" | "leather";
  /** show the four corner screws */
  screws?: boolean;
  as?: "section" | "div" | "article" | "aside";
  title?: React.ReactNode;
  actions?: React.ReactNode;
  flat?: boolean;
}

/** Raised rack panel: brushed metal, warm paper or stitched leather. */
export function Panel({ variant = "metal", screws = true, as = "section", title, actions, flat, className, children, ...rest }: PanelProps) {
  const Tag = as;
  return (
    <Tag
      className={cn(
        "panel",
        variant === "paper" && "panel--paper",
        variant === "leather" && "panel--leather",
        screws && "panel--screws",
        flat && "panel--flat",
        className,
      )}
      {...rest}
    >
      {screws && (
        <>
          <Screw seed={1} />
          <Screw seed={2} />
          <Screw seed={3} />
          <Screw seed={4} />
        </>
      )}
      {(title || actions) && (
        <header className="panel-title">
          {typeof title === "string" ? <h2>{title}</h2> : title}
          {actions && <div className="ml-auto flex flex-wrap items-center gap-2">{actions}</div>}
        </header>
      )}
      {children}
    </Tag>
  );
}

/** Inset recessed well for data readouts. */
export function Well({ lcd, className, children, ...rest }: React.HTMLAttributes<HTMLDivElement> & { lcd?: boolean }) {
  return (
    <div className={cn("well", lcd && "well--lcd", className)} {...rest}>
      {children}
    </div>
  );
}

export function PageHeader({
  title,
  eyebrow,
  description,
  actions,
}: {
  title: string;
  eyebrow?: string;
  description?: React.ReactNode;
  actions?: React.ReactNode;
}) {
  return (
    <div className="page-head mb-6 flex flex-wrap items-end justify-between gap-4">
      <div className="min-w-0">
        {eyebrow && <p className="label mb-1">{eyebrow}</p>}
        <h1 className="engrave text-[34px] leading-none uppercase tracking-[0.04em] !text-[var(--ink)]">{title}</h1>
        {description && <p className="muted mt-2 max-w-2xl text-[15px]">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

/** Labelled value on an engraved caption, used in definition grids. */
export function Readout({ label, children, mono = true }: { label: string; children: React.ReactNode; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <div className="label mb-1">{label}</div>
      <div className={cn("break-words text-[15px]", mono && "mono text-[13.5px]")}>{children}</div>
    </div>
  );
}
