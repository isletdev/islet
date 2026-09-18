import { lazy, Suspense } from "react";
import { Routes, Route, Navigate } from "react-router-dom";
import Shell from "./components/Shell";
import Overview from "./pages/Overview";
import Placeholder from "./pages/Placeholder";

// Heavy pages (xterm, CodeMirror) load on demand so the first paint stays small.
// Settings pulls in the QR code library for two-factor setup, which nobody
// needs before they open it.
const Settings = lazy(() => import("./pages/Settings"));
const Terminal = lazy(() => import("./pages/Terminal"));
const Workspaces = lazy(() => import("./pages/Workspaces"));
const Media = lazy(() => import("./pages/Media"));
const Assistant = lazy(() => import("./pages/Assistant"));
const Console = lazy(() => import("./pages/Console"));
const ContainersRoot = lazy(() => import("./pages/Containers"));
const Files = lazy(() => import("./pages/Files"));
const Domains = lazy(() => import("./pages/Domains"));
const DomainImport = lazy(() => import("./pages/DomainImport"));
const Apps = lazy(() => import("./pages/Apps"));
const Notifications = lazy(() => import("./pages/Notifications"));
const Cron = lazy(() => import("./pages/Cron"));
const Databases = lazy(() => import("./pages/Databases"));
const Monitoring = lazy(() => import("./pages/Monitoring"));
const Runners = lazy(() => import("./pages/Runners"));
const Security = lazy(() => import("./pages/Security"));
const Backups = lazy(() => import("./pages/Backups"));
const Embed = lazy(() => import("./pages/Embed"));
const Servers = lazy(() => import("./pages/Servers"));
// The SQL client carries CodeMirror and its SQL grammar; it loads when
// somebody opens it and not before.
const Sql = lazy(() => import("./pages/Sql"));
import Setup from "./pages/Setup";
import Login from "./pages/Login";
import Returning from "./pages/Returning";
import { useAuth } from "./lib/auth";
import { NAV } from "./nav";
import { t } from "./lib/i18n";

export default function App() {
  const { state } = useAuth();

  if (state.status === "loading") {
    return <div className="flex min-h-screen items-center justify-center text-sm text-ink-muted">{t("shell.loading")}</div>;
  }
  if (state.status === "setup") return <Setup />;
  if (state.status === "anonymous") return <Login />;
  if (state.status === "mfa") return <Login mfa />;
  // Sent here by a protected site while already signed in. Without this the
  // catch-all route below took them to the dashboard and threw the address they
  // asked for away, which made "protect with Islet login" look like a link to
  // the panel.
  if (new URLSearchParams(location.search).get("next")) return <Returning />;

  return (
    <Suspense fallback={<div className="p-6 text-sm text-ink-muted">{t("shell.loading")}</div>}>
    <Routes>
      {/* Outside the shell on purpose: the window is only a terminal. */}
      <Route path="/console" element={<Console />} />
      <Route element={<Shell />}>
        <Route index element={<Overview />} />
        <Route path="/settings" element={<Settings />} />
        <Route path="/servers" element={<Servers />} />
        <Route path="/terminal" element={<Terminal />} />
        <Route path="/workspaces" element={<Workspaces />} />
        <Route path="/media" element={<Media />} />
        <Route path="/assistant" element={<Assistant />} />
        {/* The vault is a section of Settings now. The old path still works,
            because it is in the docs, in the MCP tool description and in
            anybody's bookmarks. */}
        <Route path="/vault" element={<Navigate to="/settings?tab=secrets" replace />} />
        <Route path="/containers/*" element={<ContainersRoot />} />
        <Route path="/files" element={<Files />} />
        <Route path="/domains" element={<Domains />} />
        <Route path="/domains/import" element={<DomainImport />} />
        <Route path="/apps" element={<Apps />} />
        <Route path="/notifications" element={<Notifications />} />
        <Route path="/cron" element={<Cron />} />
        <Route path="/databases" element={<Databases />} />
        <Route path="/sql" element={<Sql />} />
        {/* One page, two tabs: the question is "is it up, and why not". */}
        <Route path="/uptime" element={<Monitoring />} />
        <Route path="/logs" element={<Monitoring />} />
        <Route path="/runners" element={<Runners />} />
        <Route path="/security" element={<Security />} />
        <Route path="/backups" element={<Backups />} />
        <Route path="/embed/:i" element={<Embed />} />
        {NAV.filter((n) => !n.ready).map((n) => (
          <Route key={n.path} path={n.path} element={<Placeholder item={n} />} />
        ))}
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
    </Suspense>
  );
}
