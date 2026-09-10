import { useEffect, useState } from "react";
import { api, RequestError } from "@/lib/api";
import { bytes } from "@/lib/format";
import { Button, Card } from "@/components/ui";

interface Item { key: string; label: string; bytes: number; reclaimable: number; hint: string; cleanable: boolean }

export default function DiskDoctor({ isAdmin }: { isAdmin: boolean }) {
  const [items, setItems] = useState<Item[] | null>(null);
  const [sel, setSel] = useState<Set<string>>(new Set());
  const [msg, setMsg] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const load = () => api.diskReport().then((r) => { setItems(r); setSel(new Set(r.filter((i) => i.cleanable && i.reclaimable > 0 && !i.key.startsWith("docker-local")).map((i) => i.key))); }).catch(() => setItems([]));
  useEffect(() => { void load(); }, []);

  const clean = async () => {
    setBusy(true); setMsg(null);
    try { const r = await api.diskClean(Array.from(sel)); setMsg(Object.entries(r).map(([k, v]) => `${k}: ${v}`).join(" · ")); await load(); }
    catch (e) { setMsg(e instanceof RequestError ? e.message : String(e)); }
    finally { setBusy(false); }
  };
  const total = items?.reduce((a, i) => a + i.reclaimable, 0) ?? 0;

  return (
    <Card title="Disk Doctor" description={items ? `${bytes(total)} can be reclaimed safely. Volumes are never touched here.` : "Measuring…"}>
      {items && items.length === 0 && <p className="text-sm text-ink-muted">Nothing to measure yet.</p>}
      <ul className="divide-y divide-border">
        {items?.map((i) => (
          <li key={i.key} className="flex items-center gap-3 py-2 text-sm">
            {isAdmin && <input type="checkbox" disabled={!i.cleanable || i.reclaimable === 0} checked={sel.has(i.key)} onChange={(e) => setSel((s) => { const n = new Set(s); if (e.target.checked) n.add(i.key); else n.delete(i.key); return n; })} aria-label={`Clean ${i.label}`} />}
            <div className="min-w-0 flex-1"><div className="font-medium">{i.label}</div><div className="truncate text-xs text-ink-muted">{i.hint}</div></div>
            <div className="text-right font-mono text-xs tabular-nums"><div>{bytes(i.bytes)}</div><div className="text-ink-muted">{i.reclaimable > 0 ? `${bytes(i.reclaimable)} free-able` : ""}</div></div>
          </li>
        ))}
      </ul>
      {isAdmin && items && items.length > 0 && (
        <div className="mt-3 flex items-center gap-3"><Button onClick={() => void clean()} disabled={busy || sel.size === 0}>Clean selected</Button>{msg && <span className="text-xs text-ink-muted">{msg}</span>}</div>
      )}
    </Card>
  );
}
