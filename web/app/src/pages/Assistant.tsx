import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { api, assistantUpload, RequestError, type AIProvider, type AssistantAttachment, type AssistantChat, type AssistantConfig, type AssistantEvent, type AssistantMessage, type AssistantToolResult } from "@/lib/api";
import { getNDJSON, postNDJSON } from "@/lib/stream";
import { useDialog } from "@/lib/dialogs";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Input, Select } from "@/components/ui";
import Markdown from "@/components/Markdown";
import { ArchiveFileIcon, CloseIcon, FileIcon, ImageFileIcon, MicIcon, PaperclipIcon, VideoFileIcon } from "@/components/icons";

function err(e: unknown) {
  if (e instanceof RequestError) return e.message;
  return e instanceof Error ? e.message : String(e);
}

/**
 * A file on its way to the server, or already there.
 *
 * It exists in the composer before it exists on the server, because the upload
 * of a video takes a while and a paperclip that does nothing for a minute is
 * indistinguishable from a broken one. `id` arrives when the server has taken
 * it and the scan has passed; until then this row is a progress bar, and if it
 * never arrives it is an error with the file's name on it.
 */
type Pending = {
  key: string;
  name: string;
  size: number;
  type: string;
  /** An object URL of the local file, so the thumbnail is there before the
      upload is. Revoked when the row goes. */
  preview?: string;
  progress: number;
  id?: string;
  path?: string;
  error?: string;
};

/** What to draw for a file, from its media type. */
function fileIcon(type: string) {
  if (type.startsWith("image/")) return ImageFileIcon;
  if (type.startsWith("video/") || type.startsWith("audio/")) return VideoFileIcon;
  if (/zip|compressed|tar|gzip|x-7z|rar/.test(type)) return ArchiveFileIcon;
  return FileIcon;
}

function size(n: number) {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(1)} GB`;
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${Math.round(n / (1 << 10))} KB`;
  return `${n} B`;
}

/**
 * Dictation, where the browser offers it.
 *
 * The Web Speech API rather than anything on this server: a model that
 * transcribes audio is another dependency, another key and another bill, for a
 * convenience. Where the browser has no such thing — Firefox — the button is
 * not drawn rather than drawn and disabled.
 *
 * It is worth knowing that Chrome's implementation sends the audio to Google,
 * which on a self-hosted panel is not what everybody expects, so the button
 * says so.
 */
type SpeechResult = ArrayLike<{ transcript: string }> & { isFinal: boolean };
type SpeechEvent = { results: ArrayLike<SpeechResult>; resultIndex: number };
interface Recognizer {
  lang: string;
  continuous: boolean;
  interimResults: boolean;
  start(): void;
  stop(): void;
  onresult: ((e: SpeechEvent) => void) | null;
  onerror: ((e: { error: string }) => void) | null;
  onend: (() => void) | null;
}
function newRecognizer(): Recognizer | null {
  if (typeof window === "undefined") return null;
  const w = window as unknown as { SpeechRecognition?: new () => Recognizer; webkitSpeechRecognition?: new () => Recognizer };
  const Ctor = w.SpeechRecognition ?? w.webkitSpeechRecognition;
  return Ctor ? new Ctor() : null;
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
  const [models, setModels] = useState<AIProvider[]>([]);
  // Which model the next conversation will use. Empty means the default, which
  // is the whole answer when only one is configured — then nothing is asked.
  const [provider, setProvider] = useState("");
  const [chats, setChats] = useState<AssistantChat[]>([]);
  const [chatId, setChatId] = useState<string | null>(null);
  const [msgs, setMsgs] = useState<AssistantMessage[]>([]);
  const [activity, setActivity] = useState<Activity[]>([]);
  const [text, setText] = useState("");
  // What is attached to the message not yet sent, and what this server can do
  // about malware. scanner is null until asked, "" when nothing is installed.
  const [pending, setPending] = useState<Pending[]>([]);
  const [scanner, setScanner] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);
  const [listening, setListening] = useState(false);
  const picker = useRef<HTMLInputElement>(null);
  const recog = useRef<Recognizer | null>(null);
  const [canDictate] = useState(() => newRecognizer() !== null);
  const [running, setRunning] = useState(false);
  const [reconnecting, setReconnecting] = useState(false);
  const [listOpen, setListOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const foot = useRef<HTMLDivElement>(null);
  const abort = useRef<AbortController | null>(null);
  const runId = useRef<string | null>(null);
  const openChat = useRef<string | null>(null);
  // Whether the run reported an ending of its own. Without it a dropped
  // connection is indistinguishable from a finished answer.
  const settled = useRef(false);
  const retry = useRef<number | undefined>(undefined);
  const tries = useRef(0);
  const resumeNow = useRef<() => Promise<void>>(undefined);

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
  // Asked once, so the composer can say whether anything will check a file
  // before it is uploaded rather than after.
  useEffect(() => { void api.assistantUploads().then((r) => setScanner(r.scanner ?? "")).catch(() => setScanner(null)); }, []);
  useEffect(() => {
    void api.aiProviders().then((list) => {
      setModels(list);
      setProvider((p) => p || list.find((m) => m.default)?.id || list[0]?.id || "");
    }).catch(() => setModels([]));
  }, []);
  // A new turn belongs on screen without being scrolled to.
  useEffect(() => { foot.current?.scrollIntoView({ behavior: "smooth", block: "end" }); }, [msgs, activity, running]);

  const handle = useCallback((ev: AssistantEvent) => {
    switch (ev.type) {
      case "start":
        runId.current = ev.runId;
        settled.current = false;
        if (ev.chatId) {
          openChat.current = ev.chatId;
          setChatId(ev.chatId);
          rememberChat(ev.chatId);
        }
        // The stream is replayed from the beginning whenever this page
        // reattaches, so anything this run already wrote into the stored
        // conversation is dropped first and rebuilt from the events.
        if (typeof ev.base === "number") setMsgs((m) => (m.length > ev.base ? m.slice(0, ev.base) : m));
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
        // The turn carries what it did, so the running list has said its piece.
        setActivity([]);
        break;
      case "done":
        settled.current = true;
        setMsgs(ev.messages);
        setActivity([]);
        setRunning(false);
        void refreshChats();
        break;
      case "error":
        // The run itself failed, which is the only kind of failure worth
        // putting on the screen. A connection that drops is not one.
        settled.current = true;
        if (ev.messages?.length) setMsgs(ev.messages);
        setError(ev.message);
        setRunning(false);
        void refreshChats();
        break;
    }
  }, [refreshChats]);

  // Backing off, because a daemon that is restarting should not be hammered,
  // and a phone in a tunnel will not be helped by trying twice a second.
  //
  // It reaches the current resume through a ref: the two call each other — a
  // failed resume schedules another — and a ref is how that is written without
  // one of them capturing a stale copy of the other.
  const resumeSoon = useCallback(() => {
    window.clearTimeout(retry.current);
    setReconnecting(true);
    setRunning(true);
    const wait = Math.min(15000, 800 * 2 ** Math.min(tries.current, 4));
    tries.current += 1;
    retry.current = window.setTimeout(() => { void resumeNow.current?.(); }, wait);
  }, []);

  // follow reads a stream to its end and decides what its ending meant.
  //
  // A stream that stops is not a failure. A phone locking its screen, a tab
  // going to the background, a train entering a tunnel: all of them end the
  // connection while the run carries on, and reporting that as an error — which
  // is what this used to do, because a dropped fetch is a rejected promise —
  // put "Failed to fetch" on the screen of somebody whose deploy was fine. Only
  // an `error` event means the run failed. Everything else reconnects.
  const follow = useCallback(async (start: (onEvent: (e: AssistantEvent) => void, signal: AbortSignal) => Promise<void>) => {
    const ac = new AbortController();
    abort.current?.abort();
    abort.current = ac;
    settled.current = false;
    setRunning(true);
    setError(null);
    let dropped: boolean;
    try {
      await start(handle, ac.signal);
      // Ending without a closing line is a cut connection, not an answer.
      dropped = !settled.current;
    } catch {
      dropped = true;
    } finally {
      if (abort.current === ac) abort.current = null;
    }
    // Stopped on purpose: by the Stop button, by opening another conversation,
    // or by leaving the page.
    if (ac.signal.aborted) return;
    if (dropped) resumeSoon();
    else setRunning(false);
  }, [handle, resumeSoon]);

  // Pick the run back up from the beginning.
  //
  // From the beginning, not from where this page got to: the events are what
  // say which tools ran, and a client that reattached from its own position saw
  // "working…" with an empty list until the run finished. The run's first event
  // carries the point in the conversation it started from, so replaying the lot
  // cannot double anything.
  const attach = useCallback(async (id: string) => {
    runId.current = id;
    setReconnecting(true);
    try {
      await follow((onEvent, signal) => getNDJSON<AssistantEvent>(`/api/v1/assistant/runs/${encodeURIComponent(id)}?from=0`, onEvent, signal));
    } finally {
      setReconnecting(false);
    }
  }, [follow]);

  // resume finds whatever is working on the open conversation and watches it
  // again. When nothing is, the conversation is simply reloaded: the run
  // finished while this page was away, and the answer is already stored.
  const resume = useCallback(async () => {
    window.clearTimeout(retry.current);
    const chat = openChat.current;
    if (!chat) { setRunning(false); return; }
    try {
      const list = await api.assistantChats();
      const here = list.find((c) => c.id === chat);
      setChats(list);
      if (!here) { setRunning(false); return; }
      if (here.runId) {
        tries.current = 0;
        await attach(here.runId);
        return;
      }
      const detail = await api.assistantChatOpen(chat);
      setMsgs(detail.turns);
      setActivity([]);
      setRunning(false);
    } catch {
      // Still unreachable. Come back later, more slowly each time, and the
      // moment the tab or the network returns.
      resumeSoon();
    }
  }, [attach, resumeSoon]);

  // The timer above fires into whatever resume is current.
  useEffect(() => { resumeNow.current = resume; }, [resume]);


  const open = useCallback(async (id: string) => {
    window.clearTimeout(retry.current);
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
      // Still working — on another device, or on this one before the screen
      // locked. Watch it, replaying what already happened.
      if (detail.runId) void attach(detail.runId);
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
  // A phone unlocking, or a network coming back, is when a dropped stream has
  // to be picked up again — and is the moment to stop waiting out a backoff.
  useEffect(() => {
    const back = () => {
      if (document.visibilityState !== "visible" || abort.current) return;
      tries.current = 0;
      void resume();
    };
    document.addEventListener("visibilitychange", back);
    window.addEventListener("online", back);
    return () => {
      document.removeEventListener("visibilitychange", back);
      window.removeEventListener("online", back);
    };
  }, [resume]);

  // Leaving the page stops the watching, and the waiting. The run is the
  // daemon's and carries on either way.
  useEffect(() => () => {
    window.clearTimeout(retry.current);
    abort.current?.abort();
  }, []);

  // ---- attachments --------------------------------------------------------

  // Each file is uploaded on its own, as soon as it is chosen. By the time the
  // question is typed the bytes are usually already there, and a file that is
  // refused — too large, or a scanner that objects — says so against that file
  // instead of failing the question with it.
  const addFiles = useCallback((chosen: FileList | File[] | null) => {
    if (!chosen) return;
    for (const file of Array.from(chosen)) {
      const key = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
      const preview = file.type.startsWith("image/") ? URL.createObjectURL(file) : undefined;
      setPending((p) => [...p, { key, name: file.name, size: file.size, type: file.type, preview, progress: 0 }]);
      void assistantUpload(file, (f) => setPending((p) => p.map((x) => (x.key === key ? { ...x, progress: f } : x))))
        .then((u) => setPending((p) => p.map((x) => (x.key === key ? { ...x, id: u.id, path: u.path, type: u.type || x.type, progress: 1 } : x))))
        .catch((e) => setPending((p) => p.map((x) => (x.key === key ? { ...x, error: err(e) } : x))));
    }
  }, []);

  // Taking a file off the message deletes it from the server too. It was put
  // there to be used in this conversation; a file nobody attached in the end is
  // disk somebody has to find and remove by hand.
  const unattach = useCallback((key: string) => {
    setPending((p) => {
      const gone = p.find((x) => x.key === key);
      if (gone?.preview) URL.revokeObjectURL(gone.preview);
      if (gone?.id) void api.assistantUploadDelete(gone.id).catch(() => { /* it may already be gone */ });
      return p.filter((x) => x.key !== key);
    });
  }, []);

  // Previews are object URLs, and a page left open through a dozen questions
  // would hold every one of them.
  useEffect(() => () => { setPending((p) => { p.forEach((x) => x.preview && URL.revokeObjectURL(x.preview)); return []; }); }, []);

  const uploading = pending.some((p) => !p.id && !p.error);
  const attached = pending.filter((p) => p.id);

  const dictate = () => {
    if (listening) { recog.current?.stop(); return; }
    const r = newRecognizer();
    if (!r) return;
    recog.current = r;
    r.lang = navigator.language || "en-US";
    r.continuous = true;
    r.interimResults = false;
    // Appended to whatever is already typed, because dictation is usually the
    // long half of a question whose first words were typed.
    r.onresult = (e) => {
      let said = "";
      for (let i = e.resultIndex; i < e.results.length; i++) {
        if (e.results[i].isFinal) said += e.results[i][0].transcript;
      }
      if (said.trim()) setText((t) => (t ? `${t.trimEnd()} ${said.trim()}` : said.trim()));
    };
    r.onerror = (ev) => {
      setListening(false);
      if (ev.error !== "aborted" && ev.error !== "no-speech") {
        setError(ev.error === "not-allowed" ? "The browser would not give the page the microphone." : `Dictation stopped: ${ev.error}`);
      }
    };
    r.onend = () => setListening(false);
    setListening(true);
    r.start();
  };
  useEffect(() => () => recog.current?.stop(), []);

  const send = async (e: FormEvent) => {
    e.preventDefault();
    const q = text.trim();
    if ((!q && attached.length === 0) || running || uploading) return;
    const files: AssistantAttachment[] = attached.map((p) => ({ name: p.name, path: p.path ?? "", size: p.size, type: p.type }));
    const ids = attached.map((p) => p.id!);
    setMsgs((m) => [...m, { role: "user", text: q, files: files.length ? files : undefined }]);
    setText("");
    // Cleared, not deleted: these files belong to the conversation now.
    pending.forEach((p) => p.preview && URL.revokeObjectURL(p.preview));
    setPending([]);
    setActivity([]);
    await follow((onEvent, signal) =>
      postNDJSON<AssistantEvent>("/api/v1/assistant/chat", onEvent,
        // The model goes with the first message only. After that the
        // conversation has one of its own and the daemon uses that, so
        // changing the picker cannot move a chat that is under way.
        { chatId: chatId ?? undefined, text: q, fileIds: ids.length ? ids : undefined, providerId: chatId ? undefined : (provider || undefined) }, signal));
  };

  // Which model a new conversation will use. Only asked when there is more
  // than one to ask about; with one configured the answer is never in doubt.
  // What the open conversation is using, for the line beside the title.
  const chatModel = models.find((m) => m.id === chats.find((c) => c.id === chatId)?.providerId)?.name ?? "";

  const newChat = () => {
    window.clearTimeout(retry.current);
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
    setProvider("");
  };

  const stop = async () => {
    window.clearTimeout(retry.current);
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

  const ready = cfg?.ready ?? false;

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
          {/* The picker is for the conversation that has not started yet. An
              open one already has a model, and changing it halfway through
              would make the transcript a record of two different things. */}
          {models.length > 1 && !chatId && (
            <Select value={provider} onChange={(e) => setProvider(e.target.value)} className="h-8 w-52 max-w-[45vw] text-xs" aria-label="Model for this conversation">
              {models.map((m) => <option key={m.id} value={m.id}>{m.name}{m.default ? " (default)" : ""}</option>)}
            </Select>
          )}
          {models.length > 1 && chatId && chatModel && <span className="hidden text-xs text-ink-muted sm:inline">{chatModel}</span>}
          {/* Only once there is something to answer with. The badge keyed off
              the config existing, which it does on a server with nothing set
              up — so a fresh install said "70 tools · anthropic" directly above
              "no assistant is configured yet". */}
          {ready && models.length <= 1 && cfg && <span className="hidden text-xs text-ink-muted sm:inline">{cfg.tools} tools · {models[0]?.name ?? cfg.provider}</span>}
          <Button type="button" variant="secondary" className="h-8 text-xs md:hidden" onClick={() => setListOpen((v) => !v)}>
            {listOpen ? "Close" : `Chats${chats.length ? ` (${chats.length})` : ""}`}
          </Button>
          <Button type="button" className="h-8 text-xs" onClick={newChat}>New</Button>
        </div>
      </div>

      {/* The grid below is a flex child with no margin of its own, so a banner
          here sat directly on top of the chat list. */}
      {!ready && (
        <div className="mb-3">
          <Alert tone="warning">
            No assistant is configured yet.{" "}
            {isAdmin
              ? <Link to="/settings?tab=ai" className="-my-1 inline-block py-1 underline">Set one up in Settings</Link>
              : "Ask an admin to set one up."}
          </Alert>
        </div>
      )}
      {error && <div className="mb-3"><Alert>{error}</Alert></div>}

      <div className="grid min-h-0 flex-1 grid-cols-1 gap-3 md:grid-cols-[minmax(0,15rem)_minmax(0,1fr)]">
        <aside className={`${listOpen ? "" : "hidden"} min-h-0 overflow-y-auto rounded-lg border border-border bg-surface p-1.5 md:block`}>
          {chats.length === 0 && <p className="px-2 py-3 text-xs text-ink-muted">No conversations yet.</p>}
          <ul className="space-y-0.5">
            {chats.map((c) => (
              <li key={c.id} className="group relative">
                <button
                  type="button"
                  onClick={() => void open(c.id)}
                  className={`w-full rounded-md px-2 py-1.5 pr-12 text-left ${c.id === chatId ? "bg-surface-2 text-ink" : "text-ink-muted hover:bg-surface-2 hover:text-ink"}`}
                  title={c.title || "Untitled"}
                >
                  <span className="block truncate text-xs">
                    {c.runId && <span aria-label="working" className="mr-1.5 inline-block size-1.5 animate-pulse rounded-full bg-success align-middle" />}
                    {c.title || "Untitled"}
                  </span>
                  <span className="mt-0.5 block text-[10px] text-ink-muted">
                    {c.runId ? "working…" : ago(c.updatedAt)}
                  </span>
                </button>
                <span className="absolute right-1 top-1/2 flex -translate-y-1/2 gap-1 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100">
                  <button type="button" onClick={() => void rename(c)} className="-my-1 px-1 py-1 text-[11px] text-ink-muted hover:text-ink" aria-label={`Rename ${c.title || "conversation"}`}>✎</button>
                  <button type="button" onClick={() => void remove(c)} className="-my-1 px-1 py-1 text-[11px] text-ink-muted hover:text-danger" aria-label={`Delete ${c.title || "conversation"}`}>✕</button>
                </span>
              </li>
            ))}
          </ul>
        </aside>

        {/* Dropping a file anywhere over the conversation attaches it. The
            counter, rather than a boolean: dragging over a child fires a leave
            for the parent, so a single flag flickered the outline off the
            moment the pointer crossed a message. */}
        <section
          className={`${listOpen ? "hidden" : "flex"} relative min-h-0 flex-col md:flex`}
          onDragOver={(e) => { if (e.dataTransfer.types.includes("Files")) { e.preventDefault(); setDragging(true); } }}
          onDragLeave={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragging(false); }}
          onDrop={(e) => {
            if (!e.dataTransfer.files.length) return;
            e.preventDefault();
            setDragging(false);
            addFiles(e.dataTransfer.files);
          }}
        >
          {dragging && (
            <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center rounded-lg border-2 border-dashed border-accent bg-bg/80 text-sm font-medium">
              Drop to attach
            </div>
          )}
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
            <Transcript msgs={msgs} />
            {activity.length > 0 && <Working items={activity} />}
            {running && activity.length === 0 && (
              <p className="text-sm text-ink-muted">{reconnecting ? "Picking up where it got to…" : "Thinking…"}</p>
            )}
            <div ref={foot} />
          </div>

          {/* Everything about to be sent, above the box it will be sent from.
              A name beside a paperclip is not enough to notice that the wrong
              photograph is attached, and noticing afterwards means it is
              already on the server and in a conversation. */}
          {pending.length > 0 && (
            <ul className="mt-2 flex flex-wrap gap-2">
              {pending.map((p) => <PendingFile key={p.key} file={p} onRemove={() => unattach(p.key)} />)}
            </ul>
          )}
          {pending.filter((p) => p.error).map((p) => (
            <p key={p.key} className="mt-1.5 text-xs text-danger">{p.error}</p>
          ))}
          {pending.length > 0 && scanner === "" && (
            <p className="mt-1.5 text-xs text-warning">
              Nothing on this server checks uploads for malware. Install ClamAV — <code>apt install clamav-daemon</code> — and it is used from then on.
            </p>
          )}

          <form onSubmit={send} className="mt-2 grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-2">
            <input
              ref={picker}
              type="file"
              multiple
              className="hidden"
              onChange={(e) => { addFiles(e.target.files); e.target.value = ""; }}
            />
            <button
              type="button"
              onClick={() => picker.current?.click()}
              disabled={!ready || running}
              aria-label="Attach files"
              title="Attach images, video, documents or a zip"
              className="-my-1 inline-flex h-9 items-center rounded-md px-2 text-ink-muted hover:bg-surface-2 hover:text-ink disabled:opacity-40"
            >
              <PaperclipIcon className="h-4 w-4" />
            </button>
            <Input
              value={text}
              onChange={(e) => setText(e.target.value)}
              // A screenshot pasted in is the fastest way there is to show
              // something, and every other box that takes files takes it.
              onPaste={(e) => { if (e.clipboardData.files.length) { e.preventDefault(); addFiles(e.clipboardData.files); } }}
              placeholder={ready ? (pending.length ? "Say what to do with these…" : "Ask for something, or drop in a file…") : "Configure an assistant first"}
              disabled={!ready || running}
              aria-label="Ask the assistant"
            />
            <div className="flex items-center gap-2">
              {canDictate && (
                <button
                  type="button"
                  onClick={dictate}
                  disabled={!ready || running}
                  aria-label={listening ? "Stop dictating" : "Dictate"}
                  aria-pressed={listening}
                  title={listening ? "Stop dictating" : "Dictate. Your browser does the listening, and may send the audio to its vendor"}
                  className={`-my-1 inline-flex h-9 items-center rounded-md px-2 disabled:opacity-40 ${listening ? "animate-pulse text-danger" : "text-ink-muted hover:bg-surface-2 hover:text-ink"}`}
                >
                  <MicIcon className="h-4 w-4" />
                </button>
              )}
              {running ? (
                <Button type="button" variant="secondary" className="h-9 text-xs" onClick={() => void stop()}>Stop</Button>
              ) : (
                <Button type="submit" className="h-9 text-xs" disabled={!ready || uploading || (!text.trim() && attached.length === 0)}>
                  {uploading ? "Uploading…" : "Ask"}
                </Button>
              )}
            </div>
          </form>
        </section>
      </div>
    </div>
  );
}

/**
 * One file in the composer: what it is, how far it has got, and a way to take
 * it off again.
 *
 * An image shows itself. Everything else shows an icon and its size, which is
 * the pair that actually distinguishes two files called "final.mp4" — a
 * thumbnail of a video is a frame nobody chose, and a document has no picture
 * at all.
 */
function PendingFile({ file, onRemove }: { file: Pending; onRemove: () => void }) {
  const Glyph = fileIcon(file.type);
  const done = Boolean(file.id);
  return (
    <li
      className={`relative flex w-40 items-center gap-2 overflow-hidden rounded-md border px-2 py-1.5 ${file.error ? "border-danger bg-danger-soft" : "border-border bg-surface"}`}
      title={file.error ? file.error : file.path ?? file.name}
    >
      {file.preview ? (
        <img src={file.preview} alt="" className="h-8 w-8 shrink-0 rounded-sm object-cover" />
      ) : (
        <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-sm bg-surface-2 text-ink-muted"><Glyph className="h-4 w-4" /></span>
      )}
      <span className="min-w-0 flex-1">
        <span className="block truncate text-xs">{file.name}</span>
        {/* Why it was refused is written under the strip, not in here: the
            reason names a virus signature or a size limit, and neither fits in
            a tile the width of a file name. */}
        <span className={`block truncate text-[10px] ${file.error ? "text-danger" : "text-ink-muted"}`}>
          {file.error ? "not attached" : done ? size(file.size) : `${Math.round(file.progress * 100)}%`}
        </span>
      </span>
      <button
        type="button"
        onClick={onRemove}
        aria-label={`Remove ${file.name}`}
        className="-my-1 shrink-0 rounded-sm p-1 text-ink-muted hover:text-danger"
      >
        <CloseIcon className="h-3 w-3" />
      </button>
      {/* The bar sits under the row rather than beside the name: a 40 MB video
          is a minute of nothing happening otherwise. It stays at full width
          while the scanner reads the file, which is after the bytes have all
          arrived and before the server answers. */}
      {!done && !file.error && (
        <span className="absolute inset-x-0 bottom-0 h-0.5 bg-surface-2">
          <span className="block h-full bg-accent transition-[width] duration-200" style={{ width: `${Math.max(3, file.progress * 100)}%` }} />
        </span>
      )}
    </li>
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
          <li key={i}><Markdown text={it.text} className="text-sm leading-relaxed text-ink-muted" /></li>
        ) : (
          <ToolLine key={it.id || i} name={it.name} input={it.input} ok={it.ok} ms={it.ms} output={it.ok === false ? it.preview : undefined} />
        ),
      )}
    </ul>
  );
}

// Arguments as one short line. A phone is 390 pixels wide and a wrapped
// argument pushed every tool call to three lines, which turned a run of nine
// into a wall.
function summarise(input: Record<string, unknown>) {
  const s = Object.entries(input)
    .map(([k, v]) => `${k}=${typeof v === "string" ? v : JSON.stringify(v)}`)
    .join(" ");
  return s.length > 64 ? s.slice(0, 64) + "…" : s;
}

function fmtMs(ms: number) {
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
}

/**
 * The conversation, with each turn's tool calls folded into the turn that asked
 * for them.
 *
 * The results arrive as a message of their own, which is how both wire formats
 * carry them, but reading them that way puts a wall of JSON between a question
 * and its answer. Here they belong to the turn that made the call.
 */
function Transcript({ msgs }: { msgs: AssistantMessage[] }) {
  const out = [];
  for (let i = 0; i < msgs.length; i++) {
    const m = msgs[i];
    // A results-only message is drawn by the turn before it, unless there was
    // none — which should not happen, and is still better shown than dropped.
    if (m.results?.length && i > 0 && msgs[i - 1].calls?.length) continue;
    out.push(<Turn key={i} m={m} answers={msgs[i + 1]?.results} />);
  }
  return <>{out}</>;
}

function Turn({ m, answers }: { m: AssistantMessage; answers?: AssistantToolResult[] }) {
  const mine = m.role === "user";
  if (mine && !m.results?.length) {
    return (
      <div className="flex flex-col items-end gap-1.5">
        {/* Files first: they are the thing the sentence refers to. The path is
            shown because it is what the assistant was actually given, and
            because it is what you would type to use the file yourself. */}
        {m.files?.length ? (
          <ul className="flex max-w-[85%] flex-wrap justify-end gap-1.5">
            {m.files.map((f, i) => {
              const Glyph = fileIcon(f.type ?? "");
              return (
                <li key={i} className="flex min-w-0 items-center gap-1.5 rounded-md border border-border bg-surface-2 px-2 py-1 text-xs" title={f.path}>
                  <Glyph className="h-3.5 w-3.5 shrink-0 text-ink-muted" />
                  <span className="truncate">{f.name}</span>
                  {f.size ? <span className="shrink-0 text-ink-faint">{size(f.size)}</span> : null}
                </li>
              );
            })}
          </ul>
        ) : null}
        {m.text && <div className="max-w-[85%] whitespace-pre-wrap rounded-lg bg-ink px-3 py-2 text-sm text-on-ink">{m.text}</div>}
      </div>
    );
  }
  const results = m.results ?? answers;
  return (
    <div className="space-y-1.5">
      {/* What this turn did, for a provider that ran its own loop. It comes
          before the prose because that is the order it happened in. */}
      {m.tools?.length ? (
        <ul className="space-y-1">
          {m.tools.map((t, i) => <ToolLine key={i} name={t.name} input={t.input} ok={t.ok} ms={t.ms} output={t.output} />)}
        </ul>
      ) : null}
      {m.text && <Markdown text={m.text} className="max-w-[90%] text-sm leading-relaxed" />}
      {m.calls?.length ? (
        <ul className="space-y-1">
          {m.calls.map((c) => {
            const r = results?.find((x) => x.callId === c.id);
            return <ToolLine key={c.id} name={c.name} input={c.input} ok={r ? !r.isError : undefined} output={r?.content} />;
          })}
        </ul>
      ) : null}
      {/* Results with no call to hang them on: shown rather than lost. */}
      {!m.calls?.length && m.results?.length ? (
        <ul className="space-y-1">
          {m.results.map((r) => <ToolLine key={r.callId} name="tool" ok={!r.isError} output={r.content} />)}
        </ul>
      ) : null}
    </div>
  );
}

/**
 * One tool, as a line you can open.
 *
 * Tool output used to be printed in full: a container listing is two hundred
 * lines of JSON between one sentence and the next, and on a phone it was the
 * whole screen. The line says what ran and how it went; the output is a click
 * away — except a refusal, which opens itself, because it names the scope that
 * was missing and that is the most useful thing on the screen when it happens.
 */
function ToolLine({ name, input, ok, ms, output }: { name: string; input?: Record<string, unknown>; ok?: boolean; ms?: number; output?: string }) {
  const mark = ok === undefined ? "◌" : ok ? "✓" : "✕";
  const tone = ok === undefined ? "text-ink-muted" : ok ? "text-success" : "text-warning";
  const head = (
    <>
      <span aria-hidden className={tone}>{mark}</span>
      <span className={ok === false ? "text-warning" : "text-ink"}>{name}</span>
      {input && Object.keys(input).length > 0 && <span className="min-w-0 truncate text-ink-muted">{summarise(input)}</span>}
      {ms !== undefined && <span className="text-ink-muted">{fmtMs(ms)}</span>}
    </>
  );
  if (!output) {
    return <li className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 font-mono text-[11px]">{head}</li>;
  }
  return (
    <li>
      <details open={ok === false} className="group">
        <summary className="-my-1 flex cursor-pointer flex-wrap items-baseline gap-x-2 gap-y-0.5 py-1 font-mono text-[11px] marker:content-['']">
          {head}
          <span className="text-ink-muted underline decoration-dotted group-open:hidden">show</span>
        </summary>
        <pre className={`mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-md border px-2.5 py-1.5 font-mono text-[11px] ${ok === false ? "border-warning/40 bg-warning-soft text-warning" : "border-border text-ink-muted"}`}>{output}</pre>
      </details>
    </li>
  );
}

/** How long ago, in as few characters as a list can spare. */
function ago(iso: string): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return "";
  const s = Math.max(0, (Date.now() - then) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  if (s < 7 * 86400) return `${Math.floor(s / 86400)}d ago`;
  return new Date(then).toLocaleDateString();
}
