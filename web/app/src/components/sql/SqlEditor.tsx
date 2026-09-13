import { useEffect, useRef } from "react";
import { EditorView, basicSetup } from "codemirror";
import { keymap, Decoration, type DecorationSet } from "@codemirror/view";
import { EditorState, Prec, StateEffect, StateField, type Extension } from "@codemirror/state";
import { PostgreSQL, MySQL, sql as sqlLang, type SQLNamespace } from "@codemirror/lang-sql";
import { highlight } from "@/lib/codetheme";

/**
 * The query editor.
 *
 * Highlighting and completion come from @codemirror/lang-sql, which is what a
 * grammar is for. The outline around the statement under the cursor does not:
 * those ranges come from lib/sqlsplit, which agrees with the server byte for
 * byte, so the box is drawn around exactly what Run would send.
 */

/** Where the current statement is, in character offsets. */
export interface Range { from: number; to: number }

const setCurrent = StateEffect.define<Range | null>();

const currentStatement = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(deco, tr) {
    deco = deco.map(tr.changes);
    for (const e of tr.effects) {
      if (!e.is(setCurrent)) continue;
      const r = e.value;
      deco = r && r.to > r.from
        ? Decoration.set([Decoration.mark({ class: "cm-islet-current" }).range(r.from, r.to)])
        : Decoration.none;
    }
    return deco;
  },
  provide: (f) => EditorView.decorations.from(f),
});

// The panel's own tokens, so the editor is not a dark rectangle in a light page.
const theme = EditorView.theme({
  "&": { backgroundColor: "var(--islet-bg)", color: "var(--islet-ink)", fontSize: "13px", height: "100%" },
  "&.cm-focused": { outline: "none" },
  ".cm-scroller": { fontFamily: "Geist Mono, ui-monospace, Menlo, Consolas, monospace", lineHeight: "1.55" },
  ".cm-content": { caretColor: "var(--islet-ink)", padding: "8px 0" },
  ".cm-cursor": { borderLeftColor: "var(--islet-ink)" },
  ".cm-gutters": {
    backgroundColor: "var(--islet-bg)", color: "var(--islet-ink-faint)",
    borderRight: "1px solid var(--islet-border)",
  },
  ".cm-activeLine": { backgroundColor: "color-mix(in srgb, var(--islet-surface-2) 55%, transparent)" },
  ".cm-activeLineGutter": { backgroundColor: "transparent", color: "var(--islet-ink-muted)" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection": {
    backgroundColor: "color-mix(in srgb, var(--islet-accent) 24%, transparent) !important",
  },
  ".cm-matchingBracket": { backgroundColor: "var(--islet-surface-2)", outline: "1px solid var(--islet-border-strong)" },
  // The statement Run would send. A left rule rather than a filled box: a
  // highlight behind text you are reading is noise, an edge is a boundary.
  ".cm-islet-current": {
    backgroundColor: "color-mix(in srgb, var(--islet-accent) 9%, transparent)",
    boxShadow: "inset 2px 0 0 0 var(--islet-accent)",
  },
  ".cm-tooltip": {
    backgroundColor: "var(--islet-surface)", border: "1px solid var(--islet-border)",
    borderRadius: "6px", color: "var(--islet-ink)",
  },
  ".cm-tooltip-autocomplete ul li[aria-selected]": {
    backgroundColor: "var(--islet-surface-2)", color: "var(--islet-ink)",
  },
});

export interface Shortcuts {
  onRunStatement: () => void;
  onRunAll: () => void;
  onSave: () => void;
  onSearchSchema: () => void;
}

interface Props {
  value: string;
  engine: string;
  /** Table and column names for completion, from the introspection cache. */
  schema?: SQLNamespace;
  defaultSchema?: string;
  current: Range | null;
  onChange: (v: string) => void;
  onCursor: (pos: number) => void;
  shortcuts: Shortcuts;
  className?: string;
  style?: React.CSSProperties;
}

export default function SqlEditor({
  value, engine, schema, defaultSchema, current, onChange, onCursor, shortcuts, className = "", style,
}: Props) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  // Held in a ref so rebuilding the editor is not needed when a handler
  // changes, which would throw away the undo history mid-edit.
  const keys = useRef(shortcuts);
  keys.current = shortcuts;
  const emit = useRef({ onChange, onCursor });
  emit.current = { onChange, onCursor };

  useEffect(() => {
    const el = host.current;
    if (!el) return;

    const dialect = engine === "mysql" || engine === "mariadb" ? MySQL : PostgreSQL;
    const extensions: Extension[] = [
      // The panel already ships basicSetup with the file editor: line numbers,
      // history, completion, bracket matching and the standard keymap. Taking
      // it whole keeps this page from adding four CodeMirror packages to do
      // what one already does.
      basicSetup,
      // Highest precedence, or the standard keymap answers Mod-Enter first.
      Prec.highest(keymap.of([
        { key: "Mod-Enter", preventDefault: true, run: () => { keys.current.onRunStatement(); return true; } },
        { key: "Shift-Mod-Enter", preventDefault: true, run: () => { keys.current.onRunAll(); return true; } },
        { key: "Mod-s", preventDefault: true, run: () => { keys.current.onSave(); return true; } },
        { key: "Mod-p", preventDefault: true, run: () => { keys.current.onSearchSchema(); return true; } },
      ])),
      sqlLang({ dialect, schema, defaultSchema, upperCaseKeywords: true }),
      currentStatement,
      theme,
      highlight,
      EditorView.lineWrapping,
      EditorView.updateListener.of((u) => {
        if (u.docChanged) emit.current.onChange(u.state.doc.toString());
        if (u.docChanged || u.selectionSet) emit.current.onCursor(u.state.selection.main.head);
      }),
    ];

    const v = new EditorView({
      state: EditorState.create({ doc: value, extensions }),
      parent: el,
    });
    view.current = v;
    return () => { v.destroy(); view.current = null; };
    // The dialect and the completion schema are structural: changing either
    // rebuilds the editor, which is right, and neither changes while typing.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [engine, schema, defaultSchema]);

  // Text set from outside — history, a saved query, a generated statement.
  useEffect(() => {
    const v = view.current;
    if (!v || v.state.doc.toString() === value) return;
    v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value } });
  }, [value]);

  useEffect(() => {
    view.current?.dispatch({ effects: setCurrent.of(current) });
  }, [current]);

  return <div ref={host} style={style} className={`min-h-0 overflow-hidden ${className}`} />;
}

/** Put the cursor at a character offset and focus, for "open this in the editor". */
export function focusEditor(el: HTMLElement | null) {
  el?.querySelector<HTMLElement>(".cm-content")?.focus();
}
