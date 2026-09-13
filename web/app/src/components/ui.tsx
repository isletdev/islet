import type { ComponentPropsWithRef, ReactNode } from "react";

export function Mark({ className = "" }: { className?: string }) {
  return (
    <svg viewBox="0 0 100 100" className={className} aria-hidden="true">
      <g fill="currentColor">
        <path d="M4 22 H50 V50 H78 V96 H4 Z" />
        <rect x="62" y="6" width="34" height="34" />
      </g>
    </svg>
  );
}

// React 19 passes ref through props, so these stay plain functions and still
// accept a ref, which the dialog needs to place focus.
export function Button({ variant = "primary", className = "", ...rest }: ComponentPropsWithRef<"button"> & { variant?: "primary" | "secondary" | "danger" }) {
  const base = "inline-flex h-9 items-center justify-center rounded-md px-3.5 text-sm font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50";
  const styles = {
    primary: "bg-ink text-on-ink hover:opacity-90",
    secondary: "border border-border-strong bg-transparent text-ink hover:bg-surface-2",
    danger: "border border-danger text-danger hover:bg-danger-soft",
  }[variant];
  return <button className={`${base} ${styles} ${className}`} {...rest} />;
}

// One line of label above every control, the same height everywhere, so a row of
// fields lines its inputs up whether or not a particular field carries a hint.
const LABEL = "mb-1.5 block h-5 text-sm font-medium leading-5";

export function Field({ label, hint, className = "", children }: { label: string; hint?: string; className?: string; children: ReactNode }) {
  return (
    <label className={`block ${className}`}>
      <span className={LABEL}>{label}</span>
      {children}
      {hint && <span className="mt-1 block text-xs text-ink-muted">{hint}</span>}
    </label>
  );
}

/** A button or note that sits in a row of fields: the spacer stands in for the
    label so the control lines up with the inputs next to it. */
export function FieldAction({ className = "", children }: { className?: string; children: ReactNode }) {
  // The spacer has to stay above the content, so the caller's classes go on an
  // inner row. Putting them on the outer box let a `flex` there pull the spacer
  // alongside the button instead of over it, which is the misalignment this
  // component exists to prevent.
  return (
    <div className="block">
      <span className={`${LABEL} invisible select-none`} aria-hidden="true">&nbsp;</span>
      <div className={className}>{children}</div>
    </div>
  );
}

export function Input({ className = "", ...rest }: ComponentPropsWithRef<"input">) {
  return (
    <input
      className={`h-9 w-full rounded-md border border-border-strong bg-bg px-3 text-sm text-ink placeholder:text-ink-faint focus:border-accent ${className}`}
      {...rest}
    />
  );
}

/** The same box as Input, so a select never sits a pixel off the field beside it. */
export function Select({ className = "", children, ...rest }: ComponentPropsWithRef<"select">) {
  return (
    <select
      className={`h-9 w-full rounded-md border border-border-strong bg-bg px-2.5 text-sm text-ink focus:border-accent ${className}`}
      {...rest}
    >
      {children}
    </select>
  );
}

/**
 * A row of tabs sitting on a hairline rule.
 *
 * The rule lives on the wrapper and the scrolling happens on the inner row, so
 * the tabs never overflow their own scroll box. Doing it the other way round,
 * with a negative margin on the buttons inside an `overflow-x-auto` box, makes
 * the browser promote the vertical axis to `auto` as well and show a scrollbar
 * for one stray pixel. The horizontal scrollbar is hidden because the row only
 * overflows on a phone, where it is dragged rather than clicked.
 */
export function Tabs({ label, className = "", children }: { label: string; className?: string; children: ReactNode }) {
  return (
    <div className={`border-b border-border ${className}`}>
      <div
        role="tablist"
        aria-label={label}
        className="-mb-px flex gap-1 overflow-x-auto overflow-y-hidden text-sm [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      >
        {children}
      </div>
    </div>
  );
}

export function Tab({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={`whitespace-nowrap border-b-2 px-3 py-2 transition-colors ${
        active ? "border-ink font-medium text-ink" : "border-transparent text-ink-muted hover:text-ink"
      }`}
    >
      {children}
    </button>
  );
}

export function Alert({ tone = "danger", children }: { tone?: "danger" | "success" | "warning"; children: ReactNode }) {
  const styles = {
    danger: "border-danger/40 bg-danger-soft text-danger",
    success: "border-success/40 bg-success-soft text-success",
    warning: "border-warning/40 bg-warning-soft text-warning",
  }[tone];
  return <div className={`rounded-md border px-3 py-2 text-sm ${styles}`} role="alert">{children}</div>;
}

export function Card({ title, description, icon, children, className = "" }: { title?: string; description?: string; icon?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={`rounded-lg border border-border bg-surface ${className}`}>
      {(title || description) && (
        <header className="flex items-start gap-3 border-b border-border px-5 py-4">
          {icon}
          <div className="min-w-0 flex-1">
            {title && <h2 className="font-semibold">{title}</h2>}
            {description && <p className="mt-0.5 text-sm text-ink-muted">{description}</p>}
          </div>
        </header>
      )}
      <div className="px-5 py-4">{children}</div>
    </section>
  );
}

export function AuthFrame({ title, subtitle, children }: { title: string; subtitle?: string; children: ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-bg px-4">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex items-center gap-2.5 text-lg font-semibold tracking-[-0.01em]">
          <Mark className="h-6 w-6" />
          Islet
        </div>
        <h1 className="text-xl font-semibold tracking-[-0.02em]">{title}</h1>
        {subtitle && <p className="mt-1 text-sm text-ink-muted">{subtitle}</p>}
        <div className="mt-6">{children}</div>
      </div>
    </div>
  );
}
