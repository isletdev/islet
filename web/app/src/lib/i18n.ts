// The panel is English. `t("key")` reads src/locales/en.json and substitutes
// {placeholders}, which keeps user-facing strings in one file and out of the
// components.
//
// Other dictionaries exist under src/locales but are not shipped: the
// translations were not good enough to put in front of anyone, and a half
// translated panel is worse than an English one. To bring a language back,
// import its file into `dicts` and add a picker.
import en from "@/locales/en.json";

const dict: Record<string, string> = en;

/** Translate a key; an unknown key renders as itself, which is loud in review. */
export function t(key: string, vars?: Record<string, string | number>): string {
  let s = dict[key] ?? key;
  if (vars) for (const [k, v] of Object.entries(vars)) s = s.replaceAll(`{${k}}`, String(v));
  return s;
}

document.documentElement.lang = "en";
