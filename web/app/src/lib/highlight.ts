/**
 * A deliberately partial syntax highlighter for read-only code.
 *
 * The editors in this panel are CodeMirror and get their colours from
 * `codetheme.ts`. A model's answer is not an editor: the code in it is a few
 * lines inside prose, and standing up a CodeMirror instance per fenced block —
 * with its parsers — to colour six lines of shell is the wrong trade on a
 * 1 vCPU box serving a bundle it also has to download.
 *
 * So this is a scanner rather than a parser, and it colours only what it is sure
 * of: comments, strings, numbers, keywords and a few well-known type names. The
 * roles map onto the same semantic tokens `codetheme.ts` uses, so a Go snippet
 * in an answer looks like the same Go in the file editor. A language it does not
 * know is returned as one plain run, which is the right failure — uncoloured
 * code reads perfectly well, mis-coloured code does not.
 *
 * It produces text, never markup: the caller wraps each run in a `<span>`. There
 * is no innerHTML in this path, for the same reason `Markdown.tsx` has none.
 */

export type Role = "plain" | "comment" | "string" | "number" | "keyword" | "type" | "meta" | "add" | "del";

export type Token = { text: string; role: Role };

/** Tailwind classes per role, matching the editors' palette. */
export const ROLE_CLASS: Record<Role, string> = {
  plain: "",
  comment: "text-ink-muted italic",
  string: "text-success",
  number: "text-warning",
  keyword: "font-medium text-accent-strong",
  type: "text-accent",
  meta: "text-ink-muted",
  add: "text-success",
  del: "text-danger",
};

const words = (s: string) => new Set(s.split(/\s+/).filter(Boolean));

type Spec = {
  line?: string[];
  block?: [string, string];
  quotes?: string[];
  keywords?: Set<string>;
  types?: Set<string>;
  /** `name:` or `name =` at the start of a line is a key, not a word. */
  keys?: RegExp;
  /** Directives recognised only in capitals at the start of a line. */
  directives?: Set<string>;
};

const SHELL: Spec = {
  line: ["#"],
  quotes: ['"', "'", "`"],
  keywords: words(`if then elif else fi for while until do done case esac in function select
    return export local readonly declare unset set shift source exit trap eval exec`),
  types: words("sudo cd echo printf test true false"),
};

const JS: Spec = {
  line: ["//"],
  block: ["/*", "*/"],
  quotes: ['"', "'", "`"],
  keywords: words(`const let var function return if else for while do break continue switch case
    default class extends new this super try catch finally throw typeof instanceof delete void
    async await yield import export from as of in static get set null undefined true false`),
  types: words("string number boolean any unknown never void object symbol bigint Promise Record Array"),
};

const GO: Spec = {
  line: ["//"],
  block: ["/*", "*/"],
  quotes: ['"', "`", "'"],
  keywords: words(`package import func var const type struct interface map chan go defer select
    if else for range return switch case default break continue fallthrough goto nil true false`),
  types: words(`string bool byte rune error int int8 int16 int32 int64 uint uint8 uint16 uint32
    uint64 uintptr float32 float64 complex64 complex128 any`),
};

const SQL: Spec = {
  line: ["--"],
  block: ["/*", "*/"],
  quotes: ["'", '"'],
  keywords: words(`select from where insert into values update set delete create alter drop table
    view index unique primary key foreign references constraint join left right full inner outer
    cross on using group order by asc desc limit offset having union all distinct as and or not
    null is like ilike between in exists case when then else end begin commit rollback explain
    analyze vacuum with returning default cascade add column if truncate grant revoke`),
  types: words(`text integer int bigint smallint serial boolean date timestamp timestamptz numeric
    decimal real double jsonb json uuid varchar char bytea interval`),
};

const YAML: Spec = {
  line: ["#"],
  quotes: ['"', "'"],
  keywords: words("true false null yes no on off"),
  keys: /^[ \t-]*[\w.\/-]+(?=\s*:)/,
};

const INI: Spec = {
  line: ["#", ";"],
  quotes: ['"', "'"],
  keywords: words("true false yes no on off"),
  keys: /^\s*[\w.-]+(?=\s*=)/,
};

const SPECS: Record<string, Spec> = {
  bash: SHELL, sh: SHELL, shell: SHELL, zsh: SHELL, console: SHELL, terminal: SHELL,
  js: JS, jsx: JS, javascript: JS, ts: JS, tsx: JS, typescript: JS, json: {
    quotes: ['"'], keywords: words("true false null"),
  },
  go: GO, golang: GO,
  sql: SQL, postgres: SQL, postgresql: SQL, mysql: SQL,
  yaml: YAML, yml: YAML, compose: YAML,
  ini: INI, conf: INI, env: INI, dotenv: INI, toml: INI, properties: INI,
  dockerfile: {
    line: ["#"],
    quotes: ['"', "'"],
    directives: words(`FROM RUN CMD LABEL MAINTAINER EXPOSE ENV ADD COPY ENTRYPOINT VOLUME USER
      WORKDIR ARG ONBUILD STOPSIGNAL HEALTHCHECK SHELL AS`),
  },
  python: {
    line: ["#"],
    quotes: ['"', "'"],
    keywords: words(`def class return if elif else for while break continue import from as with
      try except finally raise lambda yield global nonlocal pass assert del in is not and or
      None True False async await`),
    types: words("str int float bool list dict set tuple bytes object"),
  },
  http: { keys: /^[\w-]+(?=\s*:)/, keywords: words("GET POST PUT PATCH DELETE HEAD OPTIONS HTTP/1.1 HTTP/2") },
  nginx: { line: ["#"], quotes: ['"', "'"], keywords: words("server location listen upstream proxy_pass root index include return rewrite if set") },
};

/** The alias a fence carries, reduced to a spec key. */
export function normaliseLang(lang: string | undefined): string {
  return (lang ?? "").trim().toLowerCase();
}

/** Whether a fence label names something this file can colour. */
export function known(lang: string | undefined): boolean {
  const l = normaliseLang(lang);
  return l === "diff" || l === "patch" || l in SPECS;
}

export function tokenize(code: string, lang: string | undefined): Token[] {
  const l = normaliseLang(lang);
  if (l === "diff" || l === "patch") return diff(code);
  const spec = SPECS[l];
  if (!spec) return [{ text: code, role: "plain" }];
  return scan(code, spec);
}

// A diff is coloured by line, because that is what a diff means. Its own
// markers win over anything inside the line.
function diff(code: string): Token[] {
  const out: Token[] = [];
  for (const line of code.split("\n")) {
    const role: Role =
      line.startsWith("+++") || line.startsWith("---") || line.startsWith("@@") || line.startsWith("diff ")
        ? "meta"
        : line.startsWith("+") ? "add" : line.startsWith("-") ? "del" : "plain";
    out.push({ text: line, role });
    out.push({ text: "\n", role: "plain" });
  }
  out.pop();
  return out;
}

const WORD_START = /[A-Za-z_$@]/;
const WORD_REST = /[\w$-]/;

function scan(src: string, s: Spec): Token[] {
  const out: Token[] = [];
  let plain = "";
  const flush = () => { if (plain) { out.push({ text: plain, role: "plain" }); plain = ""; } };
  const push = (text: string, role: Role) => { flush(); out.push({ text, role }); };
  let i = 0;
  let lineStart = 0;
  while (i < src.length) {
    const ch = src[i];

    if (ch === "\n") { plain += ch; i++; lineStart = i; continue; }

    // A key is only a key at the start of its line, so it is tested there and
    // against that line alone.
    if (s.keys && i === lineStart) {
      const nl = src.indexOf("\n", i);
      const m = s.keys.exec(src.slice(i, nl === -1 ? src.length : nl));
      if (m && m.index === 0 && m[0]) { push(m[0], "type"); i += m[0].length; continue; }
    }

    const lc = s.line?.find((p) => src.startsWith(p, i));
    if (lc) {
      const nl = src.indexOf("\n", i);
      const stop = nl === -1 ? src.length : nl;
      push(src.slice(i, stop), "comment");
      i = stop;
      continue;
    }

    if (s.block && src.startsWith(s.block[0], i)) {
      const close = src.indexOf(s.block[1], i + s.block[0].length);
      const stop = close === -1 ? src.length : close + s.block[1].length;
      push(src.slice(i, stop), "comment");
      i = stop;
      continue;
    }

    if (s.quotes?.includes(ch)) {
      const escapes = ch !== "'";
      let j = i + 1;
      while (j < src.length) {
        if (escapes && src[j] === "\\") { j += 2; continue; }
        if (src[j] === ch) { j++; break; }
        // An unclosed quote stops at the end of its line rather than painting
        // the rest of the block green. Backticks are allowed to run on.
        if (src[j] === "\n" && ch !== "`") break;
        j++;
      }
      push(src.slice(i, j), "string");
      i = j;
      continue;
    }

    if (/[0-9]/.test(ch) && !WORD_REST.test(src[i - 1] ?? "")) {
      let j = i;
      while (j < src.length && /[0-9a-fA-FxXoObB._]/.test(src[j])) j++;
      push(src.slice(i, j), "number");
      i = j;
      continue;
    }

    if (WORD_START.test(ch)) {
      let j = i;
      while (j < src.length && WORD_REST.test(src[j])) j++;
      const word = src.slice(i, j);
      const atStart = /^[ \t]*$/.test(src.slice(lineStart, i));
      let role: Role = "plain";
      if (s.directives?.has(word) && atStart) role = "keyword";
      else if (s.keywords?.has(word)) role = "keyword";
      else if (s.types?.has(word)) role = "type";
      if (role === "plain") plain += word; else push(word, role);
      i = j;
      continue;
    }

    plain += ch;
    i++;
  }
  flush();
  return out;
}
