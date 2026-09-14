import { useCallback, useEffect, useMemo, useState, type FormEvent, type ReactNode } from "react";
import { api, RequestError, type Agent, type Workspace } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useDialog, failure } from "@/lib/dialogs";
import { postStream } from "@/lib/stream";
import { pollInterval } from "@/lib/poll";
import { Alert, Button, Card, Field, Input, Select } from "@/components/ui";
import { ExternalIcon, PlayIcon, PlusIcon, RefreshIcon, StopIcon, TrashIcon, RenameIcon } from "@/components/icons";
import { openConsole } from "./Console";
// Imported directly, as Terminal and Console do. Behind Suspense the pane
// renders at nothing-height first, and anything that measures it then measures
// a box that is not there yet.
import TermView from "@/components/TermView";

/**
 * A workspace is a directory and a tmux session; the agents inside it are tmux
 * windows. The page is built around that rather than around a list, because the
 * thing people do here is watch one agent while another works — so the terminal
 * is the page, and everything else is a way of choosing what it shows.
 */

const PRESETS: Record<Agent["preset"], { label: string; blurb: string; command: string }> = {
  claude: {
    label: "Claude Code",
    blurb: "Runs `claude` in the workspace directory, in a conversation of its own.",
    command: "claude",
  },
  shell: {
    label: "A shell",
    blurb: "A prompt that stays where you left it. For a long build, a migration, anything you want to walk away from.",
    command: "",
  },
  custom: {
    label: "Something else",
    blurb: "Any command. It is typed into the window, so you can see it and run it again.",
    command: "",
  },
};

const EMPTY_WS: Workspace = {
  id: "", name: "", directory: "", preset: "shell", command: "", mcpEnabled: true, skipPermissions: false,
  createdAt: "", updatedAt: "", lastAttachedAt: "", running: false,
};

const EMPTY_AGENT: Partial<Agent> = {
  id: "", name: "", preset: "claude", command: "claude", resume: true, skipPermissions: false,
};

/** The workspace's own shell window, which is not an agent and has no row. */
const SHELL = "shell";

function ago(ts: string) {
  if (!ts) return "never";
  const s = Math.max(0, (Date.now() - new Date(ts).getTime()) / 1000);
  if (s < 90) return "just now";
  if (s < 5400) return `${Math.round(s / 60)} min ago`;
  if (s < 172800) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} days ago`;
}

/**
 * Three states, not two. Filled and animated is running; filled and still is a
 * window sitting at a prompt after its command exited; hollow has never been
 * started. Conflating the middle one with "stopped" is how you end up pressing
 * Start on something that is already there.
 */
function Dot({ agent }: { agent: Agent }) {
  const title = agent.running ? `running ${agent.doing ?? ""}`.trim() : agent.present ? "idle at a prompt" : "not started";
  return (
    <span
      title={title}
      aria-label={title}
      className={
        "inline-block h-2 w-2 shrink-0 rounded-full " +
        (agent.running
          ? "animate-pulse bg-success"
          : agent.present
            ? "bg-ink-muted"
            : "border border-border bg-transparent")
      }
    />
  );
}

/** One icon button, with the name it would have had as text. */
function IconButton({ label, onClick, disabled = false, danger = false, children }: {
  label: string; onClick: () => void; disabled?: boolean; danger?: boolean; children: ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={label}
      aria-label={label}
      className={
        "inline-flex h-7 w-7 items-center justify-center rounded-md text-ink-muted transition-colors disabled:opacity-40 " +
        (danger ? "hover:bg-surface-2 hover:text-danger" : "hover:bg-surface-2 hover:text-ink")
      }
    >
      {children}
    </button>
  );
}

export default function Workspaces() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const ask = useDialog();

  const [list, setList] = useState<Workspace[]>([]);
  const [tmux, setTmux] = useState(true);
  const [claude, setClaude] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [wsID, setWsID] = useState<string | null>(null);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [tab, setTab] = useState<string>(SHELL); // an agent id, or SHELL
  const [editingWs, setEditingWs] = useState<Workspace | null>(null);
  const [editingAgent, setEditingAgent] = useState<Partial<Agent> | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [installing, setInstalling] = useState<string[] | null>(null);

  const ws = useMemo(() => list.find((w) => w.id === wsID) ?? null, [list, wsID]);
  const agent = useMemo(() => agents.find((a) => a.id === tab) ?? null, [agents, tab]);

  const loadList = useCallback(async () => {
    try {
      const r = await api.workspaces();
      setList(r.workspaces);
      setTmux(r.tmux);
      setClaude(r.claude);
      setErr(null);
      setWsID((cur) => (cur && r.workspaces.some((w) => w.id === cur) ? cur : r.workspaces[0]?.id ?? null));
    } catch (e) {
      setErr(e instanceof RequestError ? e.message : String(e));
    }
  }, []);

  const loadAgents = useCallback(async (id: string | null) => {
    if (!id) { setAgents([]); return; }
    try { setAgents(await api.agents(id)); } catch { /* the workspace card says why */ }
  }, []);

  useEffect(() => { void loadList(); return pollInterval(() => void loadList(), 15000); }, [loadList]);
  useEffect(() => { void loadAgents(wsID); }, [wsID, loadAgents]);
  // Live state comes from tmux, so it is polled rather than pushed. Ten seconds
  // is often enough to see an agent finish without making the box busy.
  useEffect(() => {
    if (!wsID) return;
    return pollInterval(() => void loadAgents(wsID), 10000);
  }, [wsID, loadAgents]);
  // Switching workspace lands on its shell, never on an agent of the one before.
  useEffect(() => { setTab(SHELL); }, [wsID]);
  // Attaching is what creates the session, so the state fetched a moment ago
  // says "session down" beside a terminal that is plainly up. Ask again once
  // the attach has had time to land, rather than leaving the header wrong for
  // as long as the poll interval.
  useEffect(() => {
    if (!wsID) return;
    const t = setTimeout(() => { void loadList(); void loadAgents(wsID); }, 1500);
    return () => clearTimeout(t);
  }, [wsID, tab, loadList, loadAgents]);

  if (!isAdmin) {
    return (
      <div className="mx-auto max-w-2xl">
        <Alert>Workspaces are for admins. Opening one is a shell on this server.</Alert>
      </div>
    );
  }

  const run = async (key: string, fn: () => Promise<unknown>) => {
    setBusy(key);
    try { await fn(); await loadList(); await loadAgents(wsID); }
    catch (e) { void ask.alert({ title: "That did not work", body: failure(e), tone: "danger" }); }
    finally { setBusy(null); }
  };

  // ---- the things that end something, each behind a question ---------------

  const stopAgent = async (a: Agent) => {
    const ok = await ask.confirm({
      title: `Stop ${a.name}?`,
      body: (
        <div className="space-y-2">
          <p>Its window closes and {a.preset === "claude" ? "Claude Code" : "whatever is running in it"} stops{a.running && a.doing ? ` — ${a.doing} is running right now` : ""}.</p>
          <p className="text-ink-muted">
            {a.resume
              ? "The conversation is kept. Starting it again comes back to this exact one."
              : "Resume is off for this agent, so starting it again begins an empty conversation."}
          </p>
        </div>
      ),
      confirmLabel: "Stop it", tone: "danger",
    });
    if (ok) await run(a.id + "stop", () => api.agentStop(ws!.id, a.id));
  };

  const restartAgent = async (a: Agent) => {
    const ok = await ask.confirm({
      title: `Restart ${a.name}?`,
      body: (
        <div className="space-y-2">
          <p>What is running now is stopped first{a.running && a.doing ? ` — that is ${a.doing}` : ""}.</p>
          <p className="text-ink-muted">
            {a.resume ? "It comes straight back into the same conversation." : "Resume is off, so it starts an empty conversation."}
          </p>
        </div>
      ),
      confirmLabel: "Restart it", tone: "danger",
    });
    if (!ok) return;
    await run(a.id + "restart", async () => {
      await api.agentStop(ws!.id, a.id);
      await api.agentStart(ws!.id, a.id);
    });
  };

  const removeAgent = async (a: Agent) => {
    const ok = await ask.confirm({
      title: `Remove ${a.name}?`,
      body: (
        <div className="space-y-2">
          <p>The window closes, whatever is running stops, and the agent is gone from this workspace.</p>
          <p className="text-ink-muted">Its conversation stays on disk and can still be reached with `claude --resume`; nothing in the directory is touched.</p>
        </div>
      ),
      confirmLabel: "Remove it", tone: "danger", typeToConfirm: a.name,
    });
    if (!ok) return;
    await run(a.id + "rm", () => api.agentDelete(ws!.id, a.id));
    setTab(SHELL);
  };

  const stopWorkspace = async (w: Workspace) => {
    const live = agents.filter((a) => a.running).map((a) => a.name);
    const ok = await ask.confirm({
      title: `Stop ${w.name}?`,
      body: (
        <div className="space-y-2">
          <p>This kills the whole session: every agent in it and the shell, all at once.</p>
          {live.length > 0
            ? <p className="text-warning">Running right now: {live.join(", ")}.</p>
            : <p className="text-ink-muted">No agent is running at the moment.</p>}
          <p className="text-ink-muted">Agents set to resume come back into their conversations when you start them again.</p>
        </div>
      ),
      confirmLabel: "Stop everything", tone: "danger", typeToConfirm: w.name,
    });
    if (ok) await run(w.id + "stop", () => api.workspaceStop(w.id));
  };

  const removeWorkspace = async (w: Workspace) => {
    const ok = await ask.confirm({
      title: `Remove ${w.name}?`,
      body: (
        <div className="space-y-2">
          <p>The session is killed, every agent in it stops, and its Islet access is revoked.</p>
          <p className="text-ink-muted">The directory {w.directory} and everything in it is left exactly as it is.</p>
        </div>
      ),
      confirmLabel: "Remove it", tone: "danger", typeToConfirm: w.name,
    });
    if (!ok) return;
    await run(w.id + "rm", () => api.workspaceDelete(w.id));
    setWsID(null);
  };

  // ---- saving --------------------------------------------------------------

  const saveWs = async (e: FormEvent) => {
    e.preventDefault();
    if (!editingWs) return;
    setBusy("ws-save");
    try {
      const saved = await api.workspaceSave(editingWs);
      setEditingWs(null);
      await loadList();
      setWsID(saved.id);
      if (saved.error) void ask.alert({ title: "Saved, with one thing left", body: saved.error });
    } catch (e2) {
      void ask.alert({ title: "Could not save the workspace", body: failure(e2), tone: "danger" });
    } finally { setBusy(null); }
  };

  const saveAgent = async (e: FormEvent) => {
    e.preventDefault();
    if (!editingAgent || !ws) return;
    setBusy("agent-save");
    try {
      const saved = await api.agentSave(ws.id, editingAgent);
      setEditingAgent(null);
      await loadAgents(ws.id);
      setTab(saved.id);
    } catch (e2) {
      void ask.alert({ title: "Could not save the agent", body: failure(e2), tone: "danger" });
    } finally { setBusy(null); }
  };

  const install = async (what: "tmux" | "claude") => {
    setInstalling([]);
    try {
      await postStream(`/api/v1/workspaces/${what}`, (l) => setInstalling((o) => [...(o ?? []).slice(-200), l]));
      await loadList();
    } catch (e) {
      void ask.alert({ title: `Could not install ${what}`, body: failure(e), tone: "danger" });
    } finally { setInstalling(null); }
  };

  const termPath = ws
    ? agent
      ? `/api/v1/workspaces/${ws.id}/agents/${agent.id}/attach`
      : `/api/v1/workspaces/${ws.id}/attach`
    : "";

  return (
    <div className="mx-auto max-w-[110rem] space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-[-0.02em]">Workspaces</h1>
          <p className="mt-1 text-ink-muted">
            Agents that keep working when you close the tab. Several to a workspace, each in its own conversation.
          </p>
        </div>
        {tmux && (
          <Button className="h-8 gap-1.5 px-2.5 text-xs" onClick={() => setEditingWs({ ...EMPTY_WS })}>
            <PlusIcon className="h-3.5 w-3.5" />New workspace
          </Button>
        )}
      </div>

      {err && <Alert>{err}</Alert>}

      {!tmux && (
        <Card title="tmux is not installed" description="Everything here is a tmux session, so this is the one thing that has to be there first.">
          <Button onClick={() => void install("tmux")} disabled={installing !== null}>
            {installing ? "Installing…" : "Install tmux"}
          </Button>
          {installing && <pre className="mt-3 max-h-60 overflow-auto rounded-md bg-code-bg p-3 text-xs">{installing.join("\n")}</pre>}
        </Card>
      )}

      {tmux && list.length === 0 && !editingWs && (
        <Card title="No workspaces yet" description="A workspace is a directory on this server and a session that outlives the browser.">
          <p className="max-w-prose text-ink-muted">
            Point one at a checkout, add a Claude Code agent to it, and the work carries on while you are away — through a
            closed laptop, a dropped connection, and an <code>islet update</code>.
          </p>
          <Button className="mt-3" onClick={() => setEditingWs({ ...EMPTY_WS })}>New workspace</Button>
        </Card>
      )}

      {tmux && list.length > 0 && (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-[15rem_minmax(0,1fr)]">
          {/* ---- the workspaces themselves ---- */}
          <nav aria-label="Workspaces" className="rounded-lg border border-border bg-surface p-1.5">
            <ul className="flex gap-1 overflow-x-auto lg:block lg:space-y-0.5 lg:overflow-visible">
              {list.map((w) => (
                <li key={w.id} className="shrink-0 lg:shrink">
                  <button
                    type="button"
                    onClick={() => setWsID(w.id)}
                    aria-current={w.id === wsID ? "true" : undefined}
                    className={
                      "flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-left text-sm transition-colors " +
                      (w.id === wsID ? "bg-surface-2 text-ink" : "text-ink-muted hover:bg-surface-2 hover:text-ink")
                    }
                  >
                    <span className={"inline-block h-1.5 w-1.5 shrink-0 rounded-full " + (w.running ? "bg-success" : "bg-border")} />
                    <span className="min-w-0 flex-1 truncate">{w.name}</span>
                  </button>
                </li>
              ))}
            </ul>
          </nav>

          {/* ---- the workspace on screen ---- */}
          {ws && (
            <section className="min-w-0 space-y-3">
              <header className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-surface px-3 py-2">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <h2 className="truncate font-semibold">{ws.name}</h2>
                    <span className="text-xs text-ink-muted">{ws.running ? "session up" : "session down"}</span>
                  </div>
                  <p className="truncate font-mono text-xs text-ink-muted">{ws.directory}</p>
                </div>
                <div className="flex items-center gap-1">
                  <IconButton label="Open in a separate window" onClick={() => openConsole({ workspace: ws.id, agent: agent?.id })}>
                    <ExternalIcon className="h-4 w-4" />
                  </IconButton>
                  <IconButton label={`Edit ${ws.name}`} onClick={() => setEditingWs({ ...ws })}>
                    <RenameIcon className="h-4 w-4" />
                  </IconButton>
                  <IconButton label={`Stop ${ws.name}`} danger disabled={!ws.running || busy !== null} onClick={() => void stopWorkspace(ws)}>
                    <StopIcon className="h-4 w-4" />
                  </IconButton>
                  <IconButton label={`Remove ${ws.name}`} danger disabled={busy !== null} onClick={() => void removeWorkspace(ws)}>
                    <TrashIcon className="h-4 w-4" />
                  </IconButton>
                </div>
              </header>

              {!claude && agents.some((a) => a.preset === "claude") && (
                <Alert tone="warning">
                  Claude Code is not installed on this server, so its agents cannot start.{" "}
                  <button type="button" className="underline" onClick={() => void install("claude")}>Install it</button>
                  {installing && <pre className="mt-2 max-h-40 overflow-auto rounded-md bg-code-bg p-2 text-xs">{installing.join("\n")}</pre>}
                </Alert>
              )}

              {/* ---- agent tabs ---- */}
              <div className="flex flex-wrap items-center gap-1 rounded-lg border border-border bg-surface p-1.5">
                <button
                  type="button"
                  onClick={() => setTab(SHELL)}
                  aria-current={tab === SHELL ? "true" : undefined}
                  className={
                    "inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs transition-colors " +
                    (tab === SHELL ? "bg-surface-2 text-ink" : "text-ink-muted hover:bg-surface-2 hover:text-ink")
                  }
                >
                  shell
                </button>
                {agents.map((a) => (
                  <button
                    key={a.id}
                    type="button"
                    onClick={() => setTab(a.id)}
                    aria-current={tab === a.id ? "true" : undefined}
                    className={
                      "inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs transition-colors " +
                      (tab === a.id ? "bg-surface-2 text-ink" : "text-ink-muted hover:bg-surface-2 hover:text-ink")
                    }
                  >
                    <Dot agent={a} />
                    {a.name}
                  </button>
                ))}
                <button
                  type="button"
                  onClick={() => setEditingAgent({ ...EMPTY_AGENT })}
                  className="inline-flex items-center gap-1 rounded-md px-2 py-1.5 text-xs text-ink-muted hover:bg-surface-2 hover:text-ink"
                >
                  <PlusIcon className="h-3.5 w-3.5" />Add agent
                </button>
              </div>

              {/* ---- what the selected agent is, and what can be done to it ---- */}
              {agent && (
                <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-xs">
                  <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                    <span className="font-mono text-ink">{agent.command || "(a shell)"}</span>
                    <span className="text-ink-muted">
                      {agent.resume ? "resumes its conversation" : "starts fresh each time"}
                    </span>
                    {agent.skipPermissions && <span className="text-warning">acts without asking</span>}
                    <span className="text-ink-muted">started {ago(agent.lastStartedAt)}</span>
                  </div>
                  <div className="flex items-center gap-1">
                    <IconButton label={`Start ${agent.name}`} disabled={agent.running || busy !== null} onClick={() => void run(agent.id + "start", () => api.agentStart(ws.id, agent.id))}>
                      <PlayIcon className="h-4 w-4" />
                    </IconButton>
                    <IconButton label={`Restart ${agent.name}`} disabled={busy !== null} onClick={() => void restartAgent(agent)}>
                      <RefreshIcon className="h-4 w-4" />
                    </IconButton>
                    <IconButton label={`Stop ${agent.name}`} danger disabled={!agent.present || busy !== null} onClick={() => void stopAgent(agent)}>
                      <StopIcon className="h-4 w-4" />
                    </IconButton>
                    <IconButton label={`Edit ${agent.name}`} disabled={busy !== null} onClick={() => setEditingAgent({ ...agent })}>
                      <RenameIcon className="h-4 w-4" />
                    </IconButton>
                    <IconButton label={`Remove ${agent.name}`} danger disabled={busy !== null} onClick={() => void removeAgent(agent)}>
                      <TrashIcon className="h-4 w-4" />
                    </IconButton>
                  </div>
                </div>
              )}

              {/* ---- the terminal is the page ---- */}
              <div className="overflow-hidden rounded-lg border border-border">
                <TermView key={termPath} path={termPath} reattaches className="h-[60vh] min-h-[22rem]" />
              </div>

              <p className="text-xs text-ink-muted">
                Reachable without Islet as well: <code>tmux -S /var/lib/islet/tmux.sock attach -t islet-ws-{ws.id}</code>
              </p>
            </section>
          )}
        </div>
      )}

      {/* ---- forms ---- */}
      {editingWs && (
        <Card title={editingWs.id ? `Edit ${editingWs.name}` : "New workspace"} description="A directory on this server, and a session that outlives the browser.">
          <form onSubmit={saveWs} className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <Field label="Name" hint="Lowercase letters, digits and dashes.">
              <Input value={editingWs.name} onChange={(e) => setEditingWs({ ...editingWs, name: e.target.value })} placeholder="islet" required />
            </Field>
            <Field label="Directory" hint="Where the agents start. An absolute path.">
              <Input value={editingWs.directory} onChange={(e) => setEditingWs({ ...editingWs, directory: e.target.value })} placeholder="/var/www/islet" required />
            </Field>
            <Field label="" className="md:col-span-2">
              <label className="flex items-start gap-2 text-sm">
                <input
                  type="checkbox"
                  className="mt-0.5"
                  checked={editingWs.mcpEnabled}
                  onChange={(e) => setEditingWs({ ...editingWs, mcpEnabled: e.target.checked })}
                />
                <span>
                  Let its agents reach Islet
                  <span className="block text-ink-muted">
                    A scoped API token and an MCP endpoint, so an agent can read container logs, restart a container,
                    deploy and message you through audited calls instead of working it out as root. Never the shell scope.
                  </span>
                </span>
              </label>
            </Field>
            <div className="flex gap-2 md:col-span-2">
              <Button type="submit" disabled={busy === "ws-save"}>{busy === "ws-save" ? "Saving…" : "Save workspace"}</Button>
              <Button type="button" variant="secondary" onClick={() => setEditingWs(null)}>Cancel</Button>
            </div>
          </form>
        </Card>
      )}

      {editingAgent && ws && (
        <Card
          title={editingAgent.id ? `Edit ${editingAgent.name}` : `New agent in ${ws.name}`}
          description="Its own window, its own conversation, the same directory as the rest of the workspace."
        >
          <form onSubmit={saveAgent} className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <Field label="Name" hint="Also the tmux window name, so it reads the same over SSH.">
              <Input
                value={editingAgent.name ?? ""}
                onChange={(e) => setEditingAgent({ ...editingAgent, name: e.target.value })}
                placeholder="worker"
                required
              />
            </Field>
            <Field label="What it runs" hint={PRESETS[editingAgent.preset ?? "claude"].blurb}>
              <Select
                value={editingAgent.preset ?? "claude"}
                onChange={(e) => {
                  const p = e.target.value as Agent["preset"];
                  setEditingAgent({ ...editingAgent, preset: p, command: PRESETS[p].command });
                }}
              >
                {(Object.keys(PRESETS) as Agent["preset"][]).map((k) => (
                  <option key={k} value={k}>{PRESETS[k].label}</option>
                ))}
              </Select>
            </Field>
            {editingAgent.preset !== "shell" && (
              <Field label="Command" className="md:col-span-2" hint="Typed into the window as if you had typed it, so it is visible in the scrollback.">
                <Input
                  value={editingAgent.command ?? ""}
                  onChange={(e) => setEditingAgent({ ...editingAgent, command: e.target.value })}
                  className="font-mono"
                  required
                />
              </Field>
            )}
            {editingAgent.preset === "claude" && (
              <>
                <Field label="" className="md:col-span-2">
                  <label className="flex items-start gap-2 text-sm">
                    <input
                      type="checkbox"
                      className="mt-0.5"
                      checked={editingAgent.resume ?? true}
                      onChange={(e) => setEditingAgent({ ...editingAgent, resume: e.target.checked })}
                    />
                    <span>
                      Come back to the same conversation
                      <span className="block text-ink-muted">
                        After a stop, a crash or a reboot this agent resumes the conversation it owns, rather than
                        starting an empty one. Each agent has its own, so two agents in one workspace never inherit each
                        other's history.
                      </span>
                    </span>
                  </label>
                </Field>
                <Field label="" className="md:col-span-2">
                  <label className="flex items-start gap-2 text-sm">
                    <input
                      type="checkbox"
                      className="mt-0.5"
                      checked={editingAgent.skipPermissions ?? false}
                      onChange={(e) => setEditingAgent({ ...editingAgent, skipPermissions: e.target.checked })}
                    />
                    <span>
                      Act without asking first
                      <span className="block text-ink-muted">
                        Adds <code>--dangerously-skip-permissions</code>. The agent stops pausing for approval on a
                        machine that is serving real sites. Useful when nobody is watching, which is also exactly when
                        it costs the most.
                      </span>
                    </span>
                  </label>
                </Field>
              </>
            )}
            <div className="flex gap-2 md:col-span-2">
              <Button type="submit" disabled={busy === "agent-save"}>{busy === "agent-save" ? "Saving…" : "Save agent"}</Button>
              <Button type="button" variant="secondary" onClick={() => setEditingAgent(null)}>Cancel</Button>
            </div>
          </form>
        </Card>
      )}
    </div>
  );
}
