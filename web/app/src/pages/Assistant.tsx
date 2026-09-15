import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { api, RequestError, type AssistantConfig, type AssistantEvent, type AssistantMessage } from "@/lib/api";
import { postNDJSON } from "@/lib/stream";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Input } from "@/components/ui";

function err(e: unknown) {
  if (e instanceof RequestError) return e.message;
  return e instanceof Error ? e.message : String(e);
}

/**
 * Ask the server to do something, in words.
 *
 * The assistant has no authority of its own: every tool it calls goes through
 * the same scope and role check a request from the API passes, as whoever is
 * asking. A refused tool is shown rather than hidden, because the refusal names
 * the scope that would be needed and that is a decision for the person, not
 * something for the model to work around.
 */
export default function Assistant() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const [cfg, setCfg] = useState<AssistantConfig | null>(null);
  const [msgs, setMsgs] = useState<AssistantMessage[]>([]);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const foot = useRef<HTMLDivElement>(null);
  const abort = useRef<AbortController | null>(null);

  const load = useCallback(() => { void api.assistant().then(setCfg).catch(() => setCfg(null)); }, []);
  useEffect(() => { load(); }, [load]);
  // Leaving the page ends the request; the loop stops with it, server-side.
  useEffect(() => () => abort.current?.abort(), []);
  // A new turn belongs on screen without being scrolled to.
  useEffect(() => { foot.current?.scrollIntoView({ behavior: "smooth", block: "end" }); }, [msgs, busy]);

  // The answer arrives as it is produced, one turn at a time. Waiting for the
  // whole loop and rendering it at the end is what the endpoint used to do, and
  // a request that asks for a deploy runs for minutes — long enough for a proxy
  // in front of the panel to give up on the origin and answer 524 instead.
  const ask = async (e: FormEvent) => {
    e.preventDefault();
    const q = text.trim();
    if (!q || busy) return;
    const next: AssistantMessage[] = [...msgs, { role: "user", text: q }];
    setMsgs(next); setText(""); setBusy(true); setError(null);
    const ac = new AbortController();
    abort.current = ac;
    const live = [...next];
    let finished = false;
    try {
      await postNDJSON<AssistantEvent>("/api/v1/assistant/chat", (ev) => {
        switch (ev.type) {
          case "turn":
            live.push(ev.message);
            setMsgs([...live]);
            break;
          case "done":
            finished = true;
            setMsgs(ev.messages);
            break;
          case "error":
            // The transcript comes with the failure: a run that got three tools
            // in before the provider broke is worth reading, not discarding.
            finished = true;
            if (ev.messages?.length) setMsgs(ev.messages);
            setError(ev.message);
            break;
          default:
            // start and ping say only that the connection is alive.
            break;
        }
      }, { messages: next }, ac.signal);
      // Ending without a closing line means the connection was cut, not that
      // the assistant was done. Reporting that as success is how a run killed
      // by a daemon restart used to look finished.
      if (!finished && !ac.signal.aborted) setError("the connection closed before the assistant finished");
    } catch (e2) {
      if (!ac.signal.aborted) setError(err(e2));
    } finally {
      abort.current = null;
      setBusy(false);
    }
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
        {msgs.length === 0 && (
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
        {busy && <p className="text-sm text-ink-muted">Working…</p>}
        <div ref={foot} />
      </div>

      <form onSubmit={ask} className="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1fr)_auto]">
        <Input
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={ready ? "Ask for something…" : "Configure an assistant first"}
          disabled={!ready || busy}
          aria-label="Ask the assistant"
        />
        {busy ? (
          <Button type="button" variant="secondary" className="h-9 text-xs" onClick={() => abort.current?.abort()}>
            Stop
          </Button>
        ) : (
          <Button type="submit" className="h-9 text-xs" disabled={!ready || !text.trim()}>Ask</Button>
        )}
      </form>
      {msgs.length > 0 && (
        <button type="button" onClick={() => { setMsgs([]); setError(null); }} className="mt-2 self-start text-xs text-ink-muted underline hover:text-ink">
          Start again
        </button>
      )}
    </div>
  );
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
