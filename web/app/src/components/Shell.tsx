import { useEffect, useState } from "react";
import { NavLink, Outlet } from "react-router-dom";
import { api, type Health } from "@/lib/api";
import { getTheme, setTheme, cycleTheme, type Theme } from "@/lib/theme";
import { useAuth } from "@/lib/auth";
import { NAV } from "@/nav";
import { t, useLang, setLang, getLang, LANGS } from "@/lib/i18n";
import CommandPalette from "@/components/CommandPalette";
import { Link } from "react-router-dom";

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
  const [links, setLinks] = useState<{ label: string; url: string }[]>([]);
  useEffect(() => { void api.sidebar().then(setLinks).catch(() => {}); }, []);
  const [health, setHealth] = useState<Health | null>(null);
  const [theme, setThemeState] = useState<Theme>(getTheme());
  const [open, setOpen] = useState(false);
  useLang();
  const { state, signOut } = useAuth();
  const username = state.status === "authed" ? state.me.user.username : "";
  const needsTwoFactor = state.status === "authed" && state.me.user.role === "admin" && !state.me.user.totpEnabled;

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
      {open && <button type="button" aria-label="Close navigation" onClick={() => setOpen(false)} className="fixed inset-0 z-30 bg-black/40 md:hidden" />}
      <aside className={`border-r border-border bg-surface md:static md:block md:w-auto md:shadow-none ${open ? "fixed inset-y-0 left-0 z-40 block w-64 overflow-y-auto shadow-xl" : "hidden"}`}>
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
              <span>{t("nav." + n.key)}</span>
              {!n.ready && <span className="font-mono text-[11px] text-ink-faint">{n.phase}</span>}
            </NavLink>
          ))}
          {links.length > 0 && <div className="mt-2 border-t border-border pt-2">{links.map((l, i) => (
            <NavLink key={i} to={`/embed/${i}`} onClick={() => setOpen(false)} className={({ isActive }) => `flex items-center rounded-md px-2.5 py-1.5 font-medium transition-colors ${isActive ? "bg-surface-2 text-ink" : "text-ink-muted hover:text-ink"}`}>{l.label}</NavLink>
          ))}</div>}
        </nav>
      </aside>

      <div className="flex min-w-0 flex-col">
        <header className="flex h-12 items-center justify-between gap-2 border-b border-border bg-surface px-3 md:px-4">
          <div className="flex min-w-0 items-center gap-3">
            <button
              type="button"
              className="rounded-md border border-border-strong px-2 py-1 text-xs md:hidden"
              onClick={() => setOpen((o) => !o)}
              aria-label="Toggle navigation"
            >
              {t("shell.menu")}
            </button>
            <span className={`inline-block h-2 w-2 rounded-full ${health ? "bg-success" : "bg-danger"}`} aria-hidden="true" />
            <span className="truncate font-medium">{health?.hostname ?? t("shell.unreachable")}</span>
            {health && <code className="text-xs text-ink-muted">{health.version}</code>}
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={toggleTheme}
              className="rounded-md border border-border-strong px-2.5 py-1 text-xs font-medium hover:bg-surface-2"
              title={t("shell.theme")}
            >
              {theme === "system" ? t("shell.theme.system") : theme === "light" ? t("shell.theme.light") : t("shell.theme.dark")}
            </button>
            <select value={getLang()} onChange={(e) => setLang(e.target.value as "en" | "de" | "mk")} aria-label="Language" className="hidden h-7 rounded-md border border-border-strong bg-bg px-1 text-xs sm:block">{LANGS.map((l) => <option key={l.code} value={l.code}>{l.code.toUpperCase()}</option>)}</select>
            <span className="hidden text-xs text-ink-muted sm:inline">{username}</span>
            <button type="button" onClick={() => void signOut()} className="whitespace-nowrap rounded-md border border-border-strong px-2.5 py-1 text-xs font-medium hover:bg-surface-2">
              {t("shell.signout")}
            </button>
          </div>
        </header>
        {needsTwoFactor && (
          <div className="border-b border-warning/40 bg-warning-soft px-4 py-2 text-sm text-warning">
            {t("shell.2fa")}{" "}
            <Link to="/settings" className="font-medium underline underline-offset-2">{t("shell.2fa.link")}</Link>
          </div>
        )}
        <main className="min-w-0 flex-1 p-4 md:p-6">
          <Outlet context={{ health }} />
        </main>
        <CommandPalette extra={[
          { id: "theme", label: "Switch theme", hint: theme, run: toggleTheme },
          { id: "signout", label: t("shell.signout"), run: () => void signOut() },
        ]} />
      </div>
    </div>
  );
}
