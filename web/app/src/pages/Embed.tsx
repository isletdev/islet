import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { api } from "@/lib/api";

/** An app's own UI inside the panel. Same-site cookies flow through, so a
 * domain marked "Protect with Islet login" opens without a second sign-in. */
export default function Embed() {
  const { i } = useParams();
  const [link, setLink] = useState<{ label: string; url: string } | null | undefined>(undefined);
  useEffect(() => { void api.sidebar().then((l) => setLink(l[Number(i)] ?? null)).catch(() => setLink(null)); }, [i]);
  if (link === undefined) return <p className="p-6 text-sm text-ink-muted">Loading…</p>;
  if (!link) return <p className="p-6 text-sm text-ink-muted">That sidebar link no longer exists. Manage links in Settings.</p>;
  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between border-b border-border px-4 py-1.5 text-xs text-ink-muted">
        <span>{link.label} · <span className="font-mono">{link.url}</span></span>
        <a href={link.url} target="_blank" rel="noreferrer" className="hover:text-ink">Open in a new tab</a>
      </div>
      <iframe title={link.label} src={link.url} className="min-h-0 flex-1 w-full bg-bg" sandbox="allow-same-origin allow-scripts allow-forms allow-popups allow-downloads allow-modals" />
      <p className="border-t border-border px-4 py-1 text-[11px] text-ink-faint">Blank frame? The app refuses to be embedded (X-Frame-Options); use the new-tab link.</p>
    </div>
  );
}
