import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { api, RequestError, type AssistantChat, type AssistantConfig, type AssistantEvent, type AssistantMessage } from "@/lib/api";
import { getNDJSON, postNDJSON } from "@/lib/stream";
import { useDialog } from "@/lib/dialogs";
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

// Which conversation this device had open. The conversations themselves live on
// the server; this is only so reopening the panel lands where you left it.
const LAST_KEY = "islet.assistant.chat";
const rememberChat = (id: string | null) => {
  try {
    if (id) localStorage.setItem(LAST_KEY, id);
    else localStorage.removeItem(LAST_KEY);
  } catch { /* private mode */ }
};
const lastChat = () => { try { return localStorage.getItem(LAST_KEY); } catch { return null; } };

/**
 * Ask the server to do something, in words.
 *
 * The assistant has no authority of its own: every tool it calls goes through
 * the same scope and role check a request from the API passes, as whoever is
 * asking. A refused tool is shown rather than hidden, because the refusal names
 * the scope that would be needed and that is a decision for the person, not
 * something for the model to work around.
 *
 * Neither the conversation nor the work belongs to this page. Conversations are
 * stored on the server, so one started on a laptop opens on a phone; runs
 * outlive the connection, so a locked screen does not stop a deploy, and this
 * reattaches to whatever is still working — on mount, when the tab comes back,
 * and when the network does.
 */
export default function Assistant() {
  const { state } = useAuth();
  const ask = useDialog();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const [cfg, setCfg] = useState<AssistantConfig | null>(null);
  const [chats, setChats] = useState<AssistantChat[]>([]);
  const [chatId, setChatId] = useState<string | null>(null);
  const [msgs, setMsgs] = useState<AssistantMessage[]>([]);
  const [activity, setActivity] = useState<Activity[]>([]);
  const [text, setText] = useState("");
  const [running, setRunning] = useState(false);
  const [reconnecting, setReconnecting] = useState(false);
  const [listOpen, setListOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const foot = useRef<HTMLDivElement>(null);
  const abort = useRef<AbortController | null>(null);
  const runId = useRef<string | null>(null);
  const openChat = useRef<string | null>(null);
  const seq = useRef(0);

  const refreshChats = useCallback(async () => {
    try {
      const list = await api.assistantChats();
      setChats(list);
      return list;
    } catch {
      return [] as AssistantChat[];
    }
  }, []);

  useEffect(() => { void api.assistant().then(setCfg).catch(() => setCfg(null)); }, []);
  // A new turn belongs on screen without being scrolled to.
  useEffect(() => { foot.current?.scrollIntoView({ behavior: "smooth", block: "end" }); }, [msgs, activity, running]);

  const handle = useCallback((ev: AssistantEvent) => {
    if (ev.type !== "ping") seq.current = ev.seq + 1;
    switch (ev.type) {
      case "start":
        runId.current = ev.runId;
        if (ev.chatId) {
          openChat.current = ev.chatId;
          setChatId(ev.chatId);
          rememberChat(ev.chatId);
        }
        setActivity([]);
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
        void refreshChats();
        break;
      case "error":
        if (ev.messages?.length) setMsgs(ev.messages);
        setError(ev.message);
        setRunning(false);
        void refreshChats();
        break;
    }
  }, [refreshChats]);

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
        setRunning(false);
      }
    }
  }, [handle]);

  // Pick a run back up. `from` is what this page already has, so nothing is
  // shown twice — the stored transcript and the live stream do not overlap.
  const attach = useCallback(async (id: string, from: number) => {
    runId.current = id;
    seq.current = from;
    setReconnecting(true);
    try {
      await follow((onEvent, signal) => getNDJSON<AssistantEvent>(`/api/v1/assistant/runs/${encodeURIComponent(id)}?from=${from}`, onEvent, signal));
    } finally {
      setReconnecting(false);
    }
  }, [follow]);

  const open = useCallback(async (id: string) => {
    abort.current?.abort();
    abort.current = null;
    openChat.current = id;
    setChatId(id);
    rememberChat(id);
    setListOpen(false);
    setError(null);
    setActivity([]);
    try {
      const detail = await api.assistantChatOpen(id);
      setMsgs(detail.turns);
      runId.current = detail.runId ?? null;
      // Still working somewhere else — another device, or this one before the
      // screen locked. Watch from the point the stored transcript reaches.
      if (detail.runId) void attach(detail.runId, detail.runEvents ?? 0);
      else setRunning(false);
    } catch (e) {
      setError(err(e));
    }
  }, [attach]);

  // On arrival: list the conversations, and open the one that is working, or
  // the one this device had open, or the most recent.
  useEffect(() => {
    void (async () => {
      const list = await refreshChats();
      if (list.length === 0) return;
      const working = list.find((c) => c.runId);
      const last = lastChat();
      const pick = working ?? list.find((c) => c.id === last) ?? list[0];
      void open(pick.id);
    })();
    // Mount only: re-running this would reopen the conversation on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // A phone unlocking, or a network coming back, is when a dropped stream has
  // to be picked up again.
  useEffect(() => {
    const back = () => {
      if (document.visibilityState !== "visible" || abort.current) return;
      void refreshChats().then((list) => {
        const here = list.find((c) => c.id === openChat.current);
        if (here?.runId) void attach(here.runId, here.id === openChat.current && runId.current === here.runId ? seq.current : 0);
      });
    };
    document.addEventListener("visibilitychange", back);
    window.addEventListener("online", back);
    return () => {
      document.removeEventListener("visibilitychange", back);
      window.removeEventListener("online", back);
    };
  }, [attach, refreshChats]);

  // Leaving the page stops the watching. The run is the daemon's and carries on.
  useEffect(() => () => abort.current?.abort(), []);

  const send = async (e: FormEvent) => {
    e.preventDefault();
    const q = text.trim();
    if (!q || running) return;
    setMsgs((m) => [...m, { role: "user", text: q }]);
    setText("");
    setActivity([]);
    seq.current = 0;
    await follow((onEvent, signal) =>
      postNDJSON<AssistantEvent>("/api/v1/assistant/chat", onEvent, { chatId: chatId ?? undefined, text: q }, signal));
  };

  const newChat = () => {
    abort.current?.abort();
    abort.current = null;
    runId.current = null;
    openChat.current = null;
    setChatId(null);
    rememberChat(null);
    setMsgs([]);
    setActivity([]);
    setError(null);
    setRunning(false);
    setListOpen(false);
  };

  const stop = async () => {
    const id = runId.current;
    if (id) { try { await api.assistantRunCancel(id); } catch { /* it may have just finished */ } }
    abort.current?.abort();
    abort.current = null;
    setRunning(false);
  };

  const remove = async (c: AssistantChat) => {
    if (!(await ask.confirm({ title: "Delete this conversation?", body: c.title || "Untitled", confirmLabel: "Delete", tone: "danger" }))) return;
    try {
      await api.assistantChatDelete(c.id);
    } catch (e) {
      setError(err(e));
      return;
    }
    const list = await refreshChats();
    if (openChat.current === c.id) {
      if (list.length > 0) void open(list[0].id);
      else newChat();
    }
  };

  const rename = async (c: AssistantChat) => {
    const title = await ask.prompt({ title: "Rename conversation", label: "Title", defaultValue: c.title });
    if (title === null) return;
    try {
      await api.assistantChatRename(c.id, title);
      await refreshChats();
    } catch (e) { setError(err(e)); }
  };

  const ready = cfg && (cfg.keySet || cfg.provider === "subscription");

  return (
    <div className="mx-auto flex h-full max-w-6xl flex-col">
      <div className="mb-2 flex flex-wrap items-start justify-between gap-2 sm:mb-3">
        <div>
          <h1 className="text-xl font-semibold tracking-[-0.02em]">Assistant</h1>
          <p className="mt-1 hidden text-ink-muted sm:block">
            Ask for what you want in words. It can do anything your account can, and nothing more.
          </p>
        </div>
        <div className="flex items-center gap-2">
          {cfg && <span className="hidden text-xs text-ink-muted sm:inline">{cfg.tools} tools · {cfg.provider}</span>}
          <Button type="button" variant="secondary" className="h-8 text-xs md:hidden" onClick={() => setListOpen((v) => !v)}>
            {listOpen ? "Close" : `Chats${chats.length ? ` (${chats.length})` : ""}`}
          </Button>
          <Button type="button" className="h-8 text-xs" onClick={newChat}>New</Button>
        </div>
      </div>

      {!ready && (
        <Alert tone="warning">
          No assistant is configured yet.{" "}
          {isAdmin ? <Link to="/settings" className="underline">Set one up in Settings</Link> : "Ask an admin to set one up."}
        </Alert>
      )}
      {error && <Alert>{error}</Alert>}

      <div className="grid min-h-0 flex-1 grid-cols-1 gap-3 md:grid-cols-[minmax(0,15rem)_minmax(0,1fr)]">
        <aside className={`${listOpen ? "" : "hidden"} min-h-0 overflow-y-auto rounded-lg border border-border bg-surface p-1.5 md:block`}>
          {chats.length === 0 && <p className="px-2 py-3 text-xs text-ink-muted">No conversations yet.</p>}
          <ul className="space-y-0.5">
            {chats.map((c) => (
              <li key={c.id} className="group relative">
                <button
                  type="button"
                  onClick={() => void open(c.id)}
                  className={`w-full truncate rounded-md px-2 py-2 pr-14 text-left text-xs ${c.id === chatId ? "bg-surface-2 text-ink" : "text-ink-muted hover:bg-surface-2 hover:text-ink"}`}
                  title={c.title || "Untitled"}
                >
                  {c.runId && <span aria-label="working" className="mr-1.5 inline-block size-1.5 rounded-full bg-success align-middle" />}
                  {c.title || "Untitled"}
                </button>
                <span className="absolute right-1 top-1/2 flex -translate-y-1/2 gap-1 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100">
                  <button type="button" onClick={() => void rename(c)} className="-my-1 px-1 py-1 text-[11px] text-ink-muted hover:text-ink" aria-label={`Rename ${c.title || "conversation"}`}>✎</button>
                  <button type="button" onClick={() => void remove(c)} className="-my-1 px-1 py-1 text-[11px] text-ink-muted hover:text-danger" aria-label={`Delete ${c.title || "conversation"}`}>✕</button>
                </span>
              </li>
            ))}
          </ul>
        </aside>

        <section className={`${listOpen ? "hidden" : "flex"} min-h-0 flex-col md:flex`}>
          <div className="min-h-0 flex-1 space-y-3 overflow-y-auto rounded-lg border border-border bg-surface p-3">
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

          <form onSubmit={send} className="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1fr)_auto]">
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
        </section>
      </div>
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
