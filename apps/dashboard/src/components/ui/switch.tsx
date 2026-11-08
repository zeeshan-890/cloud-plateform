"use client";

import { useId, useRef } from "react";
import { cn } from "@/lib/cn";

interface SwitchProps {
  checked: boolean;
  onChange: (next: boolean) => void;
  label: React.ReactNode;
  description?: React.ReactNode;
  disabled?: boolean;
  className?: string;
}

/** Hardware slide switch with an LED on the track. Space/Enter toggle. */
export function Switch({ checked, onChange, label, description, disabled, className }: SwitchProps) {
  const id = useId();
  return (
    <div className={cn("flex items-center gap-3", className)}>
      <button
        type="button"
        role="switch"
        id={id}
        aria-checked={checked}
        aria-describedby={description ? `${id}-d` : undefined}
        disabled={disabled}
        className="switch"
        onClick={() => onChange(!checked)}
      >
        <span className="switch-thumb" />
        <span className="sr-only">{checked ? "On" : "Off"}</span>
      </button>
      <div className="min-w-0">
        <label htmlFor={id} className="block cursor-pointer text-[15px] font-medium">
          {label}
        </label>
        {description && (
          <p id={`${id}-d`} className="hint !mt-0">
            {description}
          </p>
        )}
      </div>
    </div>
  );
}

interface KnobOption<T extends string> {
  value: T;
  label: string;
}

interface KnobProps<T extends string> {
  value: T;
  options: KnobOption<T>[];
  onChange: (v: T) => void;
  label: string;
  className?: string;
}

/**
 * Rotary position selector. The dial turns to the chosen detent; options are
 * real radio buttons underneath so keyboard and screen readers work normally.
 */
export function Knob<T extends string>({ value, options, onChange, label, className }: KnobProps<T>) {
  const idx = Math.max(0, options.findIndex((o) => o.value === value));
  const n = options.length;
  const angle = n <= 1 ? 0 : -120 + (idx * 240) / (n - 1);
  const refs = useRef<Array<HTMLButtonElement | null>>([]);

  const move = (to: number) => {
    const next = (to + n) % n;
    onChange(options[next].value);
    refs.current[next]?.focus();
  };

  return (
    <div className={cn("knob-wrap", className)}>
      <span className="label">{label}</span>
      <button
        type="button"
        className="knob-dial"
        tabIndex={-1}
        aria-hidden="true"
        onClick={() => move(idx + 1)}
        style={{ cursor: "pointer", border: 0, padding: 0, background: undefined }}
      >
        {options.map((o, i) => {
          const a = n <= 1 ? 0 : -120 + (i * 240) / (n - 1);
          return <span key={o.value} className="knob-tick" data-on={i === idx} style={{ transform: `rotate(${a}deg)` }} />;
        })}
        <span className="knob-body" style={{ transform: `rotate(${angle}deg)` }} />
      </button>
      <div role="radiogroup" aria-label={label} className="flex flex-wrap justify-center gap-1">
        {options.map((o, i) => (
          <button
            key={o.value}
            ref={(el) => {
              refs.current[i] = el;
            }}
            type="button"
            role="radio"
            aria-checked={i === idx}
            tabIndex={i === idx ? 0 : -1}
            className={cn("tag cursor-pointer", i === idx ? "tag--accent" : "tag--idle")}
            onClick={() => onChange(o.value)}
            onKeyDown={(e) => {
              if (e.key === "ArrowRight" || e.key === "ArrowDown") {
                e.preventDefault();
                move(i + 1);
              } else if (e.key === "ArrowLeft" || e.key === "ArrowUp") {
                e.preventDefault();
                move(i - 1);
              }
            }}
          >
            {o.label}
          </button>
        ))}
      </div>
    </div>
  );
}
