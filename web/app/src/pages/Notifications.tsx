import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api, RequestError, type Channel, type IsletEvent, type MailRelay } from "@/lib/api";
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
              <div><span className="font-medium">{c.name}</span> <span className="ml-2 text-xs text-ink-muted">{TYPES[c.type]?.label ?? c.type} · {c.minSeverity}+ · {c.categories === "*" ? "all categories" : c.categories}{c.quietFrom && ` · quiet ${c.quietFrom}–${c.quietTo}`}{c.digest && ` · ${c.digest} digest`}{c.subjects && ` · only ${c.subjects}`}</span>{!c.enabled && <span className="ml-2 rounded-sm bg-surface-2 px-1.5 py-0.5 text-[10px] text-ink-muted">disabled</span>}</div>
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
      {isAdmin && <MailRelayCard />}
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
        <Field label="Only for (optional)" hint="Names of apps, containers, checks, jobs or plans this channel is for, comma separated; * wildcards work (shop-*). Server-wide events still follow the categories."><Input value={c.subjects ?? ""} onChange={(e) => setC({ ...c, subjects: e.target.value })} placeholder="shop, shop-worker" className="font-mono" /></Field>
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

function MailRelayCard() {
  const [m, setM] = useState<MailRelay | null>(null);
  const [domain, setDomain] = useState(""); const [hostname, setHostname] = useState(""); const [relayhost, setRelayhost] = useState(""); const [ru, setRu] = useState(""); const [rp, setRp] = useState("");
  const [busy, setBusy] = useState(false); const [msg, setMsg] = useState<string | null>(null); const [to, setTo] = useState("");
  const load = () => api.mailRelay().then(setM).catch(() => {});
  useEffect(() => { void load(); }, []);
  if (!m) return null;
  const setup = async (e: FormEvent) => { e.preventDefault(); setBusy(true); setMsg(null); try { setM(await api.mailRelaySet({ domain, hostname, relayhost, relayUser: ru, relayPassword: rp })); setMsg("Relay running. Publish the records below, then send a test."); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } finally { setBusy(false); } };
  const test = async () => { setBusy(true); setMsg(null); try { const r = await api.mailRelayTest(to); setMsg(`Queued for ${to}. ${r.queue ? "Queue: " + r.queue : "Queue is empty, so it was handed off."}`); } catch (er) { setMsg(er instanceof RequestError ? er.message : String(er)); } finally { setBusy(false); } };
  const remove = async () => { if (!confirm("Remove the mail relay? The DKIM key volume is kept.")) return; await api.mailRelayRemove(); await load(); };
  return (
    <div className="rounded-lg border border-border bg-surface">
      <div className="border-b border-border px-4 py-3"><span className="font-semibold">Outbound mail</span><p className="mt-0.5 text-xs text-ink-muted">A Postfix relay with DKIM signing for the apps on this server and for the email channel above. Needs a domain you control; port 25 must be open at your provider, or use an upstream relay.</p></div>
      <div className="p-4 text-sm">
        {!m.domain ? (
          <form onSubmit={setup} className="grid gap-3 sm:grid-cols-2">
            <Field label="Sender domain" hint="Mail is sent as something@this-domain."><Input value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="example.com" className="font-mono" required /></Field>
            <Field label="Mail host name" hint="Defaults to mail.<domain>; needs an A record and a PTR record."><Input value={hostname} onChange={(e) => setHostname(e.target.value)} placeholder="mail.example.com" className="font-mono" /></Field>
            <Field label="Upstream relay (optional)" hint="[smtp.provider.com]:587 when the provider blocks port 25."><Input value={relayhost} onChange={(e) => setRelayhost(e.target.value)} className="font-mono" /></Field>
            <div className="grid grid-cols-2 gap-2"><Field label="Upstream user"><Input value={ru} onChange={(e) => setRu(e.target.value)} autoComplete="off" /></Field><Field label="Upstream password"><Input type="password" value={rp} onChange={(e) => setRp(e.target.value)} autoComplete="off" /></Field></div>
            <div className="flex items-center gap-2 sm:col-span-2"><Button type="submit" disabled={busy}>{busy ? "Installing…" : "Set up relay"}</Button>{msg && <span className="text-xs text-ink-muted">{msg}</span>}</div>
          </form>
        ) : (
          <div className="space-y-3">
            <p>Relay <span className="font-mono">{m.hostname}</span> for <span className="font-mono">{m.domain}</span> is {m.running ? <span className="text-success">running</span> : <span className="text-danger">not running</span>}{m.relayhost && <> via <span className="font-mono">{m.relayhost}</span></>}. Apps use <span className="font-mono">smtp://{m.appSmtp}</span> (no auth); the email channel uses host <span className="font-mono">127.0.0.1</span>, port <span className="font-mono">2525</span>.</p>
            <div className="overflow-x-auto"><table className="w-full min-w-[520px] text-xs"><thead className="text-left text-ink-muted"><tr><th className="pb-1 font-medium">Record</th><th className="pb-1 font-medium">Value</th><th className="pb-1 font-medium">Status</th></tr></thead>
              <tbody className="divide-y divide-border">{m.records.map((r) => <tr key={r.name + r.type}><td className="py-1.5 pr-2 align-top font-mono whitespace-nowrap">{r.type} {r.name}</td><td className="py-1.5 pr-2 align-top"><pre className="whitespace-pre-wrap break-all font-mono">{r.value}</pre><span className="text-ink-faint">{r.purpose}</span></td><td className="py-1.5 align-top whitespace-nowrap">{r.ok ? <span className="text-success">published</span> : r.found ? <span className="text-warning" title={r.found}>differs</span> : <span className="text-ink-muted">missing</span>}</td></tr>)}</tbody></table></div>
            <div className="flex flex-wrap items-center gap-2"><Input value={to} onChange={(e) => setTo(e.target.value)} placeholder="you@example.com" className="w-64" /><Button variant="secondary" className="h-9 text-xs" disabled={busy || !to.includes("@")} onClick={() => void test()}>Send a test</Button><Button variant="secondary" className="h-9 text-xs" onClick={() => void load()}>Re-check DNS</Button><button type="button" onClick={() => void remove()} className="ml-auto text-xs text-danger hover:underline">Remove relay</button></div>
            {msg && <p className="text-xs text-ink-muted">{msg}</p>}
          </div>
        )}
      </div>
    </div>
  );
}
