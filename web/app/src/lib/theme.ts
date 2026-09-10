export type Theme = "system" | "light" | "dark";
const KEY = "islet.theme";

export function getTheme(): Theme {
  try {
    const t = localStorage.getItem(KEY);
    if (t === "light" || t === "dark") return t;
  } catch { /* storage may be unavailable */ }
  return "system";
}

export function setTheme(t: Theme) {
  try {
    if (t === "system") localStorage.removeItem(KEY); else localStorage.setItem(KEY, t);
  } catch { /* ignore */ }
  if (t === "system") delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = t;
}

export function cycleTheme(current: Theme): Theme {
  return current === "system" ? "light" : current === "light" ? "dark" : "system";
}
