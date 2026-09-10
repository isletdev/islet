import { useEffect, useState } from "react";
import { NavLink, Outlet } from "react-router-dom";
import { api, type Health } from "@/lib/api";
import { getTheme, setTheme, cycleTheme, type Theme } from "@/lib/theme";
import { useAuth } from "@/lib/auth";
import { NAV } from "@/nav";

function Mark({ className = "" }: { className?: string }) {
  return (
    <svg viewBox="0 0 100 100" className={className} aria-hidden="true">
      <g fill="currentColor">
        <path d="M4 22 H50 V50 H78 V96 H4 Z" />
        <rect x="62" y="6" width="34" height="34" />
      </g>
    </svg>
  );
}

export default function Shell() {
  const [health, setHealth] = useState<Health | null>(null);
  const [theme, setThemeState] = useState<Theme>(getTheme());
  const [open, setOpen] = useState(false);
  const { state, signOut } = useAuth();
  const username = state.status === "authed" ? state.me.user.username : "";

  useEffect(() => {
    let alive = true;
    const tick = () => api.health().then((h) => alive && setHealth(h)).catch(() => alive && setHealth(null));
    tick();
    const id = setInterval(tick, 15000);
    return () => { alive = false; clearInterval(id); };
  }, []);

  const toggleTheme = () => {
    const next = cycleTheme(theme);
    setTheme(next);
    setThemeState(next);
  };

  return (
    <div className="min-h-screen grid grid-cols-1 md:grid-cols-[220px_1fr]">
      <aside className={`border-r border-border bg-surface md:block ${open ? "block" : "hidden"}`}>
        <div className="flex h-12 items-center gap-2.5 border-b border-border px-4 font-semibold tracking-[-0.01em]">
          <Mark className="h-5 w-5" />
          Islet
        </div>
        <nav className="p-2">
          {NAV.map((n) => (
            <NavLink
              key={n.path}
              to={n.path}
              end={n.path === "/"}
              onClick={() => setOpen(false)}
              className={({ isActive }) =>
                `flex items-center justify-between rounded-md px-2.5 py-1.5 font-medium transition-colors ${
                  isActive ? "bg-surface-2 text-ink" : "text-ink-muted hover:text-ink"
                }`
              }
            >
              <span>{n.label}</span>
              {n.phase !== "v0.1" && <span className="font-mono text-[11px] text-ink-faint">{n.phase}</span>}
            </NavLink>
          ))}
        </nav>
      </aside>

      <div className="flex min-w-0 flex-col">
        <header className="flex h-12 items-center justify-between border-b border-border bg-surface px-4">
          <div className="flex items-center gap-3">
            <button
              type="button"
              className="rounded-md border border-border-strong px-2 py-1 text-xs md:hidden"
              onClick={() => setOpen((o) => !o)}
              aria-label="Toggle navigation"
            >
              Menu
            </button>
            <span className={`inline-block h-2 w-2 rounded-full ${health ? "bg-success" : "bg-danger"}`} aria-hidden="true" />
            <span className="font-medium">{health?.hostname ?? "isletd unreachable"}</span>
            {health && <code className="text-xs text-ink-muted">{health.version}</code>}
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={toggleTheme}
              className="rounded-md border border-border-strong px-2.5 py-1 text-xs font-medium hover:bg-surface-2"
              title="Theme: system, light, dark"
            >
              {theme === "system" ? "System" : theme === "light" ? "Light" : "Dark"}
            </button>
            <span className="hidden text-xs text-ink-muted sm:inline">{username}</span>
            <button type="button" onClick={() => void signOut()} className="rounded-md border border-border-strong px-2.5 py-1 text-xs font-medium hover:bg-surface-2">
              Sign out
            </button>
          </div>
        </header>
        <main className="flex-1 p-6">
          <Outlet context={{ health }} />
        </main>
      </div>
    </div>
  );
}
