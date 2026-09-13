/**
 * Where one statement ends and the next begins.
 *
 * This is a port of internal/sqlclient/lex.go and classify.go, and it has to
 * agree with it exactly: the editor highlights the statement it is about to
 * send, and the server decides what it will actually run. Two answers to that
 * question is a tool that outlines one statement and runs another.
 *
 * It works on the UTF-8 bytes of the document rather than on the JavaScript
 * string, because Go slices bytes and the shared corpus in
 * internal/sqlclient/testdata/statements.json records byte offsets. Converting
 * once, here, is better than teaching either side to count the other's units;
 * `toChar` converts an offset back for CodeMirror.
 *
 * The parse tree from @codemirror/lang-sql drives highlighting and completion,
 * which is what a grammar is good at. It does not drive this, because the
 * grammar's idea of a statement is its own and the server's is the one that
 * matters. See docs/DECISIONS.md.
 *
 * hack/sql-split-agree.mjs runs both implementations over the corpus.
 */

export type Dialect = "postgres" | "mysql";

export type Kind = "read" | "write" | "ddl" | "transaction" | "session" | "utility" | "unknown";
export type Danger = "unfiltered" | "drop" | "truncate" | "alter" | "grant";

export interface Statement {
  sql: string;
  /** Byte offsets into the UTF-8 encoding of the document. */
  start: number;
  end: number;
  line: number;
  kind: Kind;
  returnsRows: boolean;
  danger: Danger[];
  params: string[];
}

export function dialectOf(engine: string): Dialect {
  const e = engine.toLowerCase();
  return e === "mysql" || e === "mariadb" ? "mysql" : "postgres";
}

type TokenKind = "word" | "quotedIdent" | "string" | "number" | "param" | "punct" | "semicolon";

interface Token {
  kind: TokenKind;
  text: string;
  lower: string;
  start: number;
  end: number;
  depth: number;
}

const SP = new Set([0x20, 0x09, 0x0a, 0x0d, 0x0c, 0x0b]);
const isDigit = (c: number) => c >= 0x30 && c <= 0x39;
const isAlpha = (c: number) => (c >= 0x61 && c <= 0x7a) || (c >= 0x41 && c <= 0x5a);
const isIdentStart = (c: number) => isAlpha(c) || c === 0x5f || c >= 0x80;
const isIdentPart = (c: number) => isIdentStart(c) || isDigit(c) || c === 0x24;

class Lexer {
  private pos = 0;
  private depth = 0;
  private src: Uint8Array;
  private text: string;
  private dialect: Dialect;

  // Written out rather than as constructor parameter properties: hack's
  // agreement check runs this file through Node's type stripper, which does
  // not rewrite, only erase.
  constructor(src: Uint8Array, text: string, dialect: Dialect) {
    this.src = src;
    this.text = text;
    this.dialect = dialect;
  }

  private at(i: number) { return i < this.src.length ? this.src[i] : -1; }

  private slice(a: number, b: number) {
    return decoder.decode(this.src.subarray(a, b));
  }

  private emit(kind: TokenKind, start: number): Token {
    const text = this.slice(start, this.pos);
    return { kind, text, lower: kind === "word" ? text.toLowerCase() : "", start, end: this.pos, depth: this.depth };
  }

  next(): Token | null {
    this.skipGaps();
    if (this.pos >= this.src.length) return null;
    const start = this.pos;
    const c = this.src[this.pos];

    if (c === 0x3b) { this.pos++; return this.emit("semicolon", start); }
    if (c === 0x28) { this.pos++; const t = this.emit("punct", start); this.depth++; return t; }
    if (c === 0x29) { this.pos++; if (this.depth > 0) this.depth--; return this.emit("punct", start); }
    if (c === 0x27) { this.scanQuoted(0x27, this.dialect === "mysql"); return this.emit("string", start); }
    if (c === 0x22) {
      // Postgres: a quoted identifier. MySQL: a string, unless the server runs
      // with ANSI_QUOTES, which changes nothing about where it ends.
      this.scanQuoted(0x22, this.dialect === "mysql");
      return this.emit(this.dialect === "mysql" ? "string" : "quotedIdent", start);
    }
    if (c === 0x60 && this.dialect === "mysql") { this.scanQuoted(0x60, false); return this.emit("quotedIdent", start); }
    if (c === 0x24 && this.dialect === "postgres") {
      const tag = this.dollarTag(this.pos);
      if (tag !== null) { this.scanDollarBody(tag); return this.emit("string", start); }
      this.pos++;
      while (isDigit(this.at(this.pos))) this.pos++;
      return this.emit("param", start);
    }
    if (c === 0x3f) { this.pos++; return this.emit("param", start); }
    if (c === 0x3a && this.at(this.pos + 1) === 0x3a) {
      // The cast operator, consumed whole: x::int is a cast, not a bind of :int.
      this.pos += 2;
      return this.emit("punct", start);
    }
    if (c === 0x3a && isIdentStart(this.at(this.pos + 1))) {
      this.pos++;
      while (isIdentPart(this.at(this.pos))) this.pos++;
      return this.emit("param", start);
    }
    if (isDigit(c)) {
      while (this.pos < this.src.length && (isIdentPart(this.src[this.pos]) || this.src[this.pos] === 0x2e)) this.pos++;
      return this.emit("number", start);
    }
    if (isIdentStart(c)) {
      while (isIdentPart(this.at(this.pos))) this.pos++;
      const word = this.slice(start, this.pos);
      // E'...' and friends: the prefix belongs to the string, not to a word.
      if (this.at(this.pos) === 0x27 && isStringPrefix(word, this.dialect)) {
        this.scanQuoted(0x27, this.dialect === "mysql" || word.toLowerCase() === "e");
        return this.emit("string", start);
      }
      return this.emit("word", start);
    }
    this.pos++;
    return this.emit("punct", start);
  }

  private skipGaps() {
    while (this.pos < this.src.length) {
      const c = this.src[this.pos];
      if (SP.has(c)) { this.pos++; continue; }
      if (c === 0x2d && this.lineCommentAt(this.pos)) { this.skipToEOL(); continue; }
      if (c === 0x23 && this.dialect === "mysql") { this.skipToEOL(); continue; }
      if (c === 0x2f && this.at(this.pos + 1) === 0x2a) { this.skipBlockComment(); continue; }
      return;
    }
  }

  /** MySQL needs whitespace after the second dash, so a--b is an expression there. */
  private lineCommentAt(i: number) {
    if (this.at(i + 1) !== 0x2d) return false;
    if (this.dialect !== "mysql") return true;
    if (i + 2 >= this.src.length) return true;
    return SP.has(this.src[i + 2]);
  }

  private skipToEOL() {
    while (this.pos < this.src.length && this.src[this.pos] !== 0x0a) this.pos++;
  }

  /** Postgres nests block comments; MySQL does not. */
  private skipBlockComment() {
    let nest = 0;
    while (this.pos < this.src.length) {
      if (this.src[this.pos] === 0x2f && this.at(this.pos + 1) === 0x2a) {
        nest++;
        this.pos += 2;
        if (this.dialect === "mysql" && nest > 1) nest = 1;
        continue;
      }
      if (this.src[this.pos] === 0x2a && this.at(this.pos + 1) === 0x2f) {
        this.pos += 2;
        nest--;
        if (nest <= 0) return;
        continue;
      }
      this.pos++;
    }
  }

  private scanQuoted(q: number, backslash: boolean) {
    this.pos++;
    while (this.pos < this.src.length) {
      const c = this.src[this.pos];
      if (backslash && c === 0x5c && this.pos + 1 < this.src.length) { this.pos += 2; continue; }
      if (c === q) {
        if (this.at(this.pos + 1) === q) { this.pos += 2; continue; } // '' is one quote
        this.pos++;
        return;
      }
      this.pos++;
    }
    // Unterminated: consume to the end. The user is still typing.
  }

  private dollarTag(i: number): string | null {
    let j = i + 1;
    // $ is not part of a tag: without that rule $$ swallows its own close.
    while (j < this.src.length) {
      const c = this.src[j];
      if (isAlpha(c) || c === 0x5f || c >= 0x80 || (j > i + 1 && isDigit(c))) { j++; continue; }
      break;
    }
    if (this.at(j) === 0x24) return this.slice(i, j + 1);
    return null;
  }

  private scanDollarBody(tag: string) {
    this.pos += utf8Length(tag);
    const rest = this.text;
    const from = byteToChar(this.src, this.pos);
    const k = rest.indexOf(tag, from);
    if (k >= 0) { this.pos = charToByte(this.text, k) + utf8Length(tag); return; }
    this.pos = this.src.length;
  }
}

const decoder = new TextDecoder();
const encoder = new TextEncoder();

function utf8Length(s: string) { return encoder.encode(s).length; }

function charToByte(text: string, charIndex: number) {
  return encoder.encode(text.slice(0, charIndex)).length;
}

function byteToChar(bytes: Uint8Array, byteIndex: number) {
  return decoder.decode(bytes.subarray(0, byteIndex)).length;
}

function isStringPrefix(word: string, d: Dialect) {
  switch (word.toLowerCase()) {
    case "e": case "u&": return d === "postgres";
    case "b": case "x": case "n": case "_binary": case "_utf8": case "_utf8mb4": return true;
  }
  return false;
}

/** Split a document into statements. Offsets are UTF-8 byte offsets. */
export function split(doc: string, engine: string): Statement[] {
  const dialect = dialectOf(engine);
  const bytes = encoder.encode(doc);
  const lx = new Lexer(bytes, doc, dialect);
  const lineAt = lineIndex(bytes);

  const out: Statement[] = [];
  let toks: Token[] = [];
  const flush = () => {
    if (toks.length === 0) return;
    const start = toks[0].start;
    const end = toks[toks.length - 1].end;
    const [kind, danger] = classify(toks);
    out.push({
      sql: decoder.decode(bytes.subarray(start, end)),
      start, end, line: lineAt(start), kind, danger,
      returnsRows: returnsRows(toks, kind),
      params: paramNames(toks),
    });
    toks = [];
  };

  for (;;) {
    const t = lx.next();
    if (!t) break;
    if (t.kind === "semicolon") { flush(); continue; }
    toks.push(t);
  }
  flush();
  return out;
}

function lineIndex(bytes: Uint8Array) {
  const starts = [0];
  for (let i = 0; i < bytes.length; i++) if (bytes[i] === 0x0a) starts.push(i + 1);
  return (off: number) => {
    let lo = 0;
    let hi = starts.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (starts[mid] <= off) lo = mid; else hi = mid - 1;
    }
    return lo + 1;
  };
}

function paramNames(toks: Token[]) {
  const names: string[] = [];
  const seen = new Set<string>();
  for (const t of toks) {
    if (t.kind !== "param" || !t.text.startsWith(":")) continue;
    const n = t.text.slice(1);
    if (!n || seen.has(n)) continue;
    seen.add(n);
    names.push(n);
  }
  return names;
}

// ---- classification -------------------------------------------------------

const verbKind: Record<string, Kind> = {
  select: "read", table: "read", values: "read",
  show: "read", describe: "read", desc: "read", explain: "read",

  insert: "write", update: "write", delete: "write",
  merge: "write", replace: "write", upsert: "write",
  call: "write", do: "write", import: "write", load: "write",

  create: "ddl", alter: "ddl", drop: "ddl", truncate: "ddl",
  comment: "ddl", grant: "ddl", revoke: "ddl", rename: "ddl",
  reindex: "ddl", refresh: "ddl", cluster: "ddl", security: "ddl",

  begin: "transaction", start: "transaction", commit: "transaction",
  rollback: "transaction", end: "transaction", abort: "transaction",
  savepoint: "transaction", release: "transaction",

  set: "session", reset: "session", use: "session", discard: "session",

  vacuum: "utility", analyze: "utility", analyse: "utility",
  checkpoint: "utility", lock: "utility", unlock: "utility",
  prepare: "utility", deallocate: "utility", execute: "unknown",
};

const explainOptions = new Set([
  "analyze", "analyse", "verbose", "costs", "settings", "generic_plan", "buffers",
  "serialize", "wal", "timing", "summary", "memory", "format", "json", "text",
  "xml", "yaml", "on", "off", "true", "false", "extended", "partitions", "for", "connection",
]);

function classify(toks: Token[]): [Kind, Danger[]] {
  let [i, verb] = firstWord(toks, 0);
  if (!verb) return ["unknown", []];

  // WITH may front anything. A CTE that writes makes the whole statement a
  // write, however innocent the outer SELECT looks.
  if (verb === "with") {
    const [j, v] = dmlAnywhere(toks);
    if (v) { i = j; verb = v; } else return ["read", []];
  }

  // EXPLAIN is a read unless it was asked to run the thing.
  if (verb === "explain") {
    const [j, inner, analyze] = afterExplain(toks, i);
    if (!inner || !analyze) return ["read", []];
    i = j; verb = inner;
  }

  let kind: Kind = verbKind[verb] ?? "unknown";

  // COPY runs both ways and only one of them writes.
  if (verb === "copy") return [hasWordAtTop(toks, i, "from") ? "write" : "read", []];

  const danger: Danger[] = [];
  switch (verb) {
    case "update": case "delete":
      // A WHERE inside a subquery filters the subquery, not the target table.
      if (!hasWordAtTop(toks, i, "where")) danger.push("unfiltered");
      break;
    case "drop": danger.push("drop"); break;
    case "truncate": danger.push("truncate"); break;
    case "alter": danger.push("alter"); break;
    case "grant": case "revoke": danger.push("grant"); break;
  }
  return [kind, danger];
}

function firstWord(toks: Token[], from: number): [number, string] {
  for (let i = from; i < toks.length; i++) if (toks[i].kind === "word") return [i, toks[i].lower];
  return [-1, ""];
}

function dmlAnywhere(toks: Token[]): [number, string] {
  for (let i = 0; i < toks.length; i++) {
    if (toks[i].kind !== "word") continue;
    const l = toks[i].lower;
    if (l === "insert" || l === "update" || l === "delete" || l === "merge") return [i, l];
  }
  return [-1, ""];
}

function afterExplain(toks: Token[], i: number): [number, string, boolean] {
  let analyze = false;
  for (let j = i + 1; j < toks.length; j++) {
    const t = toks[j];
    if (t.kind === "punct" && (t.text === "(" || t.text === ")" || t.text === ",")) continue;
    if (t.kind !== "word") continue;
    if (explainOptions.has(t.lower)) {
      if (t.lower === "analyze" || t.lower === "analyse") analyze = true;
      continue;
    }
    return [j, t.lower, analyze];
  }
  return [-1, "", analyze];
}

function hasWordAtTop(toks: Token[], i: number, kw: string) {
  if (i < 0 || i >= toks.length) return false;
  const top = toks[i].depth;
  for (let j = i + 1; j < toks.length; j++) {
    if (toks[j].depth === top && toks[j].kind === "word" && toks[j].lower === kw) return true;
  }
  return false;
}

function returnsRows(toks: Token[], kind: Kind) {
  if (kind === "read" || kind === "unknown") return true;
  for (const t of toks) if (t.kind === "word" && t.lower === "returning") return true;
  return false;
}

// ---- offsets at the boundary ---------------------------------------------

/**
 * Convert a byte offset into a character index CodeMirror can use.
 *
 * Both are O(n) in the prefix, which is fine for a document a person typed and
 * would not be for one a machine generated; nothing here runs per keystroke on
 * a large document.
 */
export function toChar(doc: string, byteOffset: number): number {
  if (byteOffset <= 0) return 0;
  const bytes = encoder.encode(doc);
  if (byteOffset >= bytes.length) return doc.length;
  return byteToChar(bytes, byteOffset);
}

/** The inverse, for turning a cursor position into a byte offset. */
export function toByte(doc: string, charIndex: number): number {
  return charToByte(doc, charIndex);
}

/** The statement the cursor sits in, or the one before it if it sits between two. */
export function statementAt(stmts: Statement[], byteOffset: number): Statement | null {
  let last: Statement | null = null;
  for (const s of stmts) {
    if (byteOffset >= s.start && byteOffset <= s.end) return s;
    if (s.start <= byteOffset) last = s;
  }
  return last ?? stmts[0] ?? null;
}

/** Whether a statement needs an explicit confirmation before it runs (8.2). */
export function needsConfirmation(s: Statement, production: boolean) {
  if (s.danger.length > 0) return true;
  return production && writes(s);
}

export function writes(s: Statement) {
  switch (s.kind) {
    case "read": case "transaction": case "session": case "utility": return false;
    default: return true;
  }
}

/** What to call the danger in a dialog, in words rather than a slug. */
export function dangerLabel(d: Danger): string {
  switch (d) {
    case "unfiltered": return "no WHERE clause: every row is affected";
    case "drop": return "drops an object and everything in it";
    case "truncate": return "empties a table";
    case "alter": return "changes the schema";
    case "grant": return "changes who can do what";
  }
}
