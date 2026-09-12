// Light or dark, never "follow the system". The operating system only decides
// which one a brand-new browser starts on; after that the choice is the user's
// and it sticks.
export type Theme = "light" | "dark";
const KEY = "islet.theme";

function prefersDark(): boolean {
  try {
    return window.matchMedia("(prefers-color-scheme: dark)").matches;
  } catch {
    return false;
  }
}

export function getTheme(): Theme {
  try {
    const t = localStorage.getItem(KEY);
    if (t === "light" || t === "dark") return t;
  } catch { /* storage may be unavailable */ }
  return prefersDark() ? "dark" : "light";
}

export function setTheme(t: Theme) {
  try { localStorage.setItem(KEY, t); } catch { /* ignore */ }
  document.documentElement.dataset.theme = t;
}

/** Put the current theme on <html> without recording a choice the user has not made. */
export function stampTheme(): Theme {
  const t = getTheme();
  document.documentElement.dataset.theme = t;
  return t;
}

export function otherTheme(current: Theme): Theme {
  return current === "dark" ? "light" : "dark";
}
