// Tiny i18n: a dictionary per language, `t("key")` with {placeholders},
// English as the fallback for anything untranslated. Languages live in
// src/locales/<code>.json; adding one is adding a file and a line below.
import { useSyncExternalStore } from "react";
import en from "@/locales/en.json";
import de from "@/locales/de.json";
import mk from "@/locales/mk.json";

export type Lang = "en" | "de" | "mk";
export const LANGS: { code: Lang; label: string }[] = [
  { code: "en", label: "English" },
  { code: "de", label: "Deutsch" },
  { code: "mk", label: "Македонски" },
];
const dicts: Record<Lang, Record<string, string>> = { en, de, mk };
const KEY = "islet.lang";

let current: Lang = detect();
const listeners = new Set<() => void>();

function detect(): Lang {
  try {
    const saved = localStorage.getItem(KEY);
    if (saved === "en" || saved === "de" || saved === "mk") return saved;
  } catch { /* storage may be unavailable */ }
  const nav = (navigator.language || "en").slice(0, 2);
  return nav === "de" || nav === "mk" ? nav : "en";
}

export function getLang(): Lang { return current; }

export function setLang(l: Lang) {
  current = l;
  try { if (l === "en") localStorage.removeItem(KEY); else localStorage.setItem(KEY, l); } catch { /* ignore */ }
  document.documentElement.lang = l;
  listeners.forEach((fn) => fn());
}

/** Translate a key; unknown keys fall back to English, then to the key itself. */
export function t(key: string, vars?: Record<string, string | number>): string {
  let s = dicts[current][key] ?? dicts.en[key] ?? key;
  if (vars) for (const [k, v] of Object.entries(vars)) s = s.replaceAll(`{${k}}`, String(v));
  return s;
}

/** Re-renders the component when the language changes. */
export function useLang(): Lang {
  return useSyncExternalStore((cb) => { listeners.add(cb); return () => listeners.delete(cb); }, () => current, () => current);
}

/** How complete a dictionary is, for the language picker. */
export function coverage(l: Lang): number {
  const total = Object.keys(dicts.en).length;
  if (l === "en" || total === 0) return 100;
  const done = Object.keys(dicts.en).filter((k) => dicts[l][k]).length;
  return Math.round((done / total) * 100);
}

document.documentElement.lang = current;
