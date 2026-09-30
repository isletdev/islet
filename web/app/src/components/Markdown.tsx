import { type ReactNode } from "react";

import CodeBlock from "@/components/CodeBlock";
import { CheckIcon } from "@/components/icons";

/**
 * The small part of Markdown a model actually writes, rendered as React nodes.
 *
 * Written here rather than taken from a library for two reasons. The panel's
 * components are its own and its dependency list is deliberately short; and
 * more to the point, this text is not trusted — it is a model's prose with tool
 * output quoted inside it, which means whatever was in a container's
 * environment or a file it read. Building React nodes rather than HTML means
 * there is no innerHTML anywhere in the path and nothing to escape correctly:
 * the only way to produce an element here is for this file to have decided to.
 *
 * What is supported is what gets used: headings, bold, italic, strikethrough,
 * inline and fenced code, links, bullet and numbered lists — nested, and with
 * task boxes — tables, block quotes and rules. Anything unrecognised stays as
 * the text it was, which is the right failure for a renderer that is
 * deliberately partial.
 */
export default function Markdown({ text, className = "" }: { text: string; className?: string }) {
  return <div className={`space-y-3 ${className}`}>{blocks(text)}</div>;
}

/** A list item at any depth: its indent, its marker, and what it says. */
const ITEM = /^([ \t]*)([-*+]|\d+[.)])[ \t]+(.*)$/;
const TASK = /^\[([ xX])\][ \t]+/;

function blocks(src: string): ReactNode[] {
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  const out: ReactNode[] = [];
  let i = 0;
  let key = 0;
  while (i < lines.length) {
    const line = lines[i];

    // A fence runs to its closing fence, or to the end when the model stopped
    // mid-block — which happens, and must not swallow the rest as code.
    const fence = /^\s*```+\s*([\w.+-]+)?\s*$/.exec(line);
    if (fence) {
      const body: string[] = [];
      i++;
      while (i < lines.length && !/^\s*```+\s*$/.test(lines[i])) body.push(lines[i++]);
      i++;
      out.push(<CodeBlock key={key++} code={body.join("\n")} lang={fence[1]} />);
      continue;
    }

    if (/^\s*$/.test(line)) { i++; continue; }

    if (/^\s*(---+|\*\*\*+|___+)\s*$/.test(line)) {
      out.push(<hr key={key++} className="border-border" />);
      i++;
      continue;
    }

    const head = /^(#{1,6})\s+(.*)$/.exec(line);
    if (head) {
      out.push(heading(head[1].length, head[2], key++));
      i++;
      continue;
    }

    // A table needs its separator row; without one these are just pipes in a
    // sentence.
    if (line.trim().startsWith("|") && i + 1 < lines.length && /^\s*\|?[\s:|-]+\|[\s:|-]*$/.test(lines[i + 1])) {
      const header = cells(line);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && lines[i].trim().startsWith("|")) rows.push(cells(lines[i++]));
      out.push(
        <div key={key++} className="overflow-x-auto">
          <table className="min-w-[20rem] border-collapse text-left text-xs">
            <thead>
              <tr>{header.map((h, n) => <th key={n} className="border-b border-border px-2 py-1.5 font-medium text-ink">{inline(h)}</th>)}</tr>
            </thead>
            <tbody>
              {rows.map((r, n) => (
                <tr key={n}>{r.map((c, m) => <td key={m} className="border-b border-border/60 px-2 py-1.5 align-top text-ink-muted">{inline(c)}</td>)}</tr>
              ))}
            </tbody>
          </table>
        </div>,
      );
      continue;
    }

    if (/^\s*>\s?/.test(line)) {
      const body: string[] = [];
      while (i < lines.length && /^\s*>\s?/.test(lines[i])) body.push(lines[i++].replace(/^\s*>\s?/, ""));
      out.push(
        <blockquote key={key++} className="space-y-1.5 border-l-2 border-border pl-3 text-ink-muted">{blocks(body.join("\n"))}</blockquote>,
      );
      continue;
    }

    if (ITEM.test(line)) {
      const [node, next] = list(lines, i, key++);
      out.push(node);
      i = next;
      continue;
    }

    // A paragraph is everything up to a blank line or the start of a block.
    const para: string[] = [];
    while (
      i < lines.length && !/^\s*$/.test(lines[i]) && !/^\s*```/.test(lines[i]) &&
      !/^(#{1,6})\s/.test(lines[i]) && !ITEM.test(lines[i]) &&
      !/^\s*>\s?/.test(lines[i]) && !/^\s*(---+|\*\*\*+|___+)\s*$/.test(lines[i])
    ) {
      para.push(lines[i++]);
    }
    out.push(<p key={key++} className="whitespace-pre-wrap">{inline(para.join("\n"))}</p>);
  }
  return out;
}

// Real heading elements, because an answer with sections is something a screen
// reader should be able to move through. The page owns h1 and h2, so a model's
// top level starts at h3 — and the scale is weight and a step of size, not four
// sizes nobody can tell apart.
function heading(level: number, text: string, key: number): ReactNode {
  const body = inline(text);
  if (level === 1) return <h3 key={key} className="text-base font-semibold text-ink">{body}</h3>;
  if (level === 2) return <h4 key={key} className="text-[0.9375rem] font-semibold text-ink">{body}</h4>;
  if (level === 3) return <h5 key={key} className="text-sm font-semibold text-ink">{body}</h5>;
  return <h6 key={key} className="text-sm font-semibold text-ink-muted">{body}</h6>;
}

/**
 * What kind of list an item starts: numbered, a task box, or an ordinary
 * bullet. Two lists of different kinds that touch are two lists — a bullet run
 * followed by a task run followed by a numbered one used to come out as one
 * `<ul>`, with the numbers gone and the boxes rendered as literal `[ ]`.
 */
function kindOf(line: string): "ordered" | "task" | "bullet" | "" {
  const m = ITEM.exec(line);
  if (!m) return "";
  if (/^\d/.test(m[2])) return "ordered";
  return TASK.test(m[3]) ? "task" : "bullet";
}

/**
 * One list, and everything nested under it.
 *
 * Indentation decides depth: an item indented past the one above it starts a
 * sub-list, and a line indented past a marker without one of its own is that
 * item continuing. Both are ordinary in what a model writes, and flattening
 * them — which is what this did before — turns a structure into a wall.
 */
function list(lines: string[], start: number, key: number): [ReactNode, number] {
  const base = (ITEM.exec(lines[start]) as RegExpExecArray)[1].length;
  const kind = kindOf(lines[start]);
  // A nested list is whatever it likes; only items at this list's own indent
  // have to agree with it about what kind of list this is.
  const belongs = (line: string) => {
    const m = ITEM.exec(line);
    if (!m) return false;
    return m[1].length > base || kindOf(line) === kind;
  };
  let end = start;
  while (end < lines.length) {
    const line = lines[end];
    if (/^\s*$/.test(line)) {
      // A blank line keeps the list open only when another item of the same
      // kind follows it.
      let k = end + 1;
      while (k < lines.length && /^\s*$/.test(lines[k])) k++;
      const m = k < lines.length ? ITEM.exec(lines[k]) : null;
      if (m && m[1].length >= base && belongs(lines[k])) { end = k; continue; }
      break;
    }
    const m = ITEM.exec(line);
    if (m) {
      if (m[1].length < base || !belongs(line)) break;
      end++;
      continue;
    }
    if (line.search(/\S/) > base) { end++; continue; }
    break;
  }

  const chunk = lines.slice(start, end);
  const items: string[][] = [];
  for (const line of chunk) {
    const m = ITEM.exec(line);
    if (m && m[1].length === base) items.push([m[3]]);
    else if (items.length) items[items.length - 1].push(line);
    // A line before the first item cannot happen: the run starts on one.
  }

  const ordered = kind === "ordered";
  const tasks = kind === "task";
  const nodes = items.map((it, n) => item(dedent(it), n, tasks));
  if (tasks) return [<ul key={key} className="space-y-1">{nodes}</ul>, end];
  return [
    ordered
      ? <ol key={key} className="ml-5 list-outside list-decimal space-y-1">{nodes}</ol>
      : <ul key={key} className="ml-5 list-outside list-disc space-y-1">{nodes}</ul>,
    end,
  ];
}

// The item's own text keeps its indent stripped by the marker; its body lines
// are brought back to column zero so a nested list parses as a list of its own.
function dedent(lines: string[]): string[] {
  const body = lines.slice(1);
  const indents = body.filter((l) => l.trim() !== "").map((l) => l.search(/\S/));
  const cut = indents.length ? Math.min(...indents) : 0;
  return [lines[0], ...body.map((l) => (l.trim() === "" ? "" : l.slice(cut)))];
}

function item(content: string[], key: number, task: boolean): ReactNode {
  if (task) {
    const done = /^\[[xX]\]/.test(content[0]);
    const rest = [content[0].replace(TASK, ""), ...content.slice(1)];
    return (
      <li key={key} className="flex list-none items-start gap-2">
        <span
          aria-hidden="true"
          className={`mt-[0.2rem] flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-[4px] border ${done ? "border-success bg-success/10 text-success" : "border-border-strong"}`}
        >
          {done ? <CheckIcon className="h-2.5 w-2.5" /> : null}
        </span>
        <span className={`min-w-0 flex-1 ${done ? "text-ink-muted line-through" : ""}`}>
          <span className="sr-only">{done ? "Done: " : "Not done: "}</span>
          {body(rest)}
        </span>
      </li>
    );
  }
  return <li key={key}>{body(content)}</li>;
}

// A plain item is one line of prose; an item with a nested list, a fence or a
// paragraph of its own is a small document and goes back through blocks().
function body(content: string[]): ReactNode {
  const structured = content.slice(1).some((l) => ITEM.test(l) || /^\s*```/.test(l) || /^\s*$/.test(l));
  if (!structured) return inline(content.join(" ").trim());
  return <div className="space-y-1.5">{blocks(content.join("\n"))}</div>;
}

function cells(row: string): string[] {
  return row.trim().replace(/^\|/, "").replace(/\|$/, "").split("|").map((c) => c.trim());
}

// Inline is a single pass so that code spans win over everything inside them:
// `**not bold**` is a literal, which matters when the text being quoted is a
// command or a bit of JSON.
const INLINE = /(`[^`]+`)|(\*\*[^*]+\*\*)|(__[^_]+__)|(~~[^~]+~~)|(\*[^*\n]+\*)|(\[[^\]]+\]\([^)\s]+\))|(https?:\/\/[^\s<>()]+)/g;

function inline(text: string): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  let key = 0;
  for (const m of text.matchAll(INLINE)) {
    const at = m.index ?? 0;
    if (at > last) out.push(text.slice(last, at));
    const tok = m[0];
    if (tok.startsWith("`")) {
      out.push(<code key={key++} className="rounded bg-surface-2 px-1 py-0.5 font-mono text-[0.9em] text-ink">{tok.slice(1, -1)}</code>);
    } else if (tok.startsWith("**") || tok.startsWith("__")) {
      out.push(<strong key={key++} className="font-semibold text-ink">{tok.slice(2, -2)}</strong>);
    } else if (tok.startsWith("~~")) {
      out.push(<s key={key++} className="text-ink-muted">{tok.slice(2, -2)}</s>);
    } else if (tok.startsWith("[")) {
      const cut = tok.indexOf("](");
      out.push(link(tok.slice(cut + 2, -1), tok.slice(1, cut), key++));
    } else if (tok.startsWith("http")) {
      out.push(link(tok, tok, key++));
    } else {
      out.push(<em key={key++}>{tok.slice(1, -1)}</em>);
    }
    last = at + tok.length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

// Only schemes that are safe to hand a browser. A model writing
// javascript:… into a link is not a scenario to leave to chance, and neither
// is tool output that quotes one.
function link(href: string, label: string, key: number): ReactNode {
  if (!/^https?:\/\//i.test(href) && !/^mailto:/i.test(href)) return <span key={key}>{label}</span>;
  return (
    <a key={key} href={href} target="_blank" rel="noreferrer noopener" className="-my-1 py-1 text-accent underline underline-offset-2 hover:text-accent-strong">
      {label}
    </a>
  );
}
