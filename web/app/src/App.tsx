import { lazy, Suspense } from "react";
import { Routes, Route, Navigate } from "react-router-dom";
import Shell from "./components/Shell";
import Overview from "./pages/Overview";
import Placeholder from "./pages/Placeholder";
import Settings from "./pages/Settings";

// Heavy pages (xterm, CodeMirror) load on demand so the first paint stays small.
const Terminal = lazy(() => import("./pages/Terminal"));
const ContainersRoot = lazy(() => import("./pages/Containers"));
const Files = lazy(() => import("./pages/Files"));
import Setup from "./pages/Setup";
import Login from "./pages/Login";
import { useAuth } from "./lib/auth";
import { NAV } from "./nav";

export default function App() {
  const { state } = useAuth();

  if (state.status === "loading") {
    return <div className="flex min-h-screen items-center justify-center text-sm text-ink-muted">Loading…</div>;
  }
  if (state.status === "setup") return <Setup />;
  if (state.status === "anonymous") return <Login />;
  if (state.status === "mfa") return <Login mfa />;

  return (
    <Suspense fallback={<div className="p-6 text-sm text-ink-muted">Loading…</div>}>
    <Routes>
      <Route element={<Shell />}>
        <Route index element={<Overview />} />
        <Route path="/settings" element={<Settings />} />
        <Route path="/terminal" element={<Terminal />} />
        <Route path="/containers/*" element={<ContainersRoot />} />
        <Route path="/files" element={<Files />} />
        {NAV.filter((n) => !n.ready).map((n) => (
          <Route key={n.path} path={n.path} element={<Placeholder item={n} />} />
        ))}
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
    </Suspense>
  );
}
