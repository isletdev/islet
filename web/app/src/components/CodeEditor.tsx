import { useEffect, useRef } from "react";
import { EditorView, basicSetup } from "codemirror";
import { EditorState, type Extension } from "@codemirror/state";
import { keymap } from "@codemirror/view";
import { StreamLanguage } from "@codemirror/language";
import { highlight } from "@/lib/codetheme";
import { yaml } from "@codemirror/lang-yaml";
import { json } from "@codemirror/lang-json";
import { javascript } from "@codemirror/lang-javascript";
import { python } from "@codemirror/lang-python";
import { markdown } from "@codemirror/lang-markdown";
import { shell } from "@codemirror/legacy-modes/mode/shell";
import { nginx } from "@codemirror/legacy-modes/mode/nginx";
import { toml } from "@codemirror/legacy-modes/mode/toml";
import { properties } from "@codemirror/legacy-modes/mode/properties";

function languageFor(name: string): Extension {
  const n = name.toLowerCase();
  const ext = n.includes(".") ? n.slice(n.lastIndexOf(".") + 1) : "";
  if (["yml", "yaml"].includes(ext)) return yaml();
  if (ext === "json") return json();
  if (["js", "mjs", "cjs", "jsx"].includes(ext)) return javascript({ jsx: true });
  if (["ts", "tsx", "mts"].includes(ext)) return javascript({ jsx: true, typescript: true });
  if (ext === "py") return python();
  if (["md", "markdown"].includes(ext)) return markdown();
  if (["sh", "bash", "zsh"].includes(ext) || n === "dockerfile" || n.startsWith(".bashrc") || n.startsWith(".profile") || n.endsWith("rc")) return StreamLanguage.define(shell);
  if (ext === "conf" && n.includes("nginx")) return StreamLanguage.define(nginx);
  if (ext === "toml") return StreamLanguage.define(toml);
  if (["env", "ini", "properties", "service", "conf", "cfg"].includes(ext) || n === ".env") return StreamLanguage.define(properties);
  return [];
}

// The panel's own tokens, so the editor is not a dark rectangle in a light
// page. A log pane stays dark in both themes — a console is a console — but
// this is a document somebody is writing, and it follows the page.
const theme = EditorView.theme({
  "&": { backgroundColor: "var(--islet-bg)", color: "var(--islet-ink)", fontSize: "13px", height: "100%" },
  ".cm-scroller": { fontFamily: "Geist Mono, ui-monospace, Menlo, Consolas, monospace", lineHeight: "1.5" },
  ".cm-content": { caretColor: "var(--islet-ink)" },
  ".cm-cursor": { borderLeftColor: "var(--islet-ink)" },
  ".cm-gutters": {
    backgroundColor: "var(--islet-bg)", color: "var(--islet-ink-faint)",
    borderRight: "1px solid var(--islet-border)",
  },
  ".cm-activeLine": { backgroundColor: "color-mix(in srgb, var(--islet-surface-2) 55%, transparent)" },
  ".cm-activeLineGutter": { backgroundColor: "transparent", color: "var(--islet-ink-muted)" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground": {
    backgroundColor: "color-mix(in srgb, var(--islet-accent) 24%, transparent) !important",
  },
  ".cm-matchingBracket": { backgroundColor: "var(--islet-surface-2)", outline: "1px solid var(--islet-border-strong)" },
  ".cm-tooltip": {
    backgroundColor: "var(--islet-surface)", border: "1px solid var(--islet-border)",
    borderRadius: "6px", color: "var(--islet-ink)",
  },
  ".cm-panels": { backgroundColor: "var(--islet-surface)", color: "var(--islet-ink)" },
});

interface Props {
  name: string;
  value: string;
  onChange: (v: string) => void;
  onSave: () => void;
  className?: string;
}

/** CodeMirror 6 editor with language by file name and Ctrl/Cmd+S to save. */
export default function CodeEditor({ name, value, onChange, onSave, className = "" }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const saveRef = useRef(onSave);
  saveRef.current = onSave;
  const changeRef = useRef(onChange);
  changeRef.current = onChange;

  useEffect(() => {
    if (!host.current) return;
    const state = EditorState.create({
      doc: value,
      extensions: [
        basicSetup,
        theme,
        highlight,
        languageFor(name),
        keymap.of([{ key: "Mod-s", run: () => { saveRef.current(); return true; } }]),
        EditorView.updateListener.of((u) => { if (u.docChanged) changeRef.current(u.state.doc.toString()); }),
      ],
    });
    const v = new EditorView({ state, parent: host.current });
    view.current = v;
    return () => { v.destroy(); view.current = null; };
    // The document is created once; `name` changes the language and remounts.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name]);

  // Text set from outside: a cron template, an older version of a script, a
  // shebang picked from the dropdown. Without this the editor kept whatever it
  // was created with and those controls silently did nothing.
  //
  // The comparison is what makes it safe to run on every render. Typing sends
  // the document up through onChange and it arrives back here identical, so
  // there is nothing to dispatch and the cursor is never disturbed.
  useEffect(() => {
    const v = view.current;
    if (!v || v.state.doc.toString() === value) return;
    v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value } });
  }, [value]);

  return <div ref={host} className={`overflow-hidden rounded-lg border border-border ${className}`} />;
}
