import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { api, type Health } from "@/lib/api";
import TermView from "@/components/TermView";
import { Mark } from "@/components/ui";

/**
 * The terminal on its own, filling the window.
 *
 * A shell wants height, and inside the panel it competes with a sidebar, a
 * header and a page heading. Opened as a window this is all there is: one thin
 * bar naming what you are connected to, and the rest is terminal. It is a route
 * rather than a mode so the window survives a reload and can be bookmarked.
 */
export default function Console() {
  const [params] = useSearchParams();
  const container = params.get("container") ?? "";
  const dir = params.get("dir") ?? "";
  const workspace = params.get("workspace") ?? "";
  // A workspace holds several agents, so the pop-out has to say which window it
  // wants; without it, every agent's pop-out would land on the shared shell.
  const agent = params.get("agent") ?? "";
  const [health, setHealth] = useState<Health | null>(null);

  useEffect(() => { void api.health().then(setHealth).catch(() => {}); }, []);

  const where = container || (workspace && "workspace") || health?.hostname || "this server";
  useEffect(() => { document.title = `${where} · Islet console`; }, [where]);

  return (
    <div className="flex h-dvh flex-col bg-bg">
      <div className="flex h-9 shrink-0 items-center gap-2 border-b border-border px-3 text-[13px]">
        <Mark className="h-4 w-4 shrink-0" />
        <span className="truncate font-medium">{where}</span>
        {container && <span className="shrink-0 text-xs text-ink-muted">container</span>}
        {dir && <span className="truncate font-mono text-xs text-ink-muted">{dir}</span>}
        <span className="ml-auto shrink-0 text-xs text-ink-muted">Islet console</span>
      </div>
      <div className="min-h-0 flex-1 p-2">
        <TermView
          path={
            workspace && agent
              ? `/api/v1/workspaces/${encodeURIComponent(workspace)}/agents/${encodeURIComponent(agent)}/attach`
              : workspace
              ? `/api/v1/workspaces/${encodeURIComponent(workspace)}/attach`
              : container
                ? `/api/v1/docker/containers/${encodeURIComponent(container)}/exec`
                : `/api/v1/terminal/ws${dir ? `?dir=${encodeURIComponent(dir)}` : ""}`
          }
          // A workspace is a tmux session that is still there after a drop, so
          // this one may reconnect on its own. A plain shell may not.
          reattaches={workspace !== ""}
          className="h-full"
        />
      </div>
    </div>
  );
}

/** Open the console in its own window, sized like a terminal emulator. */
export function openConsole(opts: { container?: string; dir?: string; workspace?: string; agent?: string } = {}) {
  const { container, dir, workspace, agent } = opts;
  const q = new URLSearchParams();
  if (container) q.set("container", container);
  if (dir) q.set("dir", dir);
  if (workspace) q.set("workspace", workspace);
  if (agent) q.set("agent", agent);
  const url = q.toString() ? `/console?${q}` : "/console";
  const w = Math.min(1100, Math.round(window.screen.availWidth * 0.8));
  const h = Math.min(720, Math.round(window.screen.availHeight * 0.8));
  const left = Math.round((window.screen.availWidth - w) / 2);
  const top = Math.round((window.screen.availHeight - h) / 2);
  // One window per agent, so opening a second does not replace the first —
  // watching two agents side by side is the reason they exist.
  window.open(url, "islet-console-" + ([workspace, agent].filter(Boolean).join("-") || container || dir || "host"), `popup=yes,width=${w},height=${h},left=${left},top=${top}`);
}
