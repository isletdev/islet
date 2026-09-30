import { useEffect, useRef, useState } from "react";

import { CheckIcon, CopyIcon } from "@/components/icons";
import { ROLE_CLASS, known, normaliseLang, tokenize } from "@/lib/highlight";

/**
 * A fenced code block: what it is, what it says, and a button that takes it.
 *
 * The header exists because a model writes code to be used. Everyone reaches for
 * a copy button without looking for it, so it is always drawn rather than
 * revealed on hover — a phone has no hover, and a block of shell is exactly what
 * somebody wants to copy from a phone.
 */

/** What the label says, where the fence's word is not what people call it. */
const LABELS: Record<string, string> = {
  sh: "shell", bash: "bash", zsh: "zsh", console: "shell", terminal: "shell",
  js: "JavaScript", jsx: "JSX", ts: "TypeScript", tsx: "TSX", json: "JSON",
  go: "Go", golang: "Go", sql: "SQL", postgres: "SQL", postgresql: "SQL", mysql: "SQL",
  yaml: "YAML", yml: "YAML", compose: "compose", dockerfile: "Dockerfile",
  ini: "ini", env: ".env", dotenv: ".env", toml: "TOML", conf: "conf",
  python: "Python", py: "Python", http: "HTTP", nginx: "nginx", diff: "diff", patch: "diff",
  html: "HTML", css: "CSS", md: "Markdown", markdown: "Markdown", text: "text", txt: "text",
};

export default function CodeBlock({ code, lang }: { code: string; lang?: string }) {
  const [state, setState] = useState<"idle" | "copied" | "refused">("idle");
  const timer = useRef<number | undefined>(undefined);
  const pre = useRef<HTMLPreElement>(null);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  const l = normaliseLang(lang);
  const label = LABELS[l] ?? (l || "text");
  const tokens = known(l) ? tokenize(code, l) : null;

  const settle = (to: "copied" | "refused") => {
    setState(to);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setState("idle"), to === "copied" ? 1600 : 6000);
  };

  // A browser refuses the clipboard more often than it looks: the API does not
  // exist outside a secure context, so a panel reached over plain HTTP on a LAN
  // address has none of it. Selecting the block and saying which keys to press
  // is the whole of the fallback, and saying nothing is not an option — the
  // terminal's copy button has said so since v0.9.2.
  const refused = () => {
    const node = pre.current;
    const sel = window.getSelection();
    if (node && sel) {
      const range = document.createRange();
      range.selectNodeContents(node);
      sel.removeAllRanges();
      sel.addRange(range);
    }
    settle("refused");
  };

  const copy = () => {
    if (!navigator.clipboard?.writeText) { refused(); return; }
    void navigator.clipboard.writeText(code).then(() => settle("copied")).catch(refused);
  };

  return (
    <div className="overflow-hidden rounded-lg border border-border bg-code-bg">
      <div className="flex items-center justify-between gap-2 border-b border-border/70 px-2.5 py-1">
        <span className="truncate font-mono text-[11px] text-ink-faint">{label}</span>
        <button
          type="button"
          onClick={copy}
          aria-label={state === "copied" ? "Copied" : state === "refused" ? "The browser refused the clipboard; the code is selected, press Ctrl+C" : "Copy this code"}
          title={state === "refused" ? "The browser would not let the page write to the clipboard, so the code is selected instead. Serving the panel over HTTPS fixes it." : undefined}
          className="-my-1 flex shrink-0 items-center gap-1 rounded-md px-1.5 py-1 text-[11px] text-ink-faint transition-colors hover:bg-surface-2 hover:text-ink"
        >
          {state === "copied"
            ? <CheckIcon className="h-3.5 w-3.5 text-success" />
            : <CopyIcon className="h-3.5 w-3.5" />}
          {state === "copied" ? "Copied" : state === "refused" ? "Press Ctrl+C" : "Copy"}
        </button>
      </div>
      <pre ref={pre} className="overflow-x-auto p-3 font-mono text-xs leading-relaxed text-code-fg">
        <code>
          {tokens
            ? tokens.map((t, i) => (
                t.role === "plain"
                  ? t.text
                  : <span key={i} className={ROLE_CLASS[t.role]}>{t.text}</span>
              ))
            : code}
        </code>
      </pre>
    </div>
  );
}
