import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { api, RequestError, type Workspace, type WorkspaceMCP } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useDialog, failure } from "@/lib/dialogs";
import { postStream } from "@/lib/stream";
import { pollInterval } from "@/lib/poll";
import { Alert, Button, Card, Field, Input, Select } from "@/components/ui";
import { ExternalIcon, TrashIcon } from "@/components/icons";
import { openConsole } from "./Console";
// Imported directly, as Terminal and Console do. Behind Suspense the card
// renders at nothing-height first, and the scroll below then runs against a
// page that has nothing to scroll yet.
import TermView from "@/components/TermView";

const PRESETS: Record<Workspace["preset"], { label: string; blurb: string; command: string }> = {
  claude: {
    label: "Claude Code",
    blurb: "Runs `claude` in the directory. Sign in once inside the session and it stays signed in.",
    command: "claude",
  },
  shell: {
    label: "A shell",
    blurb: "Just a prompt that stays where you left it. Good for a long build, a migration, or anything you want to walk away from.",
    command: "",
  },
  custom: {
    label: "Something else",
    blurb: "Any command. It is typed into the session, so you can see it and run it again.",
    command: "",
  },
};

const EMPTY: Workspace = {
  id: "", name: "", directory: "", preset: "claude", command: "claude", mcpEnabled: true, skipPermissions: false,
  createdAt: "", updatedAt: "", lastAttachedAt: "", running: false,
};

function ago(ts: string) {
  if (!ts) return "never";
  const s = Math.max(0, (Date.now() - new Date(ts).getTime()) / 1000);
  if (s < 90) return "just now";
  if (s < 5400) return `${Math.round(s / 60)} min ago`;
  if (s < 172800) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} days ago`;
}

export default function Workspaces() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const ask = useDialog();

  const [list, setList] = useState<Workspace[]>([]);
  const [tmux, setTmux] = useState(true);
  const [claude, setClaude] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [editing, setEditing] = useState<Workspace | null>(null);
  const [open, setOpen] = useState<Workspace | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [installing, setInstalling] = useState<string[] | null>(null);
  const termRef = useRef<HTMLDivElement>(null);

  // Opening a workspace puts the terminal below the list, which is off the
  // bottom of the screen as soon as there are a few of them — and a button that
  // appears to do nothing is a button people press twice.
  useEffect(() => {
    if (open) termRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
  }, [open?.id]);

  const load = useCallback(async () => {
    try {
      const r = await api.workspaces();
      setList(r.workspaces);
      setTmux(r.tmux);
      setClaude(r.claude);
      setErr(null);
      setOpen((o) => (o ? r.workspaces.find((w) => w.id === o.id) ?? null : null));
    } catch (e) {
      setErr(e instanceof RequestError ? e.message : String(e));
    }
  }, []);
  useEffect(() => { void load(); return pollInterval(() => void load(), 15000); }, [load]);

  if (!isAdmin) {
    return (
      <div className="mx-auto max-w-2xl">
        <Alert>Workspaces are for admins. Opening one is a shell on this server.</Alert>
      </div>
    );
  }

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!editing) return;
    setBusy("save");
    try {
      const saved = await api.workspaceSave(editing);
      setEditing(null);
      await load();
      if (saved.error) void ask.alert({ title: "Saved, with one thing left", body: saved.error });
    } catch (e2) {
      void ask.alert({ title: "Could not save the workspace", body: failure(e2), tone: "danger" });
    } finally { setBusy(null); }
  };

  const act = async (w: Workspace, fn: () => Promise<unknown>, key: string) => {
    setBusy(w.id + key);
    try { await fn(); await load(); }
    catch (e) { void ask.alert({ title: "That did not work", body: failure(e), tone: "danger" }); }
    finally { setBusy(null); }
  };

  const remove = async (w: Workspace) => {
    const ok = await ask.confirm({
      title: `Remove ${w.name}?`,
      body: (
        <div className="space-y-2">
          <p>The session is killed and anything running in it stops. Its Islet access is revoked.</p>
          <p className="text-ink-muted">The directory {w.directory} and everything in it is left exactly as it is.</p>
        </div>
      ),
      confirmLabel: "Remove it", tone: "danger", typeToConfirm: w.name,
    });
    if (!ok) return;
    await act(w, () => api.workspaceDelete(w.id), "rm");
    setOpen((o) => (o?.id === w.id ? null : o));
  };

  const install = async (what: "tmux" | "claude") => {
    setInstalling([]);
    try {
      await postStream(`/api/v1/workspaces/${what}`, (l) => setInstalling((o) => [...(o ?? []).slice(-200), l]));
      await load();
    } catch (e) {
      void ask.alert({ title: `Could not install ${what}`, body: failure(e), tone: "danger" });
    } finally { setInstalling(null); }
  };

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-[-0.02em]">Workspaces</h1>
          <p className="mt-1 text-ink-muted">
            A directory and a session that keeps running when you close the tab — for an agent, or for anything long.
          </p>
        </div>
        {tmux && <Button className="h-8 text-xs" onClick={() => setEditing({ ...EMPTY })}>New workspace</Button>}
      </div>

      {err && <Alert>{err}</Alert>}

      {!tmux && (
        <Card title="tmux is not installed" description="A workspace is a tmux session, so this is the one thing it needs.">
          <p className="text-sm text-ink-muted">
            tmux is what keeps the work alive: the command runs as a child of tmux rather than of Islet, so it survives
            a closed browser, a dropped connection and an Islet update. You can also reach the same session over SSH
            with <span className="font-mono">tmux attach</span>.
          </p>
          <Button className="mt-3 h-8 text-xs" disabled={installing !== null} onClick={() => void install("tmux")}>
            {installing !== null ? "Installing…" : "Install tmux"}
          </Button>
          {installing !== null && (
            <pre className="mt-3 max-h-56 overflow-auto rounded-lg border border-border bg-code-bg p-3 font-mono text-xs text-code-fg">
              {installing.join("\n") || "…"}
            </pre>
          )}
        </Card>
      )}

      {tmux && !claude && list.some((w) => w.preset === "claude") && (
        <Card
          title="Claude Code is not installed on this server"
          description="A workspace with the Claude Code preset has nothing to run until it is."
        >
          <p className="text-sm text-ink-muted">
            This installs it with Anthropic's own installer, falling back to npm. It lands in{" "}
            <span className="font-mono">~/.local/bin</span>, which a fresh shell may not have on its PATH — Islet runs it
            by its full path, so that does not matter here.
          </p>
          <Button className="mt-3 h-8 text-xs" disabled={installing !== null} onClick={() => void install("claude")}>
            {installing !== null ? "Installing…" : "Install Claude Code"}
          </Button>
          <p className="mt-3 text-xs text-ink-muted">
            Or run it yourself: <span className="font-mono">curl -fsSL https://claude.ai/install.sh | bash</span>
          </p>
          {installing !== null && (
            <pre className="mt-3 max-h-56 overflow-auto rounded-lg border border-border bg-code-bg p-3 font-mono text-xs text-code-fg">
              {installing.join("\n") || "…"}
            </pre>
          )}
        </Card>
      )}

      {tmux && list.length === 0 && !editing && (
        <div className="rounded-lg border border-dashed border-border p-8 text-center">
          <p className="text-sm text-ink-muted">
            No workspaces yet. Make one for a project directory and it will be waiting where you left it.
          </p>
          <Button className="mt-3 h-8 text-xs" onClick={() => setEditing({ ...EMPTY })}>New workspace</Button>
        </div>
      )}

      {list.length > 0 && (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          {list.map((w) => (
            <Card key={w.id} className={open?.id === w.id ? "border-ink" : undefined}>
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className={`h-2 w-2 shrink-0 rounded-full ${w.running ? "bg-success" : "bg-ink-faint"}`} />
                    <span className="truncate font-semibold">{w.name}</span>
                    <span className="shrink-0 rounded-sm bg-surface-2 px-1 text-[10px] text-ink-muted">
                      {PRESETS[w.preset].label}
                    </span>
                  </div>
                  <div className="mt-1 truncate font-mono text-[11px] text-ink-faint" title={w.directory}>{w.directory}</div>
                  <div className="mt-1 text-xs text-ink-muted">
                    {w.running ? "Running" : "Stopped"} · opened {ago(w.lastAttachedAt)}
                    {w.mcpEnabled && <span className="ml-2 text-ink-faint">connected to Islet</span>}
                  </div>
                </div>
                <Button className="h-8 shrink-0 px-2.5 text-xs" onClick={() => setOpen(w)}>Open</Button>
              </div>
              <div className="mt-3 flex flex-wrap items-center gap-3 text-xs">
                {w.command && (
                  <button type="button" disabled={busy === w.id + "start"} onClick={() => void act(w, () => api.workspaceStart(w.id), "start")}
                    className="-my-1 py-1 text-accent hover:underline">
                    {busy === w.id + "start" ? "Starting…" : `Run ${w.command}`}
                  </button>
                )}
                {w.running && (
                  <button type="button" disabled={busy === w.id + "stop"} onClick={() => void act(w, () => api.workspaceStop(w.id), "stop")}
                    className="-my-1 py-1 text-ink-muted hover:text-ink">
                    {busy === w.id + "stop" ? "Stopping…" : "Stop"}
                  </button>
                )}
                <button type="button" onClick={() => openConsole({ workspace: w.id })}
                  className="-my-1 inline-flex items-center gap-1 py-1 text-ink-muted hover:text-ink">
                  <ExternalIcon className="h-3.5 w-3.5" />Own window
                </button>
                <button type="button" onClick={() => setEditing(w)} className="-my-1 py-1 text-ink-muted hover:text-ink">Edit</button>
                <button type="button" onClick={() => void remove(w)}
                  className="-my-1 ml-auto inline-flex items-center gap-1 py-1 text-danger hover:underline">
                  <TrashIcon className="h-3.5 w-3.5" />Remove
                </button>
              </div>
            </Card>
          ))}
        </div>
      )}

      {editing && <Editor w={editing} onChange={setEditing} onSubmit={save} busy={busy === "save"} onCancel={() => setEditing(null)} />}

      {open && (
        <div ref={termRef}>
        <Card
          title={open.name}
          description={`${open.directory}${open.running ? "" : " · the session is not running; opening it starts one"}`}
        >
          {/* reattaches: the session lives in tmux, so a dropped connection
              costs nothing and reconnecting is safe to do automatically. */}
          <TermView path={`/api/v1/workspaces/${open.id}/attach`} reattaches className="h-[60vh]" />
          {open.preset === "claude" && !open.lastAttachedAt && (
            <p className="mt-2 rounded-md border border-border bg-surface-2 p-2 text-xs text-ink-muted">
              First time here: type <span className="font-mono">{open.command || "claude"}</span> or press
              <span className="font-mono"> Run</span>, and sign in when it asks. It prints a link — open it on any
              device, approve, and paste the code back. Your subscription signs in the same way it does on a laptop,
              and it stays signed in afterwards.
            </p>
          )}
          <div className="mt-2 flex flex-wrap items-center gap-3 text-xs text-ink-muted">
            <span>Detach by closing this — the session keeps running.</span>
            <button type="button" onClick={() => setOpen(null)} className="-my-1 py-1 hover:text-ink">Close</button>
            {open.mcpEnabled && <McpNote id={open.id} />}
          </div>
        </Card>
        </div>
      )}
    </div>
  );
}

/** What the agent can reach, and where its credential lives. */
function McpNote({ id }: { id: string }) {
  const [m, setM] = useState<WorkspaceMCP | null>(null);
  useEffect(() => { void api.workspaceMcp(id).then(setM).catch(() => {}); }, [id]);
  if (!m) return null;
  return (
    <span className="text-ink-faint" title={`Credential at ${m.path} — kept outside the project so it cannot be committed.`}>
      {m.tools} Islet tools available ({m.scopes.join(", ")})
    </span>
  );
}

function Editor({ w, onChange, onSubmit, onCancel, busy }: {
  w: Workspace; onChange: (w: Workspace) => void; onSubmit: (e: FormEvent) => void; onCancel: () => void; busy: boolean;
}) {
  const preset = PRESETS[w.preset];
  return (
    <Card title={w.id ? `Edit ${w.name}` : "New workspace"} description={preset.blurb}>
      <form onSubmit={onSubmit} className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <Field label="Name" hint="Lowercase letters, digits and dashes.">
          <Input value={w.name} onChange={(e) => onChange({ ...w, name: e.target.value })} placeholder="poolse" required />
        </Field>
        <Field label="Directory" hint="An absolute path on this server. The session starts here.">
          <Input value={w.directory} onChange={(e) => onChange({ ...w, directory: e.target.value })} placeholder="/var/www/server" required />
        </Field>
        <Field label="What runs here">
          <Select value={w.preset} onChange={(e) => {
            const p = e.target.value as Workspace["preset"];
            onChange({ ...w, preset: p, command: PRESETS[p].command, mcpEnabled: p === "shell" ? false : w.mcpEnabled });
          }}>
            {(Object.keys(PRESETS) as Workspace["preset"][]).map((p) => <option key={p} value={p}>{PRESETS[p].label}</option>)}
          </Select>
        </Field>
        {w.preset !== "shell" && (
          <Field label="Command" hint="Typed into the session when you press Run, so you can see and repeat it.">
            <Input value={w.command} onChange={(e) => onChange({ ...w, command: e.target.value })} placeholder="claude" required />
          </Field>
        )}
        {w.preset === "claude" && (
          <div className="md:col-span-2">
            <label className="flex items-start gap-2 text-sm">
              <input type="checkbox" className="mt-1" checked={w.skipPermissions ?? false}
                onChange={(e) => onChange({ ...w, skipPermissions: e.target.checked })} />
              <span>
                Let it act without asking
                <span className="mt-0.5 block text-xs text-ink-muted">
                  Adds <span className="font-mono">--dangerously-skip-permissions</span>, so it edits files and runs
                  commands without stopping for approval. That is the point of leaving one running while you are away,
                  and it is also a shell on a machine serving real sites — turn it on for a workspace you would hand
                  the keys to, not by default.
                </span>
              </span>
            </label>
          </div>
        )}
        {w.preset !== "shell" && (
          <div className="md:col-span-2">
            <label className="flex items-start gap-2 text-sm">
              <input type="checkbox" className="mt-1" checked={w.mcpEnabled}
                onChange={(e) => onChange({ ...w, mcpEnabled: e.target.checked })} />
              <span>
                Let it ask Islet about this server
                <span className="mt-0.5 block text-xs text-ink-muted">
                  Gives the agent a scoped, revocable token so it can read container logs, restart a container, run a
                  job and message you — instead of working it out as root. The credential is stored outside your
                  project, so it cannot be committed by accident.
                </span>
              </span>
            </label>
          </div>
        )}
        <div className="flex items-center gap-2 md:col-span-2">
          <Button type="submit" disabled={busy}>{busy ? "Saving…" : "Save"}</Button>
          <Button type="button" variant="secondary" onClick={onCancel}>Cancel</Button>
        </div>
      </form>
    </Card>
  );
}
