import { Routes, Route, Navigate } from "react-router-dom";
import Shell from "./components/Shell";
import Overview from "./pages/Overview";
import Placeholder from "./pages/Placeholder";
import { NAV } from "./nav";

export default function App() {
  return (
    <Routes>
      <Route element={<Shell />}>
        <Route index element={<Overview />} />
        {NAV.filter((n) => n.path !== "/").map((n) => (
          <Route key={n.path} path={n.path} element={<Placeholder item={n} />} />
        ))}
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
