import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode } from "react";

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

export function Button({ variant = "primary", className = "", ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "secondary" | "danger" }) {
  const base = "inline-flex h-9 items-center justify-center rounded-md px-3.5 text-sm font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50";
  const styles = {
    primary: "bg-ink text-on-ink hover:opacity-90",
    secondary: "border border-border-strong bg-transparent text-ink hover:bg-surface-2",
    danger: "border border-danger text-danger hover:bg-danger-soft",
  }[variant];
  return <button className={`${base} ${styles} ${className}`} {...rest} />;
}

export function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-sm font-medium">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-xs text-ink-muted">{hint}</span>}
    </label>
  );
}

export function Input({ className = "", ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={`h-9 w-full rounded-md border border-border-strong bg-bg px-3 text-sm text-ink placeholder:text-ink-faint focus:border-accent ${className}`}
      {...rest}
    />
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

export function Card({ title, description, children, className = "" }: { title?: string; description?: string; children: ReactNode; className?: string }) {
  return (
    <section className={`rounded-lg border border-border bg-surface ${className}`}>
      {(title || description) && (
        <header className="border-b border-border px-5 py-4">
          {title && <h2 className="font-semibold">{title}</h2>}
          {description && <p className="mt-0.5 text-sm text-ink-muted">{description}</p>}
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
