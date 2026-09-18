import { useRef, useCallback, useEffect, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { api, RequestError, type Blocklist, type HostAudit, type SecurityState, type SSHSettings } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { Alert, Button, Card, Field, FieldAction, Input, Select } from "@/components/ui";
import { streamLines } from "@/lib/stream";
import { capLines } from "@/lib/logcap";
import { useDialog } from "@/lib/dialogs";

function err(e: unknown) { return e instanceof RequestError ? e.message : String(e); }
function fmt(s: string) { return s ? new Date(s).toLocaleString() : ""; }

export default function Security() {
  const ask = useDialog();
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const [s, setS] = useState<SecurityState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [out, setOut] = useState<{ title: string; text: string } | null>(null);
  const load = useCallback(() => api.security().then((x) => { setS(x); setError(null); }).catch((e) => setError(err(e))), []);
  useEffect(() => { void load(); }, [load]);

  // Two checks can share one fix (ssh-root and ssh-password are both
  // ssh-harden; firewall and firewall-docker are both firewall), so the
  // spinner is keyed to the row pressed, not to the fix it runs.
  const fix = async (id: string, key?: string) => {
    setBusy(key ?? id); setOut(null);
    try { const r = await api.securityFix(id); setOut({ title: `Applied ${id}`, text: r.output || "done" }); await load(); }
    catch (e) { setOut({ title: `${id} failed`, text: err(e) }); }
    finally { setBusy(null); }
  };
  // The SSH fix in the checks list arms a five-minute rollback, and the banner
  // offering to confirm it used to live only inside the SSH card further down
  // the page. Press Fix, never scroll, and the change quietly reverts — which
  // reads as the fix not working. The banner belongs where the button is.
  const confirmSSH = async () => {
    setBusy("ssh-confirm");
    try { await api.sshConfirm(); setOut({ title: "SSH change confirmed", text: "The settings stay." }); await load(); }
    catch (e) { setOut({ title: "Could not confirm", text: err(e) }); }
    finally { setBusy(null); }
  };
  const panic = async () => {
    if (!(await ask.confirm({
      title: "Lock this server down?",
      body: <>Everything coming in is blocked except your own address, <span className="font-mono">{s?.clientIp}</span>. Every other session is signed out and every API token is revoked. Your sites go offline until you undo it in the Firewall section below.</>,
      typeToConfirm: "PANIC",
      confirmLabel: "Lock it down",
      tone: "danger",
    }))) return;
    setBusy("panic");
    try { const r = await api.panic(); setOut({ title: "Locked down", text: r.message + "\n" + r.output }); await load(); } catch (e) { setOut({ title: "Panic failed", text: err(e) }); } finally { setBusy(null); }
  };

  if (error) return <div className="mx-auto max-w-6xl"><Alert>{error}</Alert></div>;
  if (!s) return <p className="text-sm text-ink-muted">Computing the score…</p>;
  const r = s.report;
  const tone = r.score >= 90 ? "text-success" : r.score >= 60 ? "text-warning" : "text-danger";
  const checks = r.checks ?? [];
  const failing = checks.filter((c) => c.status !== "pass");

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

      {s.sshRollback && (
        <Alert tone="warning">
          An SSH change is waiting. Open a new SSH session to check you can still get in, then{" "}
          <button type="button" onClick={() => void confirmSSH()} disabled={busy !== null} className="underline">
            {busy === "ssh-confirm" ? "confirming…" : "confirm it"}
          </button>. Otherwise it rolls back in five minutes.
        </Alert>
      )}

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[260px_minmax(0,1fr)]">
        <Card>
          <div className="text-center">
            <div className={`font-mono text-6xl font-semibold tabular-nums ${tone}`}>{r.score}</div>
            <div className="mt-1 text-sm text-ink-muted">Security Score out of {r.max}</div>
            <div className="mt-3 text-xs text-ink-muted">{failing.length === 0 ? "Everything checked passes." : `${failing.length} item${failing.length === 1 ? "" : "s"} to fix`}</div>
          </div>
        </Card>
        <Card title="Checks" description="Weighted. Fixes run as root on this server and are recorded in the audit log.">
          <ul className="divide-y divide-border text-sm">
            {checks.map((c) => (
              <li key={c.id} className="flex items-start gap-3 py-2">
                <span className={`mt-1.5 h-2 w-2 flex-none rounded-full ${c.status === "pass" ? "bg-success" : c.status === "fail" ? "bg-danger" : c.status === "warn" ? "bg-warning" : "bg-ink-faint"}`} />
                <div className="min-w-0 flex-1"><div className="font-medium">{c.title} {c.status !== "pass" && <span className="font-mono text-[11px] text-ink-faint" title="Points this is worth">+{c.weight}</span>}</div><div className="text-xs text-ink-muted">{c.detail}</div></div>
                {c.status !== "pass" && (c.fix && isAdmin && r.linux ? <Button variant="secondary" className="h-7 text-xs" disabled={busy !== null} onClick={() => void fix(c.fix!, c.id)} title={c.fixNote}>{busy === c.id ? "Working…" : "Fix"}</Button> : c.fixNote ? <span className="max-w-48 text-right text-[11px] text-ink-faint">{c.fixNote}</span> : null)}
              </li>
            ))}
          </ul>
        </Card>
      </div>
      {out && <Card title={out.title}><pre className="max-h-64 overflow-auto whitespace-pre-wrap font-mono text-xs">{out.text}</pre></Card>}

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <FirewallCard s={s} isAdmin={isAdmin} onChanged={load} onFix={() => void fix("firewall")} onFixRoutes={() => void fix("firewall-routes")} busy={busy} />
        <SSHCard s={s} isAdmin={isAdmin} onChanged={load} />
      </div>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Card title="Blocked IPs" description="fail2ban bans after repeated failed SSH logins.">
          <ul className="divide-y divide-border text-xs">{s.banned.map((b) => <li key={b} className="flex items-center justify-between py-1.5"><span className="font-mono">{b}</span>{isAdmin && <button type="button" onClick={async () => { await api.unban(b.split(" ")[0]); await load(); }} className="-my-1 py-1 text-ink-muted hover:text-ink">Unban</button>}</li>)}{s.banned.length === 0 && <li className="py-2 text-ink-muted">{r.linux ? "Nobody is banned right now." : "Available on Linux servers."}</li>}</ul>
        </Card>
        <ScanCard s={s} isAdmin={isAdmin} onChanged={load} />
      </div>
      {isAdmin && r.linux && <BlocklistCard onChanged={load} />}
      {isAdmin && r.linux && <div className="grid grid-cols-1 gap-4 lg:grid-cols-2"><ServerSetup onChanged={load} /><HostAuditCard /></div>}
      {isAdmin && r.linux && <LynisCard onChanged={load} />}
      <Diagnostics />
    </div>
  );
}

// Countries worth offering by name. Anything else can be typed: this is the
// short list of codes people actually reach for, not a atlas.
const COUNTRIES = [
  { cc: "cn", name: "China" }, { cc: "ru", name: "Russia" }, { cc: "kp", name: "North Korea" },
  { cc: "ir", name: "Iran" }, { cc: "in", name: "India" }, { cc: "br", name: "Brazil" },
  { cc: "vn", name: "Vietnam" }, { cc: "id", name: "Indonesia" }, { cc: "ng", name: "Nigeria" },
];

/**
 * Lists of addresses to drop before they reach anything.
 *
 * Deliberately not part of "Fix everything": it enforces lists somebody else
 * maintains, and one of them having a bad day is the server's bad day too. The
 * page says what each list is, how many networks are loaded, and when they were
 * last fetched, so the decision is made with the facts in view.
 */
function BlocklistCard({ onChanged }: { onChanged: () => Promise<void> }) {
  const ask = useDialog();
  const [b, setB] = useState<Blocklist | null>(null);
  const [sources, setSources] = useState<string[]>([]);
  const [countries, setCountries] = useState<string[]>([]);
  const [extra, setExtra] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const load = useCallback(() => api.blocklist().then((x) => { setB(x); setSources(x.sources); setCountries(x.countries); }).catch(() => {}), []);
  useEffect(() => { void load(); }, [load]);
  if (!b) return null;

  const toggle = (list: string[], set: (v: string[]) => void, id: string) =>
    set(list.includes(id) ? list.filter((x) => x !== id) : [...list, id]);

  const apply = async () => {
    const all = [...countries, ...extra.split(/[,\s]+/).map((c) => c.trim().toLowerCase()).filter((c) => c.length === 2)];
    setBusy("apply"); setMsg(null);
    try { const r = await api.blocklistSet({ sources, countries: [...new Set(all)] }); setB(r); setExtra(""); setMsg(`${r.entries} networks loaded.`); await onChanged(); }
    catch (e) { setMsg(err(e)); } finally { setBusy(null); }
  };
  const refresh = async () => {
    setBusy("refresh"); setMsg(null);
    try { const r = await api.blocklistRefresh(); setB(r); setMsg(`${r.entries} networks loaded.`); await onChanged(); }
    catch (e) { setMsg(err(e)); } finally { setBusy(null); }
  };
  const off = async () => {
    if (!(await ask.confirm({ title: "Stop dropping these addresses?", body: "The rules and the list come out immediately. Nothing else about the firewall changes.", confirmLabel: "Turn off", tone: "danger" }))) return;
    setBusy("off"); setMsg(null);
    try { await api.blocklistOff(); await load(); await onChanged(); setMsg("Off. Nothing is being dropped by list."); }
    catch (e) { setMsg(err(e)); } finally { setBusy(null); }
  };

  return (
    <Card title="Blocklist" description="Drop connections from networks that are known to be hostile, and from countries this server has no reason to hear from, before they reach the proxy or any container.">
      {!b.available && <Alert tone="warning">ipset is not installed yet. Turning this on installs it.</Alert>}
      {b.lastError && <div className="mb-3"><Alert tone="warning">{b.lastError}</Alert></div>}
      <div className="flex flex-wrap items-center gap-2 text-xs text-ink-muted">
        <span className={`inline-flex items-center gap-1.5 ${b.enabled ? "text-success" : ""}`}>
          <span aria-hidden className={`h-1.5 w-1.5 rounded-full ${b.enabled ? "bg-success" : "bg-border-strong"}`} />
          {b.enabled ? `${b.entries.toLocaleString()} networks dropped` : "Off"}
        </span>
        {b.updatedAt && <span>· fetched {new Date(b.updatedAt).toLocaleString()}</span>}
      </div>
      <div className="mt-3 space-y-2">
        {b.catalog.map((src) => (
          <label key={src.id} className="-my-1 flex items-start gap-2 py-1 text-sm">
            <input type="checkbox" className="mt-1" checked={sources.includes(src.id)} onChange={() => toggle(sources, setSources, src.id)} />
            <span><span className="font-medium">{src.title}</span><span className="block text-xs text-ink-muted">{src.note}</span></span>
          </label>
        ))}
      </div>
      <div className="mt-4">
        <span className="text-sm font-medium">Countries</span>
        <p className="mt-0.5 text-xs text-ink-muted">Whole-country blocks are blunt: they stop customers and VPN exits as readily as attackers. Use them when a server only ever serves one part of the world.</p>
        <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1">
          {COUNTRIES.map((c) => (
            <label key={c.cc} className="-my-1 flex items-center gap-1.5 py-1 text-xs">
              <input type="checkbox" checked={countries.includes(c.cc)} onChange={() => toggle(countries, setCountries, c.cc)} />
              {c.name}
            </label>
          ))}
        </div>
        <Input value={extra} onChange={(e) => setExtra(e.target.value)} placeholder="More codes, comma separated: pk, tr" className="mt-2 h-8 w-full max-w-sm text-xs" aria-label="More country codes" />
      </div>
      <div className="mt-4 flex flex-wrap items-center gap-2">
        <Button className="h-8 text-xs" disabled={busy !== null} onClick={() => void apply()}>{busy === "apply" ? "Fetching…" : b.enabled ? "Save and fetch" : "Turn on"}</Button>
        {b.enabled && <Button variant="secondary" className="h-8 text-xs" disabled={busy !== null} onClick={() => void refresh()}>{busy === "refresh" ? "Fetching…" : "Refresh now"}</Button>}
        {b.enabled && <button type="button" className="-my-1 py-1 text-xs text-danger hover:underline" onClick={() => void off()}>Turn off</button>}
        {msg && <span className="text-xs text-ink-muted">{msg}</span>}
      </div>
      <p className="mt-2 text-xs text-ink-muted">Your own address is never dropped, whatever a list says. Lists refresh daily and are reloaded after a reboot.</p>
    </Card>
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

function ServerSetup({ onChanged }: { onChanged: () => Promise<void> }) {
  const [user, setUser] = useState(""); const [key, setKey] = useState(""); const [keyUser, setKeyUser] = useState("root"); const [key2, setKey2] = useState(""); const [tz, setTz] = useState(""); const [cur, setCur] = useState("");
  const [msg, setMsg] = useState<string | null>(null);
  useEffect(() => { void api.hostTimezone().then((r) => { setCur(r.timezone); setTz(r.timezone); }).catch(() => {}); }, []);
  const run = async (fn: () => Promise<{ output?: string; timezone?: string }>) => { setMsg(null); try { const r = await fn(); setMsg(r.output ?? (r.timezone ? `Timezone is now ${r.timezone}.` : "Done.")); await onChanged(); } catch (er) { setMsg(err(er)); } };
  return (
    <Card title="Server setup" description="The three steps every fresh server needs: a sudo user so root stays unused, your key on it, the right clock.">
      <form onSubmit={(e) => { e.preventDefault(); void run(() => api.hostUser(user, key)); }} className="grid grid-cols-1 gap-2 sm:grid-cols-[140px_minmax(0,1fr)_auto]">
        <Input value={user} onChange={(e) => setUser(e.target.value)} placeholder="deploy" className="font-mono" required />
        <Input value={key} onChange={(e) => setKey(e.target.value)} placeholder="ssh-ed25519 AAAA… (optional)" className="font-mono" />
        <Button type="submit" variant="secondary" className="h-9 text-xs">Create sudo user</Button>
      </form>
      <form onSubmit={(e) => { e.preventDefault(); void run(() => api.hostSSHKey(keyUser, key2)); }} className="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-[140px_minmax(0,1fr)_auto]">
        <Input value={keyUser} onChange={(e) => setKeyUser(e.target.value)} className="font-mono" required />
        <Input value={key2} onChange={(e) => setKey2(e.target.value)} placeholder="ssh-ed25519 AAAA…" className="font-mono" required />
        <Button type="submit" variant="secondary" className="h-9 text-xs">Add SSH key</Button>
      </form>
      <form onSubmit={(e) => { e.preventDefault(); void run(() => api.hostTimezoneSet(tz)); }} className="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-[140px_minmax(0,1fr)_auto]">
        <span className="self-center text-xs text-ink-muted">Timezone {cur && <span className="font-mono">({cur})</span>}</span>
        <Input value={tz} onChange={(e) => setTz(e.target.value)} placeholder="Europe/Skopje" className="font-mono" required />
        <Button type="submit" variant="secondary" className="h-9 text-xs">Set timezone</Button>
      </form>
      {msg && <pre className="mt-2 whitespace-pre-wrap font-mono text-xs text-ink-muted">{msg}</pre>}
      <p className="mt-2 text-xs text-ink-muted">Then turn off root and password logins under SSH; the five-minute rollback protects you if the key does not work.</p>
    </Card>
  );
}

function HostAuditCard() {
  const [a, setA] = useState<HostAudit | null>(null); const [busy, setBusy] = useState(false); const [msg, setMsg] = useState<string | null>(null);
  useEffect(() => { void api.hostAudit().then(setA).catch(() => {}); }, []);
  const run = async (rk: boolean) => { setBusy(true); setMsg(null); try { setA(await api.hostAuditRun(rk)); } catch (er) { setMsg(err(er)); } finally { setBusy(false); } };
  const baseline = async () => { setBusy(true); setMsg(null); try { const r = await api.hostBaseline(); setMsg(`Baseline recorded for ${r.files} files under /etc.`); } catch (er) { setMsg(err(er)); } finally { setBusy(false); } };
  const list = (title: string, items: string[]) => items.length > 0 && <details className="mt-1 text-xs"><summary className="cursor-pointer">{title}: {items.length}</summary><pre className="mt-1 max-h-40 overflow-auto font-mono text-[11px]">{items.join("\n")}</pre></details>;
  return (
    <Card title="Host audit" description="Unexpected setuid binaries, world-writable files, changes under /etc since the baseline, and rkhunter for rootkits. Takes a minute.">
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="secondary" className="h-8 text-xs" disabled={busy} onClick={() => void run(false)}>{busy ? "Auditing…" : "Run audit"}</Button>
        <Button variant="secondary" className="h-8 text-xs" disabled={busy} onClick={() => void run(true)}>Run with rkhunter</Button>
        <Button variant="secondary" className="h-8 text-xs" disabled={busy} onClick={() => void baseline()} title="Record /etc as it is now; later audits list every change">Set /etc baseline</Button>
        {msg && <span className="text-xs text-ink-muted">{msg}</span>}
      </div>
      {a && <div className="mt-2 text-sm">
        <p className="text-xs text-ink-muted">Last audit {fmt(a.at)}{a.baselineAt && ` · baseline ${fmt(a.baselineAt)}`}</p>
        {a.suid.length === 0 && a.worldWritable.length === 0 && a.etcChanged.length + a.etcAdded.length + a.etcRemoved.length === 0 && !a.rkhunter && <p className="mt-1 text-success">Nothing unusual.</p>}
        {list("Setuid binaries outside the known set", a.suid)}{list("World-writable files", a.worldWritable)}{list("/etc files changed", a.etcChanged)}{list("/etc files added", a.etcAdded)}{list("/etc files removed", a.etcRemoved)}
        {a.rkhunter && <details className="mt-1 text-xs" open><summary className="cursor-pointer text-warning">rkhunter warnings</summary><pre className="mt-1 max-h-48 overflow-auto font-mono text-[11px]">{a.rkhunter}</pre></details>}
        {a.rkhunterRan && !a.rkhunter && <p className="mt-1 text-xs text-success">rkhunter: no warnings.</p>}
        {a.notes.map((n) => <p key={n} className="mt-1 text-xs text-ink-muted">{n}</p>)}
      </div>}
    </Card>
  );
}

function Diagnostics() {
  const [host, setHost] = useState(""); const [port, setPort] = useState("443"); const [tool, setTool] = useState("ping");
  const [out, setOut] = useState<string[]>([]); const [busy, setBusy] = useState(false);
  // A traceroute runs for a while. Without holding the stop function it kept
  // streaming after the page was left, and started again on the next run.
  const stop = useRef<(() => void) | null>(null);
  useEffect(() => () => stop.current?.(), []);
  const run = async (e: FormEvent) => {
    e.preventDefault(); setOut([]); setBusy(true);
    stop.current?.();
    if (tool === "port") { try { const r = await api.portCheck(host, +port); setOut([`${host}:${port} is ${r.message}`]); } catch (er) { setOut([err(er)]); } setBusy(false); return; }
    stop.current = streamLines(`/api/v1/diagnostics?tool=${tool}&host=${encodeURIComponent(host)}`, (l) => setOut((p) => capLines(p, l)), (end) => { if (!end.ok) setOut((p) => capLines(p, end.message ?? "the command failed")); setBusy(false); stop.current = null; });
  };
  return (
    <Card title="Network diagnostics" description="Run from this server, so you see what the server sees.">
      <form onSubmit={run} className="flex flex-wrap items-start gap-2">
        <Field label="Tool"><Select value={tool} onChange={(e) => setTool(e.target.value)} className="w-auto"><option value="ping">ping</option><option value="traceroute">traceroute</option><option value="dig">dig</option><option value="port">port check</option></Select></Field>
        <Field label="Host"><Input value={host} onChange={(e) => setHost(e.target.value)} className="w-56 font-mono" placeholder="example.com" required /></Field>
        {tool === "port" && <Field label="Port"><Input value={port} onChange={(e) => setPort(e.target.value)} className="w-20 font-mono" /></Field>}
        <FieldAction><Button type="submit" className="h-9 text-xs" disabled={busy}>{busy ? "Running…" : "Run"}</Button></FieldAction>
      </form>
      {out.length > 0 && <pre className="mt-3 max-h-64 overflow-auto rounded-md border border-border bg-[#0A0A0A] p-3 font-mono text-xs text-[#FAFAFA] whitespace-pre-wrap">{out.join("\n")}</pre>}
    </Card>
  );
}

function FirewallCard({ s, isAdmin, onChanged, onFix, onFixRoutes, busy }: { s: SecurityState; isAdmin: boolean; onChanged: () => Promise<void>; onFix: () => void; onFixRoutes: () => void; busy: string | null }) {
  const ask = useDialog();
  const fw = s.firewall;
  const [port, setPort] = useState(""); const [proto, setProto] = useState("tcp"); const [from, setFrom] = useState(""); const [comment, setComment] = useState("");
  const [routed, setRouted] = useState(true);
  const [msg, setMsg] = useState<string | null>(null);
  const add = async (e: FormEvent) => { e.preventDefault(); setMsg(null); try { await api.firewallAllow({ port, proto, from, comment, routed }); setPort(""); setFrom(""); setComment(""); await onChanged(); } catch (er) { setMsg(err(er)); } };
  const missing = fw.missingRoutes ?? [];
  return (
    <Card title="Firewall" description={!s.report.linux ? "Available on Linux servers." : !fw.installed ? "ufw is not installed." : fw.active ? `Active. ${fw.dockerAware ? "Docker honours it." : "Docker can bypass it: run the firewall fix."}` : "Installed but inactive."}>
      {s.report.linux && !fw.active && isAdmin && <div className="space-y-2"><p className="text-sm text-ink-muted">Opens SSH and the proxy ports to the internet, and reaches the panel from your address only. Everything else is denied, on both the input and the forward chain, so published container ports are covered too.</p><Button className="h-8 text-xs" disabled={busy !== null} onClick={onFix}>Turn the firewall on</Button></div>}
      {missing.length > 0 && (
        <div className="mb-3 rounded-md border border-danger/40 bg-danger-soft p-3 text-sm text-danger">
          <p className="font-medium">Every domain behind the proxy is unreachable.</p>
          <p className="mt-1">Port {missing.join(" and ")} reaches the proxy container over the forward chain, and there is no rule allowing it. The server still answers on its own ports, so this looks like a proxy fault but it is the firewall.</p>
          {isAdmin && <Button className="mt-2 h-8 text-xs" disabled={busy !== null} onClick={() => void onFixRoutes()}>{busy === "firewall-routes" ? "Adding rules…" : "Add the missing rules"}</Button>}
        </div>
      )}
      {fw.active && (
        <>
          <table className="w-full text-xs"><tbody className="divide-y divide-border">
            {fw.rules.map((r, i) => <tr key={i}><td className="py-1.5 font-mono">{r.port}{r.proto && `/${r.proto}`}</td><td className="py-1.5 text-ink-muted">{r.routed ? <span title="Reaches a port published by a container" className="rounded-sm bg-surface-2 px-1.5 py-0.5 text-[11px]">to containers</span> : <span title="Reaches a port the server itself listens on" className="rounded-sm bg-surface-2 px-1.5 py-0.5 text-[11px]">to this server</span>}</td><td className="py-1.5 text-ink-muted">from {r.from}</td><td className="py-1.5 text-ink-muted">{r.comment}</td><td className="py-1.5 text-right">{isAdmin && <button type="button" onClick={async () => { if (await ask.confirm({ title: `Remove the rule for port ${r.port}?`, body: "Whatever that rule was letting through stops reaching this server.", confirmLabel: "Remove rule", tone: "danger" })) { await api.firewallDelete({ port: r.port, proto: r.proto, from: r.from, routed: !!r.routed }); await onChanged(); } }} className="-my-1 py-1 text-danger hover:underline">Remove</button>}</td></tr>)}
          </tbody></table>
          {isAdmin && <PanelRestrict cidr={s.panelCidr ?? ""} onChanged={onChanged} />}
          {isAdmin && <form onSubmit={add} className="mt-3 flex flex-wrap items-start gap-2 border-t border-border pt-3">
            <Field label="Port"><Input value={port} onChange={(e) => setPort(e.target.value)} className="w-24 font-mono" placeholder="5432" required /></Field>
            <Field label="Proto"><Select value={proto} onChange={(e) => setProto(e.target.value)} className="w-auto"><option>tcp</option><option>udp</option></Select></Field>
            <Field label="From" hint="empty = anywhere"><Input value={from} onChange={(e) => setFrom(e.target.value)} className="w-40 font-mono" placeholder="203.0.113.0/24" /></Field>
            <Field label="Comment"><Input value={comment} onChange={(e) => setComment(e.target.value)} className="w-36" placeholder="office" /></Field>
            <Field label="Reaches" hint="A container port needs a forward rule; an allow rule alone never matches it."><Select value={routed ? "container" : "host"} onChange={(e) => setRouted(e.target.value === "container")} className="w-44"><option value="container">A container</option><option value="host">This server</option></Select></Field>
            <FieldAction className="flex items-center gap-2"><Button type="submit" className="h-9 text-xs">Allow</Button>{msg && <span className="text-xs text-danger">{msg}</span>}</FieldAction>
          </form>}
        </>
      )}
    </Card>
  );
}

function PanelRestrict({ cidr, onChanged }: { cidr: string; onChanged: () => Promise<void> }) {
  const ask = useDialog();
  const [v, setV] = useState(cidr); const [msg, setMsg] = useState<string | null>(null);
  const apply = async (c: string) => { if (c && !(await ask.confirm({ title: `Reach the panel only from ${c}?`, body: "Connect through that range first and check it works. Your current address is kept as a fallback, but if it changes, SSH is the way back.", confirmLabel: "Restrict the panel", tone: "danger" }))) return; setMsg(null); try { await api.panelRestrict(c); setMsg(c ? `Panel reachable only from ${c}.` : "Panel public again."); await onChanged(); } catch (e) { setMsg(err(e)); } };
  return (
    <div className="mt-3 flex flex-wrap items-start gap-2 border-t border-border pt-3">
      <Field label="Panel only via VPN" hint="CIDR of your VPN: 10.8.0.0/24 for wg-easy, 100.64.0.0/10 for Tailscale."><Input value={v} onChange={(e) => setV(e.target.value)} className="w-44 font-mono" placeholder="10.8.0.0/24" /></Field>
      <FieldAction className="flex flex-wrap items-center gap-2">
        <Button type="button" variant="secondary" className="h-9 text-xs" onClick={() => void apply(v)} disabled={!v}>Restrict</Button>
        {cidr && <Button type="button" variant="secondary" className="h-9 text-xs" onClick={() => void apply("")}>Make public again</Button>}
        {msg && <span className="text-xs text-ink-muted">{msg}</span>}
      </FieldAction>
    </div>
  );
}

function SSHCard({ s, isAdmin, onChanged }: { s: SecurityState; isAdmin: boolean; onChanged: () => Promise<void> }) {
  const [cfg, setCfg] = useState<SSHSettings>(s.ssh);
  const [msg, setMsg] = useState<string | null>(null);
  useEffect(() => setCfg(s.ssh), [s.ssh]);
  const set = (p: Partial<SSHSettings>) => setCfg((c) => ({ ...c, ...p }));
  const apply = async (e: FormEvent) => { e.preventDefault(); setMsg(null); try { const r = await api.sshApply(cfg); setMsg(r.message); await onChanged(); } catch (er) { setMsg(err(er)); } };
  return (
    <Card title="SSH" description={s.report.linux ? (s.sshHasKeys ? "authorized_keys found. Settings are validated with sshd -t before reload." : "No authorized_keys found yet. Add your public key before turning off passwords.") : "Available on Linux servers."}>
      <form onSubmit={apply} className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
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
  const [params] = useSearchParams();
  // Arriving from a container's "See the findings" names the image, so open
  // that scan and bring it into view rather than dropping the person at the top
  // of a long page with nothing expanded.
  const wanted = params.get("scan");
  const [image, setImage] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(wanted);
  const card = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!wanted) return;
    setOpen(wanted);
    card.current?.scrollIntoView({ block: "start", behavior: "smooth" });
  }, [wanted]);
  const scan = async (e: FormEvent) => { e.preventDefault(); setBusy(true); setMsg(null); try { const r = await api.scanImage(image); setMsg(`${r.critical} critical, ${r.high} high, ${r.medium} medium, ${r.low} low`); setOpen(r.target); await onChanged(); } catch (er) { setMsg(err(er)); } finally { setBusy(false); } };
  return (
    <Card title="Image scans" description="Trivy runs in a container and checks an image's packages against the CVE database. The first run downloads the database.">
      <div ref={card} />
      {wanted && !s.scans.some((sc) => sc.target === wanted) && (
        <p className="mb-3 text-sm text-ink-muted">There is no scan for <span className="font-mono">{wanted}</span> yet. Scan it below, or from the container.</p>
      )}
      {isAdmin && <form onSubmit={scan} className="flex items-start gap-2"><Field label="Image"><Input value={image} onChange={(e) => setImage(e.target.value)} className="w-64 font-mono" placeholder="nginx:1.27-alpine" required /></Field><FieldAction className="flex items-center gap-2"><Button type="submit" className="h-9 text-xs" disabled={busy}>{busy ? "Scanning…" : "Scan"}</Button>{msg && <span className="text-xs text-ink-muted">{msg}</span>}</FieldAction></form>}
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
