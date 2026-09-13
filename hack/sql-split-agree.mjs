// The two statement splitters have to give the same answer.
//
// The editor outlines the statement it is about to send and the server decides
// what it will actually run. If those disagree, the tool highlights one
// statement and runs another, which is the worst possible way to be wrong about
// a DELETE. internal/sqlclient/testdata/statements.json is the shared corpus:
// the Go tests read it, and so does this.
//
// Usage, from the repository root:
//
//   node --experimental-strip-types hack/sql-split-agree.mjs
//
// No test framework and no build step: Node strips the types and imports the
// application's own module, so this checks the code that ships.

import { readFileSync } from "node:fs";
import { split } from "../web/app/src/lib/sqlsplit.ts";

const corpus = JSON.parse(
  readFileSync(new URL("../internal/sqlclient/testdata/statements.json", import.meta.url), "utf8"),
);

let failed = 0;
let checked = 0;

for (const c of corpus.cases) {
  const got = split(c.doc, c.engine);
  const want = c.statements;
  const problems = [];

  if (got.length !== want.length) {
    problems.push(`${got.length} statements, want ${want.length}`);
  }
  for (let i = 0; i < Math.min(got.length, want.length); i++) {
    const g = got[i];
    const w = want[i];
    if (g.sql !== w.sql) problems.push(`[${i}] sql ${JSON.stringify(g.sql)} != ${JSON.stringify(w.sql)}`);
    if (g.start !== w.start) problems.push(`[${i}] start ${g.start} != ${w.start}`);
    if (g.end !== w.end) problems.push(`[${i}] end ${g.end} != ${w.end}`);
    if (g.line !== w.line) problems.push(`[${i}] line ${g.line} != ${w.line}`);
    if (g.kind !== w.kind) problems.push(`[${i}] kind ${g.kind} != ${w.kind}`);
    const wantDanger = (w.danger ?? []).join(",");
    const gotDanger = g.danger.join(",");
    if (gotDanger !== wantDanger) problems.push(`[${i}] danger [${gotDanger}] != [${wantDanger}]`);
    const wantParams = (w.params ?? []).join(",");
    const gotParams = g.params.join(",");
    if (gotParams !== wantParams) problems.push(`[${i}] params [${gotParams}] != [${wantParams}]`);
  }

  checked++;
  if (problems.length) {
    failed++;
    console.log(`FAIL  ${c.name}`);
    for (const p of problems) console.log(`        ${p}`);
  }
}

console.log(`\n${checked - failed}/${checked} cases agree with the Go splitter.`);
if (failed) process.exit(1);
