import { useEffect, useState } from "react";
import { api, type AuditEntry } from "@/lib/api";
import { Button, Card } from "@/components/ui";

export default function AuditLog() {
  const [rows, setRows] = useState<AuditEntry[]>([]);
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);

  const load = async (before?: number) => {
    setBusy(true);
    try {
      const page = await api.audit(50, before);
      setRows((r) => (before ? [...r, ...page] : page));
      setDone(page.length < 50);
    } catch { /* leave what we have */ }
    finally { setBusy(false); }
  };
  useEffect(() => { void load(); }, []);

  return (
    <Card title="Audit log" description="Every action taken through Islet, newest first. This list cannot be edited.">
      <div className="overflow-x-auto"><table className="w-full min-w-[560px] text-sm">
        <thead className="text-left text-xs text-ink-muted">
          <tr><th className="pb-2 font-medium">When</th><th className="pb-2 font-medium">Actor</th><th className="pb-2 font-medium">Action</th><th className="pb-2 font-medium">Detail</th></tr>
        </thead>
        <tbody className="divide-y divide-border">
          {rows.map((e) => (
            <tr key={e.id}>
              <td className="whitespace-nowrap py-1.5 font-mono text-xs text-ink-muted">{new Date(e.createdAt).toLocaleString()}</td>
              <td className="py-1.5">{e.actor}</td>
              <td className="py-1.5 font-mono text-xs">{e.action}</td>
              <td className="max-w-[28ch] truncate py-1.5 text-ink-muted" title={`${e.target} ${e.detail}`.trim()}>{[e.target, e.detail].filter(Boolean).join(" · ")}</td>
            </tr>
          ))}
          {rows.length === 0 && <tr><td colSpan={4} className="py-2 text-ink-muted">No entries yet.</td></tr>}
        </tbody>
      </table></div>
      {!done && rows.length > 0 && (
        <div className="mt-3"><Button variant="secondary" disabled={busy} onClick={() => void load(rows[rows.length - 1].id)}>Load older</Button></div>
      )}
    </Card>
  );
}
