import { useEffect, useRef } from "react";
import { EditorView, basicSetup } from "codemirror";
import { EditorState, type Extension } from "@codemirror/state";
import { keymap } from "@codemirror/view";
import { StreamLanguage } from "@codemirror/language";
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

const theme = EditorView.theme({
  "&": { backgroundColor: "#0A0A0A", color: "#FAFAFA", fontSize: "13px", height: "100%" },
  ".cm-scroller": { fontFamily: "Geist Mono, ui-monospace, Menlo, Consolas, monospace", lineHeight: "1.5" },
  ".cm-content": { caretColor: "#FAFAFA" },
  ".cm-cursor": { borderLeftColor: "#FAFAFA" },
  ".cm-gutters": { backgroundColor: "#0A0A0A", color: "#6B6B6B", borderRight: "1px solid #262626" },
  ".cm-activeLine": { backgroundColor: "#141414" },
  ".cm-activeLineGutter": { backgroundColor: "#141414" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground": { backgroundColor: "#2A2A2A !important" },
  ".cm-matchingBracket": { backgroundColor: "#262626", outline: "1px solid #4A4A4A" },
}, { dark: true });

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
