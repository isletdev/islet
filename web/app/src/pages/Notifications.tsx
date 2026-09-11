import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api, RequestError, type Channel, type IsletEvent } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card, Field, Input } from "@/components/ui";

const TYPES: Record<string, { label: string; fields: { key: string; label: string; hint?: string; secret?: boolean }[]; help: string }> = {
  telegram: { label: "Telegram", help: "Create a bot with @BotFather, paste its token, send the bot a message, then click Detect chat.", fields: [{ key: "token", label: "Bot token", secret: true }, { key: "chatId", label: "Chat ID" }] },
  discord: { label: "Discord", help: "Server settings → Integrations → Webhooks → New webhook, copy the URL.", fields: [{ key: "webhookUrl", label: "Webhook URL", secret: true }] },
  slack: { label: "Slack", help: "Create an app with an Incoming Webhook and paste its URL.", fields: [{ key: "webhookUrl", label: "Webhook URL", secret: true }] },
  email: { label: "Email", help: "Any SMTP relay: Resend, Postmark, Mailgun, Brevo, or your provider. Port 587 uses STARTTLS, 465 uses TLS.", fields: [{ key: "host", label: "SMTP host" }, { key: "port", label: "Port", hint: "587" }, { key: "username", label: "Username" }, { key: "password", label: "Password", secret: true }, { key: "from", label: "From" }, { key: "to", label: "To", hint: "comma separated" }] },
  ntfy: { label: "ntfy", help: "Uses ntfy.sh unless you run your own server.", fields: [{ key: "url", label: "Server URL", hint: "https://ntfy.sh" }, { key: "topic", label: "Topic" }, { key: "token", label: "Access token (optional)", secret: true }] },
  gotify: { label: "Gotify", help: "Create an application in Gotify and paste its token.", fields: [{ key: "url", label: "Server URL" }, { key: "token", label: "App token", secret: true }] },
  pushover: { label: "Pushover", help: "Create an application at pushover.net.", fields: [{ key: "appToken", label: "App token", secret: true }, { key: "userKey", label: "User key", secret: true }] },
  webhook: { label: "Webhook", help: "Islet POSTs JSON and signs it with HMAC-SHA256 in X-Islet-Signature when a secret is set.", fields: [{ key: "url", label: "URL" }, { key: "secret", label: "Signing secret (optional)", secret: true }] },
};
const CATEGORIES = ["system", "security", "deploy", "container", "database", "domain", "cron", "backup", "runner", "uptime", "report", "custom"];

export default function Notifications() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const [channels, setChannels] = useState<Channel[]>([]);
  const [events, setEvents] = useState<IsletEvent[]>([]);
  const [editing, setEditing] = useState<Channel | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(() => Promise.all([api.channels(), api.events(50)]).then(([c, e]) => { setChannels(c); setEvents(e); setErr(null); }).catch((e) => setErr(e instanceof RequestError ? e.message : String(e))), []);
  useEffect(() => { void load(); const id = setInterval(() => void load(), 15000); return () => clearInterval(id); }, [load]);

  const test = async (c: Channel) => { setMsg(null); try { await api.channelTest(c.id); setMsg(`Sent a test to ${c.name}.`); } catch (e) { setMsg(e instanceof RequestError ? e.message : String(e)); } };
  const remove = async (c: Channel) => { if (!confirm(`Remove channel ${c.name}?`)) return; await api.channelDelete(c.id); await load(); };

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <div>
        <h1 className="text-xl font-semibold tracking-[-0.02em]">Notifications</h1>
        <p className="mt-1 text-ink-muted">Where Islet tells you what happened. Criticals always go through; warnings and info follow each channel's rules.</p>
      </div>
      {err && <Alert>{err}</Alert>}

      <div className="rounded-lg border border-border bg-surface">
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <span className="font-semibold">Channels</span>
          {isAdmin && <Button className="h-8 text-xs" onClick={() => setEditing({ id: "", type: "telegram", name: "", config: {}, categories: "*", minSeverity: "warning", quietFrom: "", quietTo: "", enabled: true, createdAt: "" })}>Add channel</Button>}
        </div>
        <ul className="divide-y divide-border text-sm">
          {channels.map((c) => (
            <li key={c.id} className="flex flex-wrap items-center justify-between gap-2 px-4 py-2.5">
              <div><span className="font-medium">{c.name}</span> <span className="ml-2 text-xs text-ink-muted">{TYPES[c.type]?.label ?? c.type} · {c.minSeverity}+ · {c.categories === "*" ? "all categories" : c.categories}{c.quietFrom && ` · quiet ${c.quietFrom}–${c.quietTo}`}{c.digest && ` · ${c.digest} digest`}</span>{!c.enabled && <span className="ml-2 rounded-sm bg-surface-2 px-1.5 py-0.5 text-[10px] text-ink-muted">disabled</span>}</div>
              {isAdmin && <div className="flex gap-3 text-xs"><button type="button" onClick={() => void test(c)} className="text-ink-muted hover:text-ink">Send test</button><button type="button" onClick={() => setEditing({ ...c, config: {} })} className="text-ink-muted hover:text-ink">Edit</button><button type="button" onClick={() => void remove(c)} className="text-danger hover:underline">Remove</button></div>}
            </li>
          ))}
          {channels.length === 0 && <li className="px-4 py-6 text-center text-ink-muted">No channels yet. Add Telegram, Discord, Slack, email or a webhook.</li>}
        </ul>
        {msg && <p className="border-t border-border px-4 py-2 text-xs text-ink-muted">{msg}</p>}
      </div>

      {editing && <ChannelForm initial={editing} onClose={() => setEditing(null)} onSaved={async () => { setEditing(null); await load(); }} />}

      <Card title="Recent events" description="Everything Islet noticed, newest first. Warnings repeat at most every ten minutes.">
        <ul className="divide-y divide-border text-sm">
          {events.map((e) => (
            <li key={e.id} className="flex items-start gap-3 py-2">
              <span className={`mt-1.5 h-2 w-2 flex-none rounded-full ${e.severity === "critical" ? "bg-danger" : e.severity === "warning" ? "bg-warning" : "bg-success"}`} aria-hidden="true" />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-baseline gap-x-2"><span className="font-medium">{e.title}</span><span className="font-mono text-[11px] text-ink-faint">{e.category} · {e.severity}</span></div>
                {e.message && <div className="text-ink-muted">{e.message}</div>}
              </div>
              <span className="whitespace-nowrap font-mono text-[11px] text-ink-faint">{new Date(e.createdAt).toLocaleString()}</span>
            </li>
          ))}
          {events.length === 0 && <li className="py-4 text-center text-ink-muted">Nothing yet.</li>}
        </ul>
      </Card>
    </div>
  );
}

function ChannelForm({ initial, onClose, onSaved }: { initial: Channel; onClose: () => void; onSaved: () => Promise<void> }) {
  const [c, setC] = useState<Channel>(initial);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [chats, setChats] = useState<{ chatId: string; name: string; type: string }[] | null>(null);
  const t = TYPES[c.type];
  const setCfg = (k: string, v: string) => setC({ ...c, config: { ...(c.config ?? {}), [k]: v } });
  const cats = c.categories === "*" ? [] : c.categories.split(",").filter(Boolean);
  const toggleCat = (cat: string) => {
    const next = cats.includes(cat) ? cats.filter((x) => x !== cat) : [...cats, cat];
    setC({ ...c, categories: next.length === 0 || next.length === CATEGORIES.length ? "*" : next.join(",") });
  };
  const submit = async (e: FormEvent) => {
    e.preventDefault(); setBusy(true); setMsg(null);
    try { await api.channelSave(c); await onSaved(); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); }
    finally { setBusy(false); }
  };
  const detect = async () => {
    setMsg(null);
    try { const r = await api.telegramDetect(c.config?.token ?? ""); setChats(r); if (r.length === 1) setCfg("chatId", r[0].chatId); if (r.length === 0) setMsg("No messages seen yet. Open the bot in Telegram, press Start, then try again."); }
    catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); }
  };

  return (
    <Card title={c.id ? `Edit ${c.name}` : "Add channel"} description={t?.help}>
      <form onSubmit={submit} className="grid gap-4 md:grid-cols-2">
        <Field label="Type"><select value={c.type} onChange={(e) => setC({ ...c, type: e.target.value, config: {} })} disabled={!!c.id} className="h-9 w-full rounded-md border border-border-strong bg-bg px-2 text-sm">{Object.entries(TYPES).map(([k, v]) => <option key={k} value={k}>{v.label}</option>)}</select></Field>
        <Field label="Name"><Input value={c.name} onChange={(e) => setC({ ...c, name: e.target.value })} required placeholder="Ops channel" /></Field>
        {t?.fields.map((f) => (
          <Field key={f.key} label={f.label} hint={c.id && f.secret ? "Leave empty to keep the stored value." : f.hint}>
            <div className="flex gap-2">
              <Input value={c.config?.[f.key] ?? ""} onChange={(e) => setCfg(f.key, e.target.value)} type={f.secret ? "password" : "text"} placeholder={f.hint} autoComplete="off" />
              {c.type === "telegram" && f.key === "chatId" && <Button type="button" variant="secondary" onClick={() => void detect()}>Detect chat</Button>}
            </div>
            {f.key === "chatId" && chats && chats.length > 1 && <div className="mt-1 flex flex-wrap gap-1">{chats.map((ch) => <button key={ch.chatId} type="button" onClick={() => setCfg("chatId", ch.chatId)} className="rounded-sm border border-border-strong px-2 py-0.5 text-xs">{ch.name} ({ch.type})</button>)}</div>}
          </Field>
        ))}
        <Field label="Minimum severity" hint="Criticals always go through.">
          <select value={c.minSeverity} onChange={(e) => setC({ ...c, minSeverity: e.target.value as Channel["minSeverity"] })} className="h-9 w-full rounded-md border border-border-strong bg-bg px-2 text-sm"><option value="info">Info and up (everything)</option><option value="warning">Warnings and criticals</option><option value="critical">Criticals only</option></select>
        </Field>
        <Field label="Digest" hint="Batch warnings and info into one message; criticals still go out at once.">
          <select value={c.digest ?? ""} onChange={(e) => setC({ ...c, digest: e.target.value })} className="h-9 w-full rounded-md border border-border-strong bg-bg px-2 text-sm"><option value="">Send each event</option><option value="hourly">Hourly digest</option><option value="daily">Daily digest</option></select>
        </Field>
        <Field label="Quiet hours (optional)" hint="Local server time. Warnings and info wait; criticals do not.">
          <div className="flex items-center gap-2"><Input value={c.quietFrom} onChange={(e) => setC({ ...c, quietFrom: e.target.value })} placeholder="22:00" className="w-24" /><span className="text-ink-muted">to</span><Input value={c.quietTo} onChange={(e) => setC({ ...c, quietTo: e.target.value })} placeholder="07:00" className="w-24" /></div>
        </Field>
        <div className="md:col-span-2">
          <span className="mb-1 block text-sm font-medium">Categories</span>
          <div className="flex flex-wrap gap-1">
            <button type="button" onClick={() => setC({ ...c, categories: "*" })} className={`rounded-sm border px-2 py-0.5 text-xs ${c.categories === "*" ? "border-ink bg-ink text-on-ink" : "border-border-strong text-ink-muted"}`}>all</button>
            {CATEGORIES.map((cat) => <button key={cat} type="button" onClick={() => toggleCat(cat)} className={`rounded-sm border px-2 py-0.5 text-xs ${c.categories !== "*" && cats.includes(cat) ? "border-ink bg-ink text-on-ink" : "border-border-strong text-ink-muted"}`}>{cat}</button>)}
          </div>
        </div>
        <label className="flex items-center gap-1.5 text-sm md:col-span-2"><input type="checkbox" checked={c.enabled} onChange={(e) => setC({ ...c, enabled: e.target.checked })} />Enabled</label>
        <div className="flex items-center gap-2 md:col-span-2"><Button type="submit" disabled={busy}>Save</Button><Button type="button" variant="secondary" onClick={onClose}>Cancel</Button>{msg && <span className="text-sm text-ink-muted">{msg}</span>}</div>
      </form>
    </Card>
  );
}
