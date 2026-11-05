import { useEffect, useRef, useState } from "react";
import { pollInterval } from "@/lib/poll";
import { RefreshIcon } from "@/components/icons";
import { Button } from "@/components/ui";

/**
 * Says when the daemon behind this tab has been updated since the tab loaded.
 *
 * A page is a program that was downloaded once. After an update the daemon
 * serves the new panel, but a tab that was already open keeps running the old
 * one — every screen as it was — until somebody reloads it, and nothing said so.
 * A new upload screen shipped, and a person who had the panel open was told it
 * was live and saw the old one.
 *
 * It asks this daemon directly, not the server in view: the code in this tab
 * came from here, and a managed server updating does not change it. The commit
 * is compared rather than the version, so a rebuild under the same number
 * counts. Coming back to a hidden tab checks at once, which is when a stale tab
 * is most likely to be looked at.
 */
export default function UpdateNotice() {
  const loaded = useRef<string | null>(null);
  const [next, setNext] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const check = () =>
      fetch("/api/v1/health", { credentials: "same-origin", headers: { Accept: "application/json" } })
        .then((r) => (r.ok ? (r.json() as Promise<{ version?: string; commit?: string }>) : null))
        .then((h) => {
          // A restart in progress answers nothing, and a signed-out request
          // answers without a commit; neither is news.
          if (!alive || !h?.commit) return;
          if (loaded.current === null) { loaded.current = h.commit; return; }
          if (h.commit !== loaded.current) setNext(h.version || h.commit);
        })
        .catch(() => {});
    void check();
    const stop = pollInterval(() => void check(), 60000);
    return () => { alive = false; stop(); };
  }, []);

  if (!next) return null;
  return (
    <div role="status" className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-accent/30 bg-accent-soft px-3 py-2 text-sm text-ink md:px-5">
      <RefreshIcon className="h-4 w-4 shrink-0 text-accent" />
      <span className="min-w-0 flex-1">Islet was updated to {next}. This tab is still running the previous version — reload to use the new one.</span>
      <Button className="h-7 px-3 text-xs" onClick={() => window.location.reload()}>Reload</Button>
    </div>
  );
}
