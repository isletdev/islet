import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { api, RequestError, type AssistantConfig, type AssistantEvent, type AssistantMessage } from "@/lib/api";
import { getNDJSON, postNDJSON } from "@/lib/stream";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Input } from "@/components/ui";

function err(e: unknown) {
  if (e instanceof RequestError) return e.message;
  return e instanceof Error ? e.message : String(e);
}

/** What the run is doing right now, in the order it happened. */
type Activity =
  | { kind: "text"; text: string }
  | { kind: "tool"; id: string; name: string; input?: Record<string, unknown>; ms?: number; ok?: boolean; preview?: string };

// Where the current run is remembered, so reopening the panel finds it again.
const RUN_KEY = "islet.assistant.run";
const remember = (id: string | null) => {
  try {
    if (id) localStorage.setItem(RUN_KEY, id);
    else localStorage.removeItem(RUN_KEY);
  } catch { /* private mode */ }
};
const remembered = () => { try { return localStorage.getItem(RUN_KEY); } catch { return null; } };

/**
 * Ask the server to do something, in words.
 *
 * The assistant has no authority of its own: every tool it calls goes through
 * the same scope and role check a request from the API passes, as whoever is
 * asking. A refused tool is shown rather than hidden, because the refusal names
 * the scope that would be needed and that is a decision for the person, not
 * something for the model to work around.
 *
 * The run belongs to the server, not to this page. A phone that locks its
 * screen, an app sent to the background or a tab closed mid-deploy all drop the
 * connection; the work carries on, and this reattaches to it — on mount, and
 * every time the tab becomes visible again — from the last event it saw.
 */
export default function Assistant() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const [cfg, setCfg] = useState<AssistantConfig | null>(null);
  const [msgs, setMsgs] = useState<AssistantMessage[]>([]);
  const [activity, setActivity] = useState<Activity[]>([]);
  const [text, setText] = useState("");
  const [running, setRunning] = useState(false);
  const [reconnecting, setReconnecting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const foot = useRef<HTMLDivElement>(null);
  const abort = useRef<AbortController | null>(null);
  const runId = useRef<string | null>(null);
  const seq = useRef(0);

  const load = useCallback(() => { void api.assistant().then(setCfg).catch(() => setCfg(null)); }, []);
  useEffect(() => { load(); }, [load]);
  // A new turn belongs on screen without being scrolled to.
  useEffect(() => { foot.current?.scrollIntoView({ behavior: "smooth", block: "end" }); }, [msgs, activity, running]);

  const handle = useCallback((ev: AssistantEvent) => {
    if (ev.type !== "ping") seq.current = ev.seq + 1;
    switch (ev.type) {
      case "start":
        runId.current = ev.runId;
        remember(ev.runId);
        setActivity([]);
        // Reattaching after a reload starts with an empty page, so the question
        // has to come back from the run rather than from this component.
        setMsgs((m) => (m.length === 0 && ev.ask ? [{ role: "user", text: ev.ask }] : m));
        break;
      case "text":
        setActivity((a) => [...a, { kind: "text", text: ev.text }]);
        break;
      case "tool":
        setActivity((a) => [...a, { kind: "tool", id: ev.id, name: ev.name, input: ev.input }]);
        break;
      case "tool_done":
        setActivity((a) => a.map((i) => (i.kind === "tool" && i.id === ev.id ? { ...i, ms: ev.ms, ok: ev.ok, preview: ev.preview } : i)));
        break;
      case "turn":
        setMsgs((m) => [...m, ev.message]);
        break;
      case "done":
        setMsgs(ev.messages);
        setActivity([]);
        setRunning(false);
        break;
      case "error":
        if (ev.messages?.length) setMsgs(ev.messages);
        setError(ev.message);
        setRunning(false);
        break;
    }
  }, []);

  // follow reads a stream to its end. It does not decide what a stream means:
  // an ending one may be a finished run or a connection that dropped, and only
  // the events say which.
  const follow = useCallback(async (start: (onEvent: (e: AssistantEvent) => void, signal: AbortSignal) => Promise<void>) => {
    const ac = new AbortController();
    abort.current?.abort();
    abort.current = ac;
    setRunning(true);
    setError(null);
    try {
      await start(handle, ac.signal);
    } catch (e) {
      if (!ac.signal.aborted) setError(err(e));
    } finally {
      if (abort.current === ac) {
        abort.current = null;
        setRunning((was) => (was ? false : was));
      }
    }
  }, [handle]);

  const ask = async (e: FormEvent) => {
    e.preventDefault();
    const q = text.trim();
    if (!q || running) return;
    const next: AssistantMessage[] = [...msgs, { role: "user", text: q }];
    setMsgs(next);
    setText("");
    setActivity([]);
    seq.current = 0;
    await follow((onEvent, signal) => postNDJSON<AssistantEvent>("/api/v1/assistant/chat", onEvent, { messages: next }, signal));
  };

  // Pick a run back up from where this page got to. Used on mount and whenever
  // the tab comes back, which on a phone is every time the screen unlocks.
  const reattach = useCallback(async (id: string, from: number) => {
    runId.current = id;
    setReconnecting(true);
    try {
      await follow((onEvent, signal) => getNDJSON<AssistantEvent>(`/api/v1/assistant/runs/${encodeURIComponent(id)}?from=${from}`, onEvent, signal));
    } finally {
      setReconnecting(false);
    }
  }, [follow]);

  useEffect(() => {
    const id = remembered();
    if (!id) return;
    // From zero: this page has just loaded and knows nothing, so the whole run
    // is replayed to rebuild it. A finished one ends immediately, which is how
    // reopening the panel shows the last answer.
    void api.assistantRuns().then((rs) => {
      if (!rs.some((r) => r.id === id)) { remember(null); return; }
      void reattach(id, 0);
    }).catch(() => { /* offline: the visibility handler tries again */ });
    // Mount only: reattach and remembered are stable, and re-running this would
    // restart the stream on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const onVisible = () => {
      if (document.visibilityState !== "visible") return;
      // Still connected: nothing to do. Otherwise resume from the last event
      // this page saw, so nothing is shown twice.
      if (abort.current || !runId.current) return;
      void api.assistantRuns().then((rs) => {
        const r = rs.find((x) => x.id === runId.current);
        if (r && (r.status === "running" || r.events > seq.current)) void reattach(r.id, seq.current);
      }).catch(() => { /* still offline */ });
    };
    document.addEventListener("visibilitychange", onVisible);
    window.addEventListener("online", onVisible);
    return () => {
      document.removeEventListener("visibilitychange", onVisible);
      window.removeEventListener("online", onVisible);
    };
  }, [reattach]);

  // Leaving the page stops the watching. The run is the daemon's and carries on.
  useEffect(() => () => abort.current?.abort(), []);

  const stop = async () => {
    const id = runId.current;
    if (id) { try { await api.assistantRunCancel(id); } catch { /* it may have just finished */ } }
    abort.current?.abort();
    abort.current = null;
    setRunning(false);
  };

  const startAgain = () => {
    abort.current?.abort();
    abort.current = null;
    runId.current = null;
    remember(null);
    setMsgs([]);
    setActivity([]);
    setError(null);
    setRunning(false);
  };

  const ready = cfg && (cfg.keySet || cfg.provider === "subscription");

  return (
    <div className="mx-auto flex h-full max-w-4xl flex-col">
      <div className="mb-2 flex flex-wrap items-start justify-between gap-2 sm:mb-3">
        <div>
          <h1 className="text-xl font-semibold tracking-[-0.02em]">Assistant</h1>
          <p className="mt-1 hidden text-ink-muted sm:block">
            Ask for what you want in words. It can do anything your account can, and nothing more.
          </p>
        </div>
        {cfg && <span className="text-xs text-ink-muted">{cfg.tools} tools · {cfg.provider}</span>}
      </div>

      {!ready && (
        <Alert tone="warning">
          No assistant is configured yet.{" "}
          {isAdmin ? <Link to="/settings" className="underline">Set one up in Settings</Link> : "Ask an admin to set one up."}
        </Alert>
      )}
      {error && <Alert>{error}</Alert>}

      <div className="mt-2 min-h-0 flex-1 space-y-3 overflow-y-auto rounded-lg border border-border bg-surface p-3">
        {msgs.length === 0 && !running && (
          <div className="py-8 text-center text-sm text-ink-muted">
            <p className="font-medium text-ink">Try asking for something.</p>
            <ul className="mt-3 space-y-1.5">
              {[
                "What needs my attention on this server?",
                "Put the panel on panel.example.com",
                "Deploy github.com/me/shop and give it a Postgres",
                "Which containers are using the most memory?",
              ].map((s) => (
                <li key={s}>
                  <button type="button" onClick={() => setText(s)} className="-my-1 py-1 underline underline-offset-2 hover:text-ink">{s}</button>
                </li>
              ))}
            </ul>
          </div>
        )}
        {msgs.map((m, i) => <Turn key={i} m={m} />)}
        {activity.length > 0 && <Working items={activity} />}
        {running && activity.length === 0 && (
          <p className="text-sm text-ink-muted">{reconnecting ? "Picking up where it got to…" : "Thinking…"}</p>
        )}
        <div ref={foot} />
      </div>

      <form onSubmit={ask} className="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1fr)_auto]">
        <Input
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={ready ? "Ask for something…" : "Configure an assistant first"}
          disabled={!ready || running}
          aria-label="Ask the assistant"
        />
        {running ? (
          <Button type="button" variant="secondary" className="h-9 text-xs" onClick={() => void stop()}>Stop</Button>
        ) : (
          <Button type="submit" className="h-9 text-xs" disabled={!ready || !text.trim()}>Ask</Button>
        )}
      </form>
      {(msgs.length > 0 || running) && (
        <button type="button" onClick={startAgain} className="mt-2 self-start text-xs text-ink-muted underline hover:text-ink">
          Start again
        </button>
      )}
    </div>
  );
}

/**
 * What is happening, while it happens.
 *
 * A screen that says "Working…" for four minutes is indistinguishable from one
 * that has hung, and a deploy really does take four minutes. Each tool is named
 * as it starts and marked as it finishes, with how long it took.
 */
function Working({ items }: { items: Activity[] }) {
  return (
    <ul className="space-y-1">
      {items.map((it, i) =>
        it.kind === "text" ? (
          <li key={i} className="whitespace-pre-wrap text-sm text-ink-muted">{it.text}</li>
        ) : (
          <li key={it.id || i} className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 font-mono text-[11px]">
            <span aria-hidden className={it.ms === undefined ? "text-ink-muted" : it.ok ? "text-success" : "text-warning"}>
              {it.ms === undefined ? "◌" : it.ok ? "✓" : "✕"}
            </span>
            <span className={it.ok === false ? "text-warning" : "text-ink"}>{it.name}</span>
            {it.input && Object.keys(it.input).length > 0 && (
              <span className="min-w-0 break-all text-ink-muted">{summarise(it.input)}</span>
            )}
            {it.ms !== undefined && <span className="text-ink-muted">{fmtMs(it.ms)}</span>}
            {it.ok === false && it.preview && <span className="w-full break-all text-warning">{it.preview}</span>}
          </li>
        ),
      )}
    </ul>
  );
}

function summarise(input: Record<string, unknown>) {
  return Object.entries(input)
    .map(([k, v]) => `${k}=${typeof v === "string" ? v : JSON.stringify(v)}`)
    .join(" ")
    .slice(0, 120);
}

function fmtMs(ms: number) {
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
}

function Turn({ m }: { m: AssistantMessage }) {
  // A turn carrying only results is the transcript of what the tools said; it
  // is shown because a refusal is the most useful thing on the screen when one
  // happens, and hiding it would leave the model's summary as the only account.
  if (m.results?.length) {
    return (
      <ul className="space-y-1">
        {m.results.map((r) => (
          <li key={r.callId} className={`overflow-x-auto whitespace-pre-wrap break-all rounded-md border px-2.5 py-1.5 font-mono text-[11px] ${r.isError ? "border-warning/40 bg-warning-soft text-warning" : "border-border text-ink-muted"}`}>
            {r.isError ? "refused: " : ""}{r.content.length > 300 ? r.content.slice(0, 300) + "…" : r.content}
          </li>
        ))}
      </ul>
    );
  }
  const mine = m.role === "user";
  return (
    <div className={mine ? "flex justify-end" : ""}>
      <div className={`max-w-[85%] rounded-lg px-3 py-2 text-sm ${mine ? "bg-ink text-on-ink" : "bg-surface-2"}`}>
        {m.text && <p className="whitespace-pre-wrap">{m.text}</p>}
        {m.calls?.length ? (
          <ul className="mt-1.5 space-y-1">
            {m.calls.map((c) => (
              <li key={c.id} className="font-mono text-[11px] text-ink-muted">→ {c.name}</li>
            ))}
          </ul>
        ) : null}
      </div>
    </div>
  );
}
