import { Routes, Route, Navigate } from "react-router-dom";
import Shell from "./components/Shell";
import Overview from "./pages/Overview";
import Placeholder from "./pages/Placeholder";
import Settings from "./pages/Settings";
import Terminal from "./pages/Terminal";
import ContainersRoot from "./pages/Containers";
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
    <Routes>
      <Route element={<Shell />}>
        <Route index element={<Overview />} />
        <Route path="/settings" element={<Settings />} />
        <Route path="/terminal" element={<Terminal />} />
        <Route path="/containers/*" element={<ContainersRoot />} />
        {NAV.filter((n) => !n.ready).map((n) => (
          <Route key={n.path} path={n.path} element={<Placeholder item={n} />} />
        ))}
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
