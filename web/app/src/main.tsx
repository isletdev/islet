import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import "./index.css";
import App from "./App";
import { AuthProvider } from "./lib/auth";
import { DialogProvider } from "./lib/dialogs";
import { stampTheme } from "./lib/theme";

// theme.js already did this before the first paint; repeating it here keeps the
// document correct even if that file failed to load.
stampTheme();

// A tab open when the daemon is updated keeps running the previous build. Its
// chunk names no longer exist on the server, so the next page it navigates to
// fails to load and it sits there — and until it is reloaded it also talks to
// the new API with the old client, which is how a streamed answer reached a
// version that parsed the whole body at once. Vite reports the failed import;
// reloading picks up the new index.html and the chunk names in it.
//
// Once, and not again for half a minute: if the fetch is failing because the
// server is unreachable rather than because it moved on, a reload would only
// find the cached shell and ask for the same missing chunk.
window.addEventListener("vite:preloadError", (e) => {
  const last = Number(sessionStorage.getItem("islet.reloadedAt") ?? 0);
  if (Date.now() - last < 30_000) return;
  e.preventDefault();
  try { sessionStorage.setItem("islet.reloadedAt", String(Date.now())); } catch { /* private mode */ }
  location.reload();
});

if ("serviceWorker" in navigator && location.protocol === "https:") {
  window.addEventListener("load", () => { navigator.serviceWorker.register("/sw.js").catch(() => {}); });
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      <DialogProvider>
        <AuthProvider>
        <App />
      </AuthProvider>
      </DialogProvider>
    </BrowserRouter>
  </StrictMode>,
);
