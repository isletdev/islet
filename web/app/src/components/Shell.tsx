import { useEffect, useRef, useState } from "react";
import { NavLink, Outlet, Link } from "react-router-dom";
import { api, type Health } from "@/lib/api";
import { getTheme, setTheme, otherTheme, type Theme } from "@/lib/theme";
import { useAuth } from "@/lib/auth";
import { NAV, NAV_GROUPS, type NavItem } from "@/nav";
import { t } from "@/lib/i18n";
import CommandPalette, { openPalette } from "@/components/CommandPalette";
import { Mark } from "@/components/ui";
import { pollInterval } from "@/lib/poll";
import {
  NAV_ICONS, MenuIcon, CloseIcon, SunIcon, MoonIcon, UserIcon, ChevronDownIcon,
  SignOutIcon, SettingsIcon, ShieldAlertIcon, ExternalIcon, SearchIcon,
} from "@/components/icons";

const TWO_FACTOR_DISMISSED = "islet.2fa.dismissed";

export default function Shell() {
  const [links, setLinks] = useState<{ label: string; url: string }[]>([]);
  const [health, setHealth] = useState<Health | null>(null);
  const [theme, setThemeState] = useState<Theme>(getTheme());
  const [navOpen, setNavOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [hideTwoFactor, setHideTwoFactor] = useState(() => {
    try { return sessionStorage.getItem(TWO_FACTOR_DISMISSED) === "1"; } catch { return false; }
  });
  const menuRef = useRef<HTMLDivElement>(null);
  const { state, signOut } = useAuth();
  const user = state.status === "authed" ? state.me.user : null;
  const needsTwoFactor = !!user && user.role === "admin" && !user.totpEnabled && !hideTwoFactor;

  useEffect(() => { void api.sidebar().then(setLinks).catch(() => {}); }, []);

  useEffect(() => {
    let alive = true;
    const tick = () => api.health().then((h) => alive && setHealth(h)).catch(() => alive && setHealth(null));
    tick();
    const stop = pollInterval(tick, 15000);
    return () => { alive = false; stop(); };
  }, []);

  // The account menu closes on a click elsewhere or on Escape.
  useEffect(() => {
    if (!menuOpen) return;
    const onDown = (e: MouseEvent) => { if (!menuRef.current?.contains(e.target as Node)) setMenuOpen(false); };
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setMenuOpen(false); };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => { document.removeEventListener("mousedown", onDown); document.removeEventListener("keydown", onKey); };
  }, [menuOpen]);

  const flipTheme = () => {
    const next = otherTheme(theme);
    setTheme(next);
    setThemeState(next);
  };

  const dismissTwoFactor = () => {
    setHideTwoFactor(true);
    try { sessionStorage.setItem(TWO_FACTOR_DISMISSED, "1"); } catch { /* ignore */ }
  };

  const closeNav = () => setNavOpen(false);

  return (
    <div className="flex h-screen overflow-hidden bg-bg">
      {navOpen && (
        <button
          type="button"
          aria-label="Close navigation"
          onClick={closeNav}
          className="fixed inset-0 z-30 bg-black/40 md:hidden"
        />
      )}

      <aside
        className={`z-40 w-[250px] shrink-0 flex-col border-r border-border bg-surface ${
          navOpen ? "fixed inset-y-0 left-0 flex shadow-float" : "hidden"
        } md:static md:flex md:shadow-none`}
      >
        <div className="flex h-14 shrink-0 items-center gap-2.5 px-4 text-[15px] font-semibold tracking-[-0.01em]">
          <Mark className="h-[22px] w-[22px]" />
          Islet
          <button
            type="button"
            onClick={closeNav}
            aria-label="Close navigation"
            className="ml-auto rounded-md p-1.5 text-ink-muted hover:bg-surface-2 hover:text-ink md:hidden"
          >
            <CloseIcon />
          </button>
        </div>

        <nav className="min-h-0 flex-1 overflow-y-auto px-3 pb-4">
          {NAV_GROUPS.map((g) => {
            const items = NAV.filter((n) => n.group === g.id);
            if (items.length === 0) return null;
            return (
              <div key={g.id} className="pt-4 first:pt-1">
                <div className="px-2.5 pb-1.5 text-[11px] font-medium text-ink-faint">{g.label}</div>
                <div className="space-y-0.5">
                  {items.map((n) => <NavRow key={n.path} item={n} onNavigate={closeNav} />)}
                </div>
              </div>
            );
          })}

          {links.length > 0 && (
            <div className="pt-4">
              <div className="px-2.5 pb-1.5 text-[11px] font-medium text-ink-faint">Your apps</div>
              <div className="space-y-0.5">
                {links.map((l, i) => (
                  <NavLink
                    key={i}
                    to={`/embed/${i}`}
                    onClick={closeNav}
                    className={({ isActive }) => rowClass(isActive)}
                  >
                    <span className={`flex h-[18px] w-[18px] items-center justify-center`}><ExternalIcon className="h-4 w-4" /></span>
                    <span className="truncate">{l.label}</span>
                  </NavLink>
                ))}
              </div>
            </div>
          )}
        </nav>

        <div className="shrink-0 border-t border-border p-3">
          {NAV.filter((n) => n.group === "account").map((n) => <NavRow key={n.path} item={n} onNavigate={closeNav} />)}
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center justify-between gap-2 border-b border-border bg-surface px-3 md:px-5">
          <div className="flex min-w-0 items-center gap-3">
            <button
              type="button"
              className="rounded-md p-1.5 text-ink-muted hover:bg-surface-2 hover:text-ink md:hidden"
              onClick={() => setNavOpen(true)}
              aria-label="Open navigation"
            >
              <MenuIcon className="h-5 w-5" />
            </button>
            <span
              className={`inline-block h-2 w-2 shrink-0 rounded-full ${health ? "bg-success" : "bg-danger"}`}
              title={health ? "Daemon reachable" : t("shell.unreachable")}
              aria-hidden="true"
            />
            <span className="truncate text-[13px] font-medium">{health?.hostname ?? t("shell.unreachable")}</span>
            {health && (
              <code className="hidden rounded-sm bg-surface-2 px-1.5 py-0.5 text-[11px] text-ink-muted sm:inline">{health.version}</code>
            )}
          </div>

          <div className="flex items-center gap-1.5">
            <button
              type="button"
              onClick={openPalette}
              className="hidden h-8 items-center gap-2 rounded-md border border-border px-2.5 text-xs text-ink-muted hover:bg-surface-2 hover:text-ink lg:flex"
              aria-label="Search"
            >
              <SearchIcon className="h-4 w-4" />
              <span>Search</span>
              <kbd className="rounded-sm border border-border px-1 font-mono text-[10px] text-ink-faint">⌘K</kbd>
            </button>

            <button
              type="button"
              onClick={flipTheme}
              className="rounded-md p-2 text-ink-muted hover:bg-surface-2 hover:text-ink"
              title={theme === "dark" ? "Switch to light" : "Switch to dark"}
              aria-label={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
            >
              {theme === "dark" ? <SunIcon className="h-[18px] w-[18px]" /> : <MoonIcon className="h-[18px] w-[18px]" />}
            </button>

            <div className="relative" ref={menuRef}>
              <button
                type="button"
                onClick={() => setMenuOpen((o) => !o)}
                className="flex h-9 items-center gap-1.5 rounded-md pl-1 pr-1.5 hover:bg-surface-2"
                aria-haspopup="menu"
                aria-expanded={menuOpen}
                aria-label="Account menu"
              >
                <span className="flex h-7 w-7 items-center justify-center rounded-full bg-surface-2 text-ink-muted">
                  <UserIcon className="h-[18px] w-[18px]" />
                </span>
                <ChevronDownIcon className={`h-4 w-4 text-ink-faint transition-transform ${menuOpen ? "rotate-180" : ""}`} />
              </button>

              {menuOpen && (
                <div
                  role="menu"
                  className="absolute right-0 top-full z-50 mt-1.5 w-56 overflow-hidden rounded-lg border border-border bg-surface shadow-float"
                >
                  <div className="border-b border-border px-3 py-2.5">
                    <div className="truncate text-[13px] font-medium">{user?.username}</div>
                    <div className="mt-0.5 text-[11px] capitalize text-ink-muted">{user?.role}</div>
                  </div>
                  <Link
                    to="/settings"
                    role="menuitem"
                    onClick={() => setMenuOpen(false)}
                    className="flex items-center gap-2.5 px-3 py-2 text-[13px] text-ink-muted hover:bg-surface-2 hover:text-ink"
                  >
                    <SettingsIcon className="h-4 w-4" />
                    Account settings
                  </Link>
                  <button
                    type="button"
                    role="menuitem"
                    onClick={() => { setMenuOpen(false); void signOut(); }}
                    className="flex w-full items-center gap-2.5 border-t border-border px-3 py-2 text-left text-[13px] text-ink-muted hover:bg-surface-2 hover:text-ink"
                  >
                    <SignOutIcon className="h-4 w-4" />
                    {t("shell.signout")}
                  </button>
                </div>
              )}
            </div>
          </div>
        </header>

        <main className="min-h-0 flex-1 overflow-y-auto p-4 md:p-6">
          <Outlet context={{ health }} />
        </main>
      </div>

      {needsTwoFactor && (
        <div className="pointer-events-none fixed inset-x-0 bottom-0 z-30 flex justify-center p-4 md:justify-end md:p-5">
          <div className="pointer-events-auto flex w-full max-w-sm items-start gap-3 rounded-lg border border-warning/40 bg-warning-soft p-3 text-warning shadow-float">
            <ShieldAlertIcon className="mt-px h-[18px] w-[18px] shrink-0" />
            <div className="min-w-0 flex-1 text-[13px]">
              <p>{t("shell.2fa")}</p>
              <Link to="/settings" className="mt-1 inline-block font-medium underline underline-offset-2">
                {t("shell.2fa.link")}
              </Link>
            </div>
            <button
              type="button"
              onClick={dismissTwoFactor}
              aria-label="Dismiss until next sign-in"
              className="-mr-1 -mt-1 shrink-0 rounded-md p-1 hover:bg-warning/10"
            >
              <CloseIcon className="h-4 w-4" />
            </button>
          </div>
        </div>
      )}

      <CommandPalette extra={[
        { id: "theme", label: theme === "dark" ? "Switch to light theme" : "Switch to dark theme", run: flipTheme },
        { id: "signout", label: t("shell.signout"), run: () => void signOut() },
      ]} />
    </div>
  );
}

function rowClass(isActive: boolean) {
  return `relative flex h-9 items-center gap-2.5 rounded-md px-2.5 text-[13px] font-medium transition-colors ${
    isActive ? "bg-surface-2 text-ink" : "text-ink-muted hover:bg-surface-2/60 hover:text-ink"
  }`;
}

function NavRow({ item, onNavigate }: { item: NavItem; onNavigate: () => void }) {
  const Glyph = NAV_ICONS[item.key];
  return (
    <NavLink to={item.path} end={item.path === "/"} onClick={onNavigate} className={({ isActive }) => rowClass(isActive)}>
      {({ isActive }) => (
        <>
          {isActive && <span className="absolute inset-y-1.5 left-0 w-[3px] rounded-full bg-accent" aria-hidden="true" />}
          {Glyph && <Glyph className={`h-[18px] w-[18px] shrink-0 ${isActive ? "text-accent" : ""}`} />}
          <span className="truncate">{t("nav." + item.key)}</span>
          {!item.ready && <span className="ml-auto font-mono text-[11px] text-ink-faint">{item.phase}</span>}
        </>
      )}
    </NavLink>
  );
}
