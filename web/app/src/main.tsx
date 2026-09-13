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
