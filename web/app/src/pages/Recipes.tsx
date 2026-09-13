import { useEffect, useRef, useState, type FormEvent } from "react";
import { api, RequestError, type Recipe } from "@/lib/api";
import { postStream } from "@/lib/stream";
import { useAuth } from "@/lib/auth";
import { Button, Card, Field, Input, Select } from "@/components/ui";
import AppIcon from "@/components/AppIcon";
import { capLines } from "@/lib/logcap";

function err(e: unknown) { return e instanceof RequestError ? e.message : e instanceof Error ? e.message : String(e); }

/** Guided setups: one form, then every step runs with live progress and
 * undoes itself on failure. */
export default function Recipes() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const [list, setList] = useState<Recipe[]>([]);
  const [sel, setSel] = useState<Recipe | null>(null);
  useEffect(() => { void api.recipes().then(setList).catch(() => {}); }, []);
  const cats = Array.from(new Set(list.map((r) => r.category)));
  return (
    <div className="space-y-4">
      {sel && <Runner recipe={sel} isAdmin={isAdmin} onClose={() => setSel(null)} />}
      {cats.map((c) => (
        <div key={c}>
          <h2 className="mb-2 text-[11px] font-medium text-ink-faint">{c}</h2>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {list.filter((r) => r.category === c).map((r) => (
              <button key={r.slug} type="button" onClick={() => { setSel(r); window.scrollTo({ top: 0 }); }} className={`rounded-lg border bg-surface p-4 text-left transition-colors hover:border-border-strong ${sel?.slug === r.slug ? "border-ink" : "border-border"}`}>
                <div className="flex items-center gap-2.5">
                  <AppIcon slug={r.slug} category={r.category} name={r.name} size="sm" />
                  <span className="min-w-0 flex-1 truncate font-medium">{r.name}</span>
                  <span className="shrink-0 font-mono text-[11px] text-ink-faint">{r.time}</span>
                </div>
                <p className="mt-2 text-xs text-ink-muted">{r.description}</p>
                <p className="mt-2 text-[11px] text-ink-faint">{r.steps.length} steps: {r.steps.map((s) => s.type).join(" → ")}</p>
              </button>
            ))}
          </div>
        </div>
      ))}
      {list.length === 0 && <p className="text-sm text-ink-muted">No recipes found.</p>}
    </div>
  );
}

function Runner({ recipe, isAdmin, onClose }: { recipe: Recipe; isAdmin: boolean; onClose: () => void }) {
  const [vals, setVals] = useState<Record<string, string>>(() => Object.fromEntries(recipe.inputs.map((i) => [i.key, i.default ?? ""])));
  const [log, setLog] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const box = useRef<HTMLPreElement>(null);
  useEffect(() => { setVals(Object.fromEntries(recipe.inputs.map((i) => [i.key, i.default ?? ""]))); setLog(null); setMsg(null); }, [recipe]);
  useEffect(() => { box.current?.scrollTo(0, box.current.scrollHeight); }, [log]);
  const run = async (e: FormEvent) => {
    e.preventDefault(); setBusy(true); setLog([]); setMsg(null);
    try {
      await postStream(`/api/v1/recipes/${recipe.slug}/run`, (l) => { setLog((p) => capLines(p, l)); if (l.startsWith("error:")) setMsg(l.slice(6).trim()); }, vals);
    } catch (er) { setMsg(err(er)); }
    finally { setBusy(false); }
  };
  const done = log?.some((l) => l.startsWith("[recipe] done"));
  const step = log ? log.filter((l) => l.startsWith("[recipe] step")).length : 0;
  return (
    <Card title={recipe.name} description={recipe.description} icon={<AppIcon slug={recipe.slug} category={recipe.category} name={recipe.name} />}>
      <form onSubmit={run} className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {recipe.inputs.map((i) => (
          <Field key={i.key} label={i.label + (i.optional ? " (optional)" : "")} hint={i.hint}>
            {i.type === "select" ? (
              <Select value={vals[i.key] ?? ""} onChange={(e) => setVals({ ...vals, [i.key]: e.target.value })}>{(i.options ?? "").split(",").map((o) => <option key={o} value={o.trim()}>{o.trim()}</option>)}</Select>
            ) : (
              <Input value={vals[i.key] ?? ""} onChange={(e) => setVals({ ...vals, [i.key]: e.target.value })} type={i.type === "secret" ? "password" : "text"} className={i.type === "url" || i.type === "domain" || i.key === "name" ? "font-mono" : ""} required={!i.optional} autoComplete="off" />
            )}
          </Field>
        ))}
        <div className="flex items-center gap-2 sm:col-span-2">
          <Button type="submit" disabled={!isAdmin || busy}>{busy ? `Running step ${step} of ${recipe.steps.length}…` : done ? "Run again" : "Run"}</Button>
          <Button type="button" variant="secondary" onClick={onClose}>Close</Button>
          {!isAdmin && <span className="text-xs text-ink-muted">Only admins run recipes.</span>}
          {msg && <span className="text-sm text-danger">{msg}</span>}
        </div>
      </form>
      {log && (
        <div className="mt-3">
          <ol className="mb-2 flex flex-wrap gap-1 text-[11px]">{recipe.steps.map((s, i) => <li key={i} className={`rounded-sm border px-1.5 py-0.5 ${i < step - 1 || done ? "border-success/40 text-success" : i === step - 1 && !done ? (msg ? "border-danger/40 text-danger" : "border-accent/40 text-accent") : "border-border text-ink-faint"}`}>{i + 1}. {s.label ? s.label.replace(/\{\{[^}]+\}\}/g, "…") : s.type}</li>)}</ol>
          <pre ref={box} className="max-h-96 overflow-auto rounded-md border border-border bg-[#0A0A0A] p-3 font-mono text-xs text-[#FAFAFA] whitespace-pre-wrap">{log.join("\n") || "starting…"}</pre>
          {done && <p className="mt-2 text-sm text-success">Done. {log.find((l) => l.startsWith("[recipe] done"))?.replace("[recipe] done.", "").trim()}</p>}
          {msg && log.some((l) => l.includes("undid")) && <p className="mt-2 text-xs text-ink-muted">Everything the recipe created was removed again; fix the cause and run it once more.</p>}
        </div>
      )}
    </Card>
  );
}
