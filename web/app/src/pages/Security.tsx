import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api, RequestError, type SecurityState, type SSHSettings } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card, Field, Input } from "@/components/ui";
import { streamLines } from "@/lib/stream";

function err(e: unknown) { return e instanceof RequestError ? e.message : String(e); }
function fmt(s: string) { return s ? new Date(s).toLocaleString() : ""; }

export default function Security() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const [s, setS] = useState<SecurityState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [out, setOut] = useState<{ title: string; text: string } | null>(null);
  const load = useCallback(() => api.security().then((x) => { setS(x); setError(null); }).catch((e) => setError(err(e))), []);
  useEffect(() => { void load(); }, [load]);

  const fix = async (id: string) => {
    setBusy(id); setOut(null);
    try { const r = await api.securityFix(id); setOut({ title: `Applied ${id}`, text: r.output || "done" }); await load(); }
    catch (e) { setOut({ title: `${id} failed`, text: err(e) }); }
    finally { setBusy(null); }
  };
  const panic = async () => {
    if (!prompt(`This blocks ALL inbound traffic except from your IP (${s?.clientIp}), signs out every other session and revokes every API token. Sites go offline until you undo it in the Firewall section. Type PANIC to continue.`)?.match(/^PANIC$/)) return;
    setBusy("panic");
    try { const r = await api.panic(); setOut({ title: "Locked down", text: r.message + "\n" + r.output }); await load(); } catch (e) { setOut({ title: "Panic failed", text: err(e) }); } finally { setBusy(null); }
  };

  if (error) return <div className="mx-auto max-w-6xl"><Alert>{error}</Alert></div>;
  if (!s) return <p className="text-sm text-ink-muted">Computing the score…</p>;
  const r = s.report;
  const tone = r.score >= 90 ? "text-success" : r.score >= 60 ? "text-warning" : "text-danger";
  const failing = r.checks.filter((c) => c.status !== "pass");

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-[-0.02em]">Security</h1>
          <p className="mt-1 text-ink-muted">{r.linux ? "Ten minutes of one-click fixes take a fresh server to 90." : "Host checks need a Linux server; panel checks are shown."}</p>
        </div>
        <div className="flex gap-2">
          {isAdmin && r.linux && failing.some((c) => c.fix && c.fix !== "ssh-harden" && c.fix !== "apt-upgrade") && <Button className="h-8 text-xs" disabled={busy !== null} onClick={async () => { setBusy("all"); setOut(null); try { const res = await api.securityFixAll(); setOut({ title: "Ran all safe fixes", text: res.map((x) => `${x.fix}: ${x.status}${x.error ? " (" + x.error + ")" : ""}`).join("\n") }); await load(); } finally { setBusy(null); } }}>{busy === "all" ? "Fixing…" : "Fix everything safe"}</Button>}
          {isAdmin && r.linux && <Button variant="danger" className="h-8 text-xs" disabled={busy !== null} onClick={() => void panic()}>Panic button</Button>}
        </div>
      </div>

      <div className="grid gap-4 lg:grid-cols-[260px_minmax(0,1fr)]">
        <Card>
          <div className="text-center">
            <div className={`font-mono text-6xl font-semibold tabular-nums ${tone}`}>{r.score}</div>
            <div className="mt-1 text-sm text-ink-muted">Security Score out of {r.max}</div>
            <div className="mt-3 text-xs text-ink-muted">{failing.length === 0 ? "Everything checked passes." : `${failing.length} item${failing.length === 1 ? "" : "s"} to fix`}</div>
          </div>
        </Card>
        <Card title="Checks" description="Weighted. Fixes run as root on this server and are recorded in the audit log.">
          <ul className="divide-y divide-border text-sm">
            {r.checks.map((c) => (
              <li key={c.id} className="flex items-start gap-3 py-2">
                <span className={`mt-1.5 h-2 w-2 flex-none rounded-full ${c.status === "pass" ? "bg-success" : c.status === "fail" ? "bg-danger" : c.status === "warn" ? "bg-warning" : "bg-ink-faint"}`} />
                <div className="min-w-0 flex-1"><div className="font-medium">{c.title} <span className="font-mono text-[11px] text-ink-faint">+{c.weight}</span></div><div className="text-xs text-ink-muted">{c.detail}</div></div>
                {c.status !== "pass" && (c.fix && isAdmin && r.linux ? <Button variant="secondary" className="h-7 text-xs" disabled={busy !== null} onClick={() => void fix(c.fix!)} title={c.fixNote}>{busy === c.fix ? "Working…" : "Fix"}</Button> : c.fixNote ? <span className="max-w-48 text-right text-[11px] text-ink-faint">{c.fixNote}</span> : null)}
              </li>
            ))}
          </ul>
        </Card>
      </div>
      {out && <Card title={out.title}><pre className="max-h-64 overflow-auto whitespace-pre-wrap font-mono text-xs">{out.text}</pre></Card>}

      <div className="grid gap-4 lg:grid-cols-2">
        <FirewallCard s={s} isAdmin={isAdmin} onChanged={load} onFix={() => void fix("firewall")} busy={busy} />
        <SSHCard s={s} isAdmin={isAdmin} onChanged={load} />
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <Card title="Blocked IPs" description="fail2ban bans after repeated failed SSH logins.">
          <ul className="divide-y divide-border text-xs">{s.banned.map((b) => <li key={b} className="flex items-center justify-between py-1.5"><span className="font-mono">{b}</span>{isAdmin && <button type="button" onClick={async () => { await api.unban(b.split(" ")[0]); await load(); }} className="text-ink-muted hover:text-ink">Unban</button>}</li>)}{s.banned.length === 0 && <li className="py-2 text-ink-muted">{r.linux ? "Nobody is banned right now." : "Available on Linux servers."}</li>}</ul>
        </Card>
        <ScanCard s={s} isAdmin={isAdmin} onChanged={load} />
      </div>
      {isAdmin && r.linux && <LynisCard onChanged={load} />}
      <Diagnostics />
    </div>
  );
}

function LynisCard({ onChanged }: { onChanged: () => Promise<void> }) {
  const [out, setOut] = useState<string | null>(null); const [busy, setBusy] = useState(false); const [score, setScore] = useState<number | null>(null);
  const run = async () => { setBusy(true); setOut(null); try { const r = await api.lynis(); setScore(r.score); setOut(r.output); await onChanged(); } catch (e) { setOut(err(e)); } finally { setBusy(false); } };
  return (
    <Card title="Lynis audit" description="A full system audit by Lynis (installed on first run, takes a minute or two). The hardening index joins the Security Score.">
      <div className="flex items-center gap-3"><Button variant="secondary" className="h-8 text-xs" disabled={busy} onClick={() => void run()}>{busy ? "Auditing…" : "Run Lynis audit"}</Button>{score !== null && <span className="text-sm">Hardening index <span className="font-mono font-semibold">{score}</span>/100</span>}</div>
      {out && <pre className="mt-3 max-h-72 overflow-auto rounded-md border border-border bg-[#0A0A0A] p-3 font-mono text-xs text-[#FAFAFA] whitespace-pre-wrap">{out}</pre>}
    </Card>
  );
}

function Diagnostics() {
  const [host, setHost] = useState(""); const [port, setPort] = useState("443"); const [tool, setTool] = useState("ping");
  const [out, setOut] = useState<string[]>([]); const [busy, setBusy] = useState(false);
  const run = async (e: FormEvent) => {
    e.preventDefault(); setOut([]); setBusy(true);
    if (tool === "port") { try { const r = await api.portCheck(host, +port); setOut([`${host}:${port} is ${r.message}`]); } catch (er) { setOut([err(er)]); } setBusy(false); return; }
    streamLines(`/api/v1/diagnostics?tool=${tool}&host=${encodeURIComponent(host)}`, (l) => setOut((p) => [...p, l]), (m) => { if (m !== "done") setOut((p) => [...p, m]); setBusy(false); });
  };
  return (
    <Card title="Network diagnostics" description="Run from this server, so you see what the server sees.">
      <form onSubmit={run} className="flex flex-wrap items-end gap-2">
        <Field label="Tool"><select value={tool} onChange={(e) => setTool(e.target.value)} className="h-9 rounded-md border border-border-strong bg-bg px-2 text-sm"><option value="ping">ping</option><option value="traceroute">traceroute</option><option value="dig">dig</option><option value="port">port check</option></select></Field>
        <Field label="Host"><Input value={host} onChange={(e) => setHost(e.target.value)} className="w-56 font-mono" placeholder="example.com" required /></Field>
        {tool === "port" && <Field label="Port"><Input value={port} onChange={(e) => setPort(e.target.value)} className="w-20 font-mono" /></Field>}
        <Button type="submit" className="h-9 text-xs" disabled={busy}>{busy ? "Running…" : "Run"}</Button>
      </form>
      {out.length > 0 && <pre className="mt-3 max-h-64 overflow-auto rounded-md border border-border bg-[#0A0A0A] p-3 font-mono text-xs text-[#FAFAFA] whitespace-pre-wrap">{out.join("\n")}</pre>}
    </Card>
  );
}

function FirewallCard({ s, isAdmin, onChanged, onFix, busy }: { s: SecurityState; isAdmin: boolean; onChanged: () => Promise<void>; onFix: () => void; busy: string | null }) {
  const fw = s.firewall;
  const [port, setPort] = useState(""); const [proto, setProto] = useState("tcp"); const [from, setFrom] = useState(""); const [comment, setComment] = useState("");
  const [msg, setMsg] = useState<string | null>(null);
  const add = async (e: FormEvent) => { e.preventDefault(); setMsg(null); try { await api.firewallAllow({ port, proto, from, comment }); setPort(""); setFrom(""); setComment(""); await onChanged(); } catch (er) { setMsg(err(er)); } };
  return (
    <Card title="Firewall" description={!s.report.linux ? "Available on Linux servers." : !fw.installed ? "ufw is not installed." : fw.active ? `Active. ${fw.dockerAware ? "Docker honours it." : "Docker can bypass it: run the firewall fix."}` : "Installed but inactive."}>
      {s.report.linux && !fw.active && isAdmin && <Button className="h-8 text-xs" disabled={busy !== null} onClick={onFix}>Enable with SSH, 80, 443 and the panel port</Button>}
      {fw.active && (
        <>
          <table className="w-full text-xs"><tbody className="divide-y divide-border">
            {fw.rules.map((r, i) => <tr key={i}><td className="py-1.5 font-mono">{r.port}{r.proto && `/${r.proto}`}</td><td className="py-1.5 text-ink-muted">from {r.from}</td><td className="py-1.5 text-ink-muted">{r.comment}</td><td className="py-1.5 text-right">{isAdmin && <button type="button" onClick={async () => { if (confirm(`Remove the rule for ${r.port}?`)) { await api.firewallDelete({ port: r.port, proto: r.proto, from: r.from }); await onChanged(); } }} className="text-danger hover:underline">Remove</button>}</td></tr>)}
          </tbody></table>
          {isAdmin && <PanelRestrict cidr={s.panelCidr ?? ""} onChanged={onChanged} />}
          {isAdmin && <form onSubmit={add} className="mt-3 flex flex-wrap items-end gap-2 border-t border-border pt-3">
            <Field label="Port"><Input value={port} onChange={(e) => setPort(e.target.value)} className="w-24 font-mono" placeholder="5432" required /></Field>
            <Field label="Proto"><select value={proto} onChange={(e) => setProto(e.target.value)} className="h-9 rounded-md border border-border-strong bg-bg px-2 text-sm"><option>tcp</option><option>udp</option></select></Field>
            <Field label="From" hint="empty = anywhere"><Input value={from} onChange={(e) => setFrom(e.target.value)} className="w-40 font-mono" placeholder="203.0.113.0/24" /></Field>
            <Field label="Comment"><Input value={comment} onChange={(e) => setComment(e.target.value)} className="w-36" placeholder="office" /></Field>
            <Button type="submit" className="h-9 text-xs">Allow</Button>
            {msg && <span className="text-xs text-danger">{msg}</span>}
          </form>}
        </>
      )}
    </Card>
  );
}

function PanelRestrict({ cidr, onChanged }: { cidr: string; onChanged: () => Promise<void> }) {
  const [v, setV] = useState(cidr); const [msg, setMsg] = useState<string | null>(null);
  const apply = async (c: string) => { if (c && !confirm(`Allow the panel port only from ${c}? Make sure you are connected through that range first, or you lock yourself out (your current IP is kept as a fallback).`)) return; setMsg(null); try { await api.panelRestrict(c); setMsg(c ? `Panel reachable only from ${c}.` : "Panel public again."); await onChanged(); } catch (e) { setMsg(err(e)); } };
  return (
    <div className="mt-3 flex flex-wrap items-end gap-2 border-t border-border pt-3">
      <Field label="Panel only via VPN" hint="CIDR of your VPN: 10.8.0.0/24 for wg-easy, 100.64.0.0/10 for Tailscale."><Input value={v} onChange={(e) => setV(e.target.value)} className="w-44 font-mono" placeholder="10.8.0.0/24" /></Field>
      <Button type="button" variant="secondary" className="h-9 text-xs" onClick={() => void apply(v)} disabled={!v}>Restrict</Button>
      {cidr && <Button type="button" variant="secondary" className="h-9 text-xs" onClick={() => void apply("")}>Make public again</Button>}
      {msg && <span className="text-xs text-ink-muted">{msg}</span>}
    </div>
  );
}

function SSHCard({ s, isAdmin, onChanged }: { s: SecurityState; isAdmin: boolean; onChanged: () => Promise<void> }) {
  const [cfg, setCfg] = useState<SSHSettings>(s.ssh);
  const [msg, setMsg] = useState<string | null>(null);
  useEffect(() => setCfg(s.ssh), [s.ssh]);
  const set = (p: Partial<SSHSettings>) => setCfg((c) => ({ ...c, ...p }));
  const apply = async (e: FormEvent) => { e.preventDefault(); setMsg(null); try { const r = await api.sshApply(cfg); setMsg(r.message); await onChanged(); } catch (er) { setMsg(err(er)); } };
  const confirm_ = async () => { try { await api.sshConfirm(); setMsg("Confirmed. The settings stay."); await onChanged(); } catch (er) { setMsg(err(er)); } };
  return (
    <Card title="SSH" description={s.report.linux ? (s.sshHasKeys ? "authorized_keys found. Settings are validated with sshd -t before reload." : "No authorized_keys found yet. Add your public key before turning off passwords.") : "Available on Linux servers."}>
      {s.sshRollback && <Alert tone="warning">A change is waiting. Open a new SSH session to make sure you can still get in, then <button type="button" onClick={() => void confirm_()} className="underline">confirm it</button>. Otherwise it rolls back in five minutes.</Alert>}
      <form onSubmit={apply} className="mt-3 grid gap-3 sm:grid-cols-2">
        <Field label="Port"><Input type="number" value={cfg.port} onChange={(e) => set({ port: +e.target.value })} disabled={!isAdmin || !s.report.linux} /></Field>
        <Field label="Max auth tries"><Input type="number" value={cfg.maxAuthTries} onChange={(e) => set({ maxAuthTries: +e.target.value })} disabled={!isAdmin || !s.report.linux} /></Field>
        <label className="flex items-center gap-1.5 text-sm"><input type="checkbox" checked={cfg.permitRootLogin} onChange={(e) => set({ permitRootLogin: e.target.checked })} disabled={!isAdmin} />Allow root login with a password</label>
        <label className="flex items-center gap-1.5 text-sm"><input type="checkbox" checked={cfg.passwordAuth} onChange={(e) => set({ passwordAuth: e.target.checked })} disabled={!isAdmin} />Password authentication</label>
        <label className="flex items-center gap-1.5 text-sm"><input type="checkbox" checked={cfg.pubkeyAuth} onChange={(e) => set({ pubkeyAuth: e.target.checked })} disabled={!isAdmin} />Public key authentication</label>
        <label className="flex items-center gap-1.5 text-sm"><input type="checkbox" checked={cfg.allowAgentForwarding} onChange={(e) => set({ allowAgentForwarding: e.target.checked })} disabled={!isAdmin} />Agent forwarding</label>
        {isAdmin && s.report.linux && <div className="flex items-center gap-2 sm:col-span-2"><Button type="submit" className="h-8 text-xs">Apply with 5-minute rollback</Button>{msg && <span className="text-xs text-ink-muted">{msg}</span>}</div>}
      </form>
    </Card>
  );
}

function ScanCard({ s, isAdmin, onChanged }: { s: SecurityState; isAdmin: boolean; onChanged: () => Promise<void> }) {
  const [image, setImage] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const scan = async (e: FormEvent) => { e.preventDefault(); setBusy(true); setMsg(null); try { const r = await api.scanImage(image); setMsg(`${r.critical} critical, ${r.high} high, ${r.medium} medium, ${r.low} low`); setOpen(r.target); await onChanged(); } catch (er) { setMsg(err(er)); } finally { setBusy(false); } };
  return (
    <Card title="Image scans" description="Trivy runs in a container and checks an image's packages against the CVE database. The first run downloads the database.">
      {isAdmin && <form onSubmit={scan} className="flex items-end gap-2"><Field label="Image"><Input value={image} onChange={(e) => setImage(e.target.value)} className="w-64 font-mono" placeholder="nginx:1.27-alpine" required /></Field><Button type="submit" className="h-9 text-xs" disabled={busy}>{busy ? "Scanning…" : "Scan"}</Button>{msg && <span className="text-xs text-ink-muted">{msg}</span>}</form>}
      <ul className="mt-3 divide-y divide-border text-xs">
        {s.scans.map((sc) => (
          <li key={sc.target} className="py-1.5">
            <button type="button" onClick={() => setOpen(open === sc.target ? null : sc.target)} className="flex w-full items-center justify-between text-left"><span className="font-mono">{sc.target}</span><span>{sc.error ? <span className="text-danger">failed</span> : <><span className={sc.critical ? "text-danger" : ""}>{sc.critical} crit</span> · <span className={sc.high ? "text-warning" : ""}>{sc.high} high</span> · {sc.medium} med · <span className="text-ink-muted">{fmt(sc.at)}</span></>}</span></button>
            {open === sc.target && <ul className="mt-1 divide-y divide-border rounded-md border border-border">{sc.findings.map((f) => <li key={f.id + f.package} className="px-2 py-1"><span className={f.severity === "CRITICAL" ? "text-danger" : "text-warning"}>{f.severity}</span> <a href={`https://nvd.nist.gov/vuln/detail/${f.id}`} target="_blank" rel="noreferrer" className="font-mono hover:underline">{f.id}</a> {f.package} {f.version}{f.fixed && ` → ${f.fixed}`}<div className="text-ink-muted">{f.title}</div></li>)}{sc.findings.length === 0 && <li className="px-2 py-1 text-ink-muted">{sc.error || "No critical or high findings."}</li>}</ul>}
          </li>
        ))}
        {s.scans.length === 0 && <li className="py-2 text-ink-muted">No scans yet.</li>}
      </ul>
    </Card>
  );
}
