import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { api, RequestError, type AssistantConfig, type AssistantMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Input } from "@/components/ui";

function err(e: unknown) { return e instanceof RequestError ? e.message : String(e); }

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

  const load = useCallback(() => { void api.assistant().then(setCfg).catch(() => setCfg(null)); }, []);
  useEffect(() => { load(); }, [load]);
  // A new turn belongs on screen without being scrolled to.
  useEffect(() => { foot.current?.scrollIntoView({ behavior: "smooth", block: "end" }); }, [msgs, busy]);

  const ask = async (e: FormEvent) => {
    e.preventDefault();
    const q = text.trim();
    if (!q || busy) return;
    const next: AssistantMessage[] = [...msgs, { role: "user", text: q }];
    setMsgs(next); setText(""); setBusy(true); setError(null);
    try {
      const r = await api.assistantChat({ messages: next });
      setMsgs(r.messages);
    } catch (e2) {
      setError(err(e2));
    } finally {
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
        <Button type="submit" className="h-9 text-xs" disabled={!ready || busy || !text.trim()}>
          {busy ? "Working…" : "Ask"}
        </Button>
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
