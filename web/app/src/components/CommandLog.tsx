import { useEffect, useState } from "react";
import { api, type CommandEntry } from "@/lib/api";
import { Button, Card } from "@/components/ui";

/** Command transparency: every command the daemon ran on this server, copyable. */
export default function CommandLog() {
  const [rows, setRows] = useState<CommandEntry[]>([]);
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState<number | null>(null);
  useEffect(() => { if (open) void api.commands(100).then(setRows).catch(() => setRows([])); }, [open]);
  return (
    <Card title="Commands Islet ran" description="Nothing hidden: the exact commands the daemon executed on this server, newest first. Copy one to run it yourself.">
      {!open ? <Button variant="secondary" className="h-8 text-xs" onClick={() => setOpen(true)}>Show the last 100</Button> : (
        <div className="max-h-96 overflow-auto">
          <table className="w-full text-xs"><tbody className="divide-y divide-border">
            {rows.map((c) => (
              <tr key={c.id} className={c.exitCode !== 0 ? "text-danger" : ""}>
                <td className="whitespace-nowrap py-1 pr-2 font-mono text-ink-muted">{new Date(c.createdAt).toLocaleTimeString()}</td>
                <td className="py-1 pr-2 text-ink-muted">{c.actor}</td>
                <td className="py-1 pr-2 font-mono break-all">{c.command}{c.exitCode !== 0 && <span className="ml-1 text-ink-muted">exit {c.exitCode}</span>}</td>
                <td className="py-1 text-right text-ink-muted whitespace-nowrap">{c.durationMs} ms <button type="button" onClick={() => { void navigator.clipboard.writeText(c.command); setCopied(c.id); setTimeout(() => setCopied(null), 1200); }} className="ml-2 hover:text-ink">{copied === c.id ? "copied" : "copy"}</button></td>
              </tr>
            ))}
            {rows.length === 0 && <tr><td className="py-2 text-ink-muted">Nothing recorded yet.</td></tr>}
          </tbody></table>
        </div>
      )}
    </Card>
  );
}
