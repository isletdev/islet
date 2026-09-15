import { type ReactNode } from "react";

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
 * What is supported is what gets used: headings, bold, italic, inline and
 * fenced code, links, bullet and numbered lists, tables, block quotes and
 * rules. Anything unrecognised stays as the text it was, which is the right
 * failure for a renderer that is deliberately partial.
 */
export default function Markdown({ text, className = "" }: { text: string; className?: string }) {
  return <div className={`space-y-2 ${className}`}>{blocks(text)}</div>;
}

function blocks(src: string): ReactNode[] {
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  const out: ReactNode[] = [];
  let i = 0;
  let key = 0;
  while (i < lines.length) {
    const line = lines[i];

    // A fence runs to its closing fence, or to the end when the model stopped
    // mid-block — which happens, and must not swallow the rest as code.
    const fence = /^```(\w+)?\s*$/.exec(line);
    if (fence) {
      const body: string[] = [];
      i++;
      while (i < lines.length && !/^```\s*$/.test(lines[i])) body.push(lines[i++]);
      i++;
      out.push(
        <pre key={key++} className="overflow-x-auto rounded-md border border-border bg-surface-2 p-2.5 font-mono text-[11px] leading-relaxed">
          <code>{body.join("\n")}</code>
        </pre>,
      );
      continue;
    }

    if (/^\s*$/.test(line)) { i++; continue; }

    if (/^---+\s*$/.test(line) || /^\*\*\*+\s*$/.test(line)) {
      out.push(<hr key={key++} className="border-border" />);
      i++;
      continue;
    }

    const head = /^(#{1,4})\s+(.*)$/.exec(line);
    if (head) {
      const level = head[1].length;
      const size = level === 1 ? "text-base" : level === 2 ? "text-sm" : "text-sm";
      out.push(<p key={key++} className={`${size} font-semibold text-ink`}>{inline(head[2])}</p>);
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
        <blockquote key={key++} className="border-l-2 border-border pl-3 text-ink-muted">{blocks(body.join("\n"))}</blockquote>,
      );
      continue;
    }

    const bullet = /^\s*[-*+]\s+/;
    const number = /^\s*\d+[.)]\s+/;
    if (bullet.test(line) || number.test(line)) {
      const ordered = !bullet.test(line);
      const items: ReactNode[] = [];
      const marker = ordered ? number : bullet;
      while (i < lines.length && marker.test(lines[i])) {
        const first = lines[i].replace(marker, "");
        i++;
        // A wrapped item continues on the next line when it is indented and is
        // not itself a new item.
        const cont: string[] = [];
        while (i < lines.length && /^\s+\S/.test(lines[i]) && !bullet.test(lines[i]) && !number.test(lines[i])) {
          cont.push(lines[i++].trim());
        }
        items.push(<li key={items.length} className="ml-4 list-outside">{inline([first, ...cont].join(" "))}</li>);
      }
      out.push(
        ordered
          ? <ol key={key++} className="list-decimal space-y-1">{items}</ol>
          : <ul key={key++} className="list-disc space-y-1">{items}</ul>,
      );
      continue;
    }

    // A paragraph is everything up to a blank line or the start of a block.
    const para: string[] = [];
    while (
      i < lines.length && !/^\s*$/.test(lines[i]) && !/^```/.test(lines[i]) &&
      !/^(#{1,4})\s/.test(lines[i]) && !bullet.test(lines[i]) && !number.test(lines[i]) &&
      !/^\s*>\s?/.test(lines[i]) && !/^---+\s*$/.test(lines[i])
    ) {
      para.push(lines[i++]);
    }
    out.push(<p key={key++} className="whitespace-pre-wrap">{inline(para.join("\n"))}</p>);
  }
  return out;
}

function cells(row: string): string[] {
  return row.trim().replace(/^\|/, "").replace(/\|$/, "").split("|").map((c) => c.trim());
}

// Inline is a single pass so that code spans win over everything inside them:
// `**not bold**` is a literal, which matters when the text being quoted is a
// command or a bit of JSON.
const INLINE = /(`[^`]+`)|(\*\*[^*]+\*\*)|(__[^_]+__)|(\*[^*\n]+\*)|(\[[^\]]+\]\([^)\s]+\))|(https?:\/\/[^\s<>()]+)/g;

function inline(text: string): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  let key = 0;
  for (const m of text.matchAll(INLINE)) {
    const at = m.index ?? 0;
    if (at > last) out.push(text.slice(last, at));
    const tok = m[0];
    if (tok.startsWith("`")) {
      out.push(<code key={key++} className="rounded bg-surface-2 px-1 py-0.5 font-mono text-[0.9em]">{tok.slice(1, -1)}</code>);
    } else if (tok.startsWith("**") || tok.startsWith("__")) {
      out.push(<strong key={key++} className="font-semibold">{tok.slice(2, -2)}</strong>);
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
    <a key={key} href={href} target="_blank" rel="noreferrer noopener" className="underline underline-offset-2 hover:text-ink">
      {label}
    </a>
  );
}
