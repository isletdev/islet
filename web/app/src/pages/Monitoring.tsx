import { NavLink, useLocation } from "react-router-dom";
import Logs from "@/pages/Logs";
import Uptime from "@/pages/Uptime";

/**
 * "Is it up" and "why not" are one question, asked in one place.
 *
 * They were two sidebar entries in two different groups — Logs under Server,
 * Uptime under Operate — which put the two halves of a single investigation as
 * far apart as the sidebar could manage. A check goes red and the next thing
 * anybody wants is the log of the thing that went red.
 *
 * Both paths still work and both are still linked from elsewhere: a
 * notification about a failed check points at /uptime and has to keep landing
 * on the checks. So the tab follows the path rather than a query parameter —
 * nothing that links here needs rewriting, and the address bar still says which
 * half is on screen.
 */
const TABS = [
  { to: "/uptime", label: "Uptime", blurb: "HTTP, TCP and keyword checks from this server." },
  { to: "/logs", label: "Logs", blurb: "System, Docker and application logs in one viewer." },
];

export default function Monitoring() {
  const { pathname } = useLocation();
  return (
    <div className="mx-auto max-w-6xl">
      <nav className="mb-4 flex flex-wrap gap-1 border-b border-border" aria-label="Monitoring">
        {TABS.map((t) => (
          <NavLink
            key={t.to}
            to={t.to}
            title={t.blurb}
            className={({ isActive }) =>
              "-mb-px border-b-2 px-3 py-2 text-sm transition-colors " +
              (isActive
                ? "border-accent font-medium text-ink"
                : "border-transparent text-ink-muted hover:border-border-strong hover:text-ink")
            }
          >
            {t.label}
          </NavLink>
        ))}
      </nav>
      {pathname.startsWith("/logs") ? <Logs /> : <Uptime />}
    </div>
  );
}
