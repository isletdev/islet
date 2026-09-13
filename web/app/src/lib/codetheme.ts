import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { tags as t } from "@lezer/highlight";

/**
 * Syntax colours for every editor in the panel.
 *
 * CodeMirror's stock highlight style is a fixed light palette: dark red
 * comments and dark blue keywords, which on the dark theme's near-black ground
 * are barely readable. These roles map onto the panel's own semantic tokens
 * instead, so each colour is the one the brand already guarantees is AA on the
 * surface behind it, in whichever theme the reader is in.
 *
 * The palette is deliberately small. A black-and-white panel does not want a
 * rainbow in the middle of it: comments recede, strings and numbers are the
 * literal values, keywords carry weight, and everything else is plain ink.
 */
export const highlight = syntaxHighlighting(HighlightStyle.define([
  { tag: [t.comment, t.lineComment, t.blockComment, t.docComment], color: "var(--islet-ink-muted)", fontStyle: "italic" },
  { tag: [t.keyword, t.controlKeyword, t.moduleKeyword, t.operatorKeyword], color: "var(--islet-accent-strong)", fontWeight: "500" },
  { tag: [t.string, t.special(t.string), t.regexp], color: "var(--islet-success)" },
  { tag: [t.number, t.bool, t.null, t.atom, t.literal], color: "var(--islet-warning)" },
  { tag: [t.typeName, t.className, t.namespace, t.tagName], color: "var(--islet-accent)" },
  { tag: [t.function(t.variableName), t.function(t.propertyName), t.macroName], color: "var(--islet-ink)", fontWeight: "500" },
  { tag: [t.variableName, t.propertyName, t.attributeName], color: "var(--islet-ink)" },
  { tag: [t.definition(t.variableName), t.definition(t.propertyName)], color: "var(--islet-ink)", fontWeight: "500" },
  { tag: [t.operator, t.punctuation, t.separator, t.bracket], color: "var(--islet-ink-muted)" },
  { tag: [t.meta, t.processingInstruction], color: "var(--islet-ink-muted)" },
  { tag: t.link, color: "var(--islet-accent)", textDecoration: "underline" },
  { tag: [t.heading, t.strong], color: "var(--islet-ink)", fontWeight: "600" },
  { tag: t.emphasis, fontStyle: "italic" },
  { tag: t.strikethrough, textDecoration: "line-through" },
  { tag: t.invalid, color: "var(--islet-danger)" },
]));
