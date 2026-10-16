import { useCallback, useEffect, useState, type FormEvent } from "react";
import QRCode from "qrcode";
import {
  api, getServer, RequestError,
  type FleetServer, type VpnAccess, type VpnIssued, type VpnPeer, type VpnPeerInput, type VpnState, type WgEasyFound,
} from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useDialog } from "@/lib/dialogs";
import { bytes } from "@/lib/format";
import { pollInterval } from "@/lib/poll";
import { clientConfig, generateKeypair } from "@/lib/wgkey";
import { Alert, Button, Card, Field, Input, Select } from "@/components/ui";
import Modal from "@/components/Modal";

function err(e: unknown) { return e instanceof RequestError ? e.message : String(e); }

function ago(iso?: string): string {
  if (!iso) return "never";
  const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} d ago`;
}

const ACCESS: Record<Exclude<VpnAccess, "server">, { label: string; blurb: string }> = {
  admin: { label: "Admin", blurb: "The panel, SSH and every port on this server." },
  apps: { label: "Apps", blurb: "Only the ports you list, such as 80 and 443 for the sites on this server." },
  internet: { label: "Internet only", blurb: "Browses through this server. Reaches nothing on it." },
};

export default function Vpn() {
  const ask = useDialog();
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const [v, setV] = useState<VpnState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [editing, setEditing] = useState<VpnPeer | "new" | null>(null);
  const [issued, setIssued] = useState<{ cfg: string; name: string } | null>(null);
  const [fleet, setFleet] = useState<FleetServer[]>([]);
  const [wgEasy, setWgEasy] = useState<WgEasyFound[]>([]);
  const local = getServer() === "local";

  const load = useCallback(() => api.vpn().then((x) => { setV(x); setError(null); }).catch((e) => setError(err(e))), []);
  useEffect(() => { void load(); return pollInterval(() => void load(), 5000); }, [load]);
  useEffect(() => {
    if (!isAdmin) return;
    if (local) api.servers().then((x) => setFleet(x.servers.filter((s) => s.status === "ready" || s.vpnAddress))).catch(() => {});
    api.vpnImportScan().then(setWgEasy).catch(() => {});
  }, [isAdmin, local, v?.status.config.enabled]);

  const run = async (key: string, fn: () => Promise<unknown>, done?: string) => {
    setBusy(key); setNote(null); setError(null);
    try { await fn(); if (done) setNote(done); await load(); }
    catch (e) { setError(err(e)); }
    finally { setBusy(null); }
  };

  // The private key is made here and lives only in this closure and the
  // dialog it opens. Nothing that leaves the browser carries it.
  const issue = async (name: string, send: (publicKey: string) => Promise<VpnIssued>) => {
    const kp = await generateKeypair();
    const iss = await send(kp.publicKey);
    const cfg = clientConfig({
      privateKey: kp.privateKey, address: iss.peer.address, presharedKey: iss.presharedKey,
      serverPublicKey: iss.serverPublicKey, endpoint: iss.endpoint, allowedIps: iss.allowedIps, dns: iss.dns,
    });
    setIssued({ cfg, name });
  };

  if (!v) {
    return <div className="mx-auto max-w-6xl">{error ? <Alert>{error}</Alert> : <p className="text-ink-muted">Loading…</p>}</div>;
  }
  const st = v.status;
  const c = st.config;
  const devices = st.peers.filter((p) => p.kind === "device");
  const servers = st.peers.filter((p) => p.kind === "server");
  const online = st.peers.filter((p) => p.online).length;

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-[-0.02em]">VPN</h1>
          <p className="mt-1 text-ink-muted">WireGuard in the kernel. A device's key is made in your browser and never stored on the server.</p>
        </div>
        {c.enabled && isAdmin && (
          <div className="flex flex-wrap gap-2">
            <Button className="h-8 text-xs" onClick={() => setEditing("new")}>Add device</Button>
            <Button variant="secondary" className="h-8 text-xs" disabled={busy !== null} onClick={async () => {
              const ok = await ask.confirm({
                title: "Turn the VPN off?",
                body: "Every device disconnects. Devices and keys are kept, so turning it back on brings them all back without touching a phone. A lockdown is undone first.",
                confirmLabel: "Turn off", tone: "danger",
              });
              if (ok) await run("off", () => api.vpnDisable(), "The VPN is off.");
            }}>{busy === "off" ? "Turning off…" : "Turn off"}</Button>
          </div>
        )}
      </div>

      {error && <Alert>{error}</Alert>}
      {note && <Alert tone="success">{note}</Alert>}
      {!st.supported && <Alert tone="warning">{st.note}</Alert>}

      {st.lockdown.pending && <PendingLockdown v={v} busy={busy} onConfirm={() => void run("confirm", () => api.vpnConfirm(), "Confirmed. The panel and SSH stay VPN-only.")} />}

      {!c.enabled ? (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
          <EnableCard v={v} isAdmin={isAdmin} busy={busy} onSubmit={(b) => void run("enable", () => api.vpnEnable(b), "The VPN is on.")} />
          <Card title="How it works">
            <ul className="list-disc space-y-1.5 pl-5 text-sm text-ink-muted">
              <li>Add a device and scan the QR code with the WireGuard app. Its private key is generated in this browser and never sent here.</li>
              <li>Each device gets an access level, enforced in the firewall: admin, apps on chosen ports, or internet only.</li>
              <li>Then limit the panel and SSH to the VPN. It undoes itself unless you confirm from inside the tunnel within two minutes.</li>
              <li>Servers in your fleet can join too, so their SSH need not face the internet.</li>
            </ul>
          </Card>
          {isAdmin && wgEasy.some((w) => w.plan) && <ImportCard found={wgEasy} v={v} busy={busy} run={run} ask={ask} />}
        </div>
      ) : (
        <>
          <Card>
            <div className="grid grid-cols-2 gap-4 text-sm sm:grid-cols-4">
              <Stat label="Status" value={st.up ? "Running" : "Not running"} tone={st.up ? "text-success" : "text-danger"} />
              <Stat label="Connected" value={`${online} of ${st.peers.length}`} />
              <Stat label="Server address" value={st.address} mono />
              <Stat label="Endpoint" value={`${c.endpoint}:${c.port}`} mono />
            </div>
            <div className="mt-3 break-all font-mono text-[11px] text-ink-faint">Server key {c.publicKey} · range {c.subnet} · DNS {c.dns}</div>
          </Card>

          <Card title="Devices" description="Revoking a device takes effect at once. A device past its expiry is taken out of the tunnel on its own.">
            {devices.length === 0 ? (
              <div className="py-6 text-center text-sm text-ink-muted">
                No devices yet.{isAdmin && <> <button type="button" className="-my-1 py-1 text-accent underline" onClick={() => setEditing("new")}>Add your first one</button>.</>}
              </div>
            ) : (
              <ul className="divide-y divide-border">
                {devices.map((p) => (
                  <PeerRow key={p.id} p={p} isAdmin={isAdmin} busy={busy}
                    onEdit={() => setEditing(p)}
                    onRekey={async () => {
                      const ok = await ask.confirm({ title: `New key for ${p.name}?`, body: "The key on the device stops working now. You get a new QR code to scan, with the same address and access.", confirmLabel: "New key", tone: "danger" });
                      if (ok) await run("rekey-" + p.id, () => issue(p.name, (pub) => api.vpnPeerRekey(p.id, pub)));
                    }}
                    onRemove={async () => {
                      const ok = await ask.confirm({ title: `Remove ${p.name}?`, body: "It is disconnected now and its key is forgotten. Adding it again means scanning a new code.", confirmLabel: "Remove", tone: "danger" });
                      if (ok) await run("rm-" + p.id, () => api.vpnPeerRemove(p.id), `${p.name} was removed.`);
                    }} />
                ))}
              </ul>
            )}
          </Card>

          {isAdmin && <LockdownCard v={v} busy={busy} run={run} ask={ask} />}

          {isAdmin && local && (fleet.length > 0 || servers.length > 0) && (
            <Card title="Fleet servers" description="A server on the VPN is managed through it, so its SSH can be closed to the internet. If the tunnel is down the panel falls back to the public address.">
              <ul className="divide-y divide-border text-sm">
                {fleet.map((f) => {
                  const peer = servers.find((s) => s.fleetServerId === f.id);
                  return (
                    <li key={f.id} className="flex flex-wrap items-center gap-3 py-2.5">
                      <span className={`h-2 w-2 flex-none rounded-full ${peer?.online ? "bg-success" : "bg-ink-faint"}`} />
                      <div className="min-w-0 flex-1">
                        <div className="font-medium">{f.name}</div>
                        <div className="text-xs text-ink-muted">{f.vpnAddress ? <>On the VPN at <span className="font-mono">{f.vpnAddress}</span>{peer && ` · last seen ${ago(peer.lastHandshake)}`}</> : <>Reached at its public address, <span className="font-mono">{f.host}</span></>}</div>
                      </div>
                      {f.vpnAddress
                        ? <Button variant="secondary" className="h-7 text-xs" disabled={busy !== null} onClick={async () => {
                            const ok = await ask.confirm({ title: `Take ${f.name} off the VPN?`, body: "It goes back to being reached at its public address, so its SSH must be reachable there.", confirmLabel: "Take off", tone: "danger" });
                            if (ok) await run("fl-" + f.id, async () => { const r = await api.vpnFleetLeave(f.id); setFleet((xs) => xs.map((x) => x.id === f.id ? { ...x, vpnAddress: undefined } : x)); if (r?.note) throw new Error(r.note); }, `${f.name} is off the VPN.`);
                          }}>{busy === "fl-" + f.id ? "Working…" : "Take off"}</Button>
                        : <Button className="h-7 text-xs" disabled={busy !== null} onClick={() => void run("fl-" + f.id, async () => { const r = await api.vpnFleetJoin(f.id); setFleet((xs) => xs.map((x) => x.id === f.id ? { ...x, vpnAddress: r.address } : x)); }, `${f.name} joined the VPN and is now managed through it.`)}>{busy === "fl-" + f.id ? "Joining…" : "Join the VPN"}</Button>}
                    </li>
                  );
                })}
              </ul>
            </Card>
          )}
        </>
      )}

      {st.uplink && (
        <Card title="This server is on its controller's VPN" description="The controller manages it through this tunnel. It reaches the controller and nothing else.">
          <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
            <span className="flex items-center gap-2"><span className={`h-2 w-2 rounded-full ${st.uplink.online ? "bg-success" : "bg-danger"}`} />{st.uplink.online ? "Connected" : "Not connected"}</span>
            <span>Address <span className="font-mono">{st.uplink.address}</span></span>
            <span>Controller <span className="font-mono">{st.uplink.serverAddress}</span> via <span className="font-mono">{st.uplink.endpoint}</span></span>
            <span className="text-ink-muted">Last handshake {ago(st.uplink.lastHandshake)}</span>
          </div>
        </Card>
      )}

      {editing && (
        <PeerForm peer={editing === "new" ? null : editing} onClose={() => setEditing(null)} onSave={async (inp) => {
          if (editing === "new") {
            await issue(inp.name, (pub) => api.vpnPeerAdd({ ...inp, publicKey: pub }));
          } else {
            await api.vpnPeerUpdate(editing.id, inp);
            setNote(`${inp.name} was saved.`);
          }
          setEditing(null);
          await load();
        }} />
      )}
      {issued && <IssuedModal cfg={issued.cfg} name={issued.name} onClose={() => setIssued(null)} />}
    </div>
  );
}

function Stat({ label, value, tone = "", mono }: { label: string; value: string; tone?: string; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <div className="text-xs text-ink-muted">{label}</div>
      <div className={`mt-0.5 truncate font-medium ${mono ? "font-mono text-[13px]" : ""} ${tone}`}>{value}</div>
    </div>
  );
}

function PeerRow({ p, isAdmin, busy, onEdit, onRekey, onRemove }: { p: VpnPeer; isAdmin: boolean; busy: string | null; onEdit: () => void; onRekey: () => void; onRemove: () => void }) {
  const level = p.access === "server" ? "Server" : ACCESS[p.access].label;
  return (
    <li className="flex flex-wrap items-center gap-x-4 gap-y-2 py-2.5 text-sm">
      <span className={`h-2 w-2 flex-none rounded-full ${p.expired ? "bg-danger" : p.online ? "bg-success" : "bg-ink-faint"}`} title={p.expired ? "Expired" : p.online ? "Connected" : "Not connected"} />
      <div className="min-w-0 flex-1 basis-48">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-medium">{p.name}</span>
          <span className="rounded border border-border px-1.5 text-[11px] text-ink-muted">{level}{p.access === "apps" && `: ${p.ports.join(", ")}`}</span>
          {p.fullTunnel && p.access !== "internet" && <span className="rounded border border-border px-1.5 text-[11px] text-ink-muted">all traffic</span>}
          {p.expired && <span className="rounded bg-danger-soft px-1.5 text-[11px] text-danger">expired</span>}
        </div>
        <div className="mt-0.5 text-xs text-ink-muted">
          <span className="font-mono">{p.address}</span>
          {" · "}{p.online ? "connected" : "last handshake " + ago(p.lastHandshake)}
          {p.endpoint && <> from <span className="font-mono">{p.endpoint}</span></>}
          {(p.rxBytes > 0 || p.txBytes > 0) && <> · ↓ {bytes(p.rxBytes)} ↑ {bytes(p.txBytes)}</>}
          {p.expiresAt && !p.expired && <> · expires {new Date(p.expiresAt).toLocaleDateString()}</>}
        </div>
      </div>
      {isAdmin && (
        <div className="flex gap-1.5">
          <Button variant="secondary" className="h-7 text-xs" disabled={busy !== null} onClick={onEdit}>Edit</Button>
          <Button variant="secondary" className="h-7 text-xs" disabled={busy !== null} onClick={onRekey}>{busy === "rekey-" + p.id ? "…" : "New key"}</Button>
          <Button variant="danger" className="h-7 text-xs" disabled={busy !== null} onClick={onRemove}>{busy === "rm-" + p.id ? "…" : "Remove"}</Button>
        </div>
      )}
    </li>
  );
}

const EXPIRY = [
  { label: "Never", days: 0 },
  { label: "1 day", days: 1 },
  { label: "1 week", days: 7 },
  { label: "30 days", days: 30 },
  { label: "90 days", days: 90 },
];

function PeerForm({ peer, onClose, onSave }: { peer: VpnPeer | null; onClose: () => void; onSave: (p: VpnPeerInput) => Promise<void> }) {
  const [name, setName] = useState(peer?.name ?? "");
  const [access, setAccess] = useState<VpnAccess>(peer?.access ?? "admin");
  const [ports, setPorts] = useState((peer?.ports?.length ? peer.ports : [80, 443]).join(", "));
  const [full, setFull] = useState(peer?.fullTunnel ?? false);
  const [expiry, setExpiry] = useState(peer?.expiresAt ? "keep" : "0");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    const list = ports.split(/[\s,]+/).filter(Boolean).map(Number);
    if (access === "apps" && list.some((n) => !Number.isInteger(n) || n < 1 || n > 65535)) { setError("Ports are numbers between 1 and 65535, separated by commas."); return; }
    let expiresAt = "";
    if (expiry === "keep") expiresAt = peer?.expiresAt ?? "";
    else if (expiry !== "0") expiresAt = new Date(Date.now() + Number(expiry) * 86400_000).toISOString().replace(/\.\d+Z$/, "Z");
    setSaving(true);
    try { await onSave({ name: name.trim(), access, ports: access === "apps" ? list : [], fullTunnel: access === "internet" || full, expiresAt }); }
    catch (e2) { setError(err(e2)); }
    finally { setSaving(false); }
  };
  const isServer = peer?.access === "server";
  return (
    <Modal title={peer ? `Edit ${peer.name}` : "Add a device"} description={peer ? undefined : "The key is made in this browser when you press Add. You get a QR code to scan with the WireGuard app."} onClose={onClose}>
      <form onSubmit={(e) => void submit(e)} className="space-y-4">
        <Field label="Name"><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="My phone" autoFocus required maxLength={60} /></Field>
        {!isServer && (
          <Field label="Access" hint={ACCESS[access as keyof typeof ACCESS]?.blurb}>
            <Select value={access} onChange={(e) => setAccess(e.target.value as VpnAccess)}>
              {Object.entries(ACCESS).map(([k, a]) => <option key={k} value={k}>{a.label}</option>)}
            </Select>
          </Field>
        )}
        {access === "apps" && <Field label="Ports" hint="TCP and UDP on this server, separated by commas."><Input value={ports} onChange={(e) => setPorts(e.target.value)} className="font-mono" /></Field>}
        {(access === "admin" || access === "apps") && (
          <label className="flex items-start gap-2 text-sm">
            <input type="checkbox" className="mt-1" checked={full} onChange={(e) => setFull(e.target.checked)} />
            <span>Send all of the device's traffic through this server<span className="block text-xs text-ink-muted">Its internet traffic leaves from this server's address. Off, only traffic for this server uses the tunnel.</span></span>
          </label>
        )}
        {!isServer && (
          <Field label="Expires" hint="An expired device is taken out of the tunnel automatically. Changing this later needs no new code.">
            <Select value={expiry} onChange={(e) => setExpiry(e.target.value)}>
              {peer?.expiresAt && <option value="keep">Keep: {new Date(peer.expiresAt).toLocaleString()}</option>}
              {EXPIRY.map((x) => <option key={x.days} value={String(x.days)}>{x.days ? `In ${x.label}` : x.label}</option>)}
            </Select>
          </Field>
        )}
        {peer && (peer.access !== access || peer.fullTunnel !== (access === "internet" || full)) && (
          <Alert tone="warning">Changing between a full and a split tunnel changes what the device routes, so its code has to be scanned again: use New key after saving. Access levels change at once.</Alert>
        )}
        {error && <Alert>{error}</Alert>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="secondary" onClick={onClose}>Cancel</Button>
          <Button type="submit" disabled={saving || !name.trim()}>{saving ? "Working…" : peer ? "Save" : "Add"}</Button>
        </div>
      </form>
    </Modal>
  );
}

function IssuedModal({ cfg, name, onClose }: { cfg: string; name: string; onClose: () => void }) {
  const [qr, setQr] = useState("");
  const [copied, setCopied] = useState(false);
  useEffect(() => { void QRCode.toDataURL(cfg, { margin: 1, width: 260, errorCorrectionLevel: "M" }).then(setQr); }, [cfg]);
  const file = name.replace(/[^A-Za-z0-9_-]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 15) || "islet";
  const download = () => {
    const a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([cfg], { type: "text/plain" }));
    a.download = `${file}.conf`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  };
  return (
    <Modal title={`Connect ${name}`} description="In the WireGuard app: add a tunnel, then scan this code — or import the file on a laptop." onClose={onClose}>
      <div className="space-y-4">
        <Alert tone="warning">Shown once. The private key exists only on this screen: the server never had it. Close this and it is gone; if a device loses it, give the device a new key.</Alert>
        <div className="flex justify-center">{qr ? <img src={qr} alt="WireGuard configuration as a QR code" className="h-64 w-64 rounded bg-white p-1" /> : <div className="h-64 w-64" />}</div>
        <details className="text-sm">
          <summary className="-my-1 cursor-pointer py-1 text-ink-muted">Show the configuration</summary>
          <pre className="mt-2 max-h-56 overflow-auto rounded border border-border bg-surface-2 p-3 font-mono text-[11px] leading-relaxed">{cfg}</pre>
        </details>
        <div className="flex flex-wrap justify-end gap-2">
          <Button type="button" variant="secondary" onClick={() => { void navigator.clipboard?.writeText(cfg).then(() => setCopied(true)); }}>{copied ? "Copied" : "Copy"}</Button>
          <Button type="button" variant="secondary" onClick={download}>Download .conf</Button>
          <Button type="button" onClick={onClose}>Done</Button>
        </div>
      </div>
    </Modal>
  );
}

function EnableCard({ v, isAdmin, busy, onSubmit }: { v: VpnState; isAdmin: boolean; busy: string | null; onSubmit: (b: { endpoint: string; port: number; subnet: string; dns: string }) => void }) {
  const c = v.status.config;
  const [endpoint, setEndpoint] = useState(c.endpoint || v.suggestedEndpoint);
  const [port, setPort] = useState(String(c.port || 51820));
  const [subnet, setSubnet] = useState(c.subnet || "10.66.0.0/24");
  const [dns, setDns] = useState(c.dns || "1.1.1.1");
  return (
    <Card title="Turn on the VPN" description="Opens one UDP port in the firewall. Nothing else changes until you add a device.">
      <form className="space-y-4" onSubmit={(e) => { e.preventDefault(); onSubmit({ endpoint: endpoint.trim(), port: Number(port), subnet: subnet.trim(), dns: dns.trim() }); }}>
        <Field label="Endpoint" hint="What devices connect to: this server's public IP, or a DNS name for it that is not behind a CDN — a CDN proxies web traffic and drops WireGuard's."><Input value={endpoint} onChange={(e) => setEndpoint(e.target.value)} className="font-mono" placeholder="203.0.113.10" required disabled={!isAdmin} /></Field>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <Field label="UDP port"><Input value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" className="font-mono" disabled={!isAdmin} /></Field>
          <Field label="Address range"><Input value={subnet} onChange={(e) => setSubnet(e.target.value)} className="font-mono" disabled={!isAdmin} /></Field>
          <Field label="DNS for full tunnels"><Input value={dns} onChange={(e) => setDns(e.target.value)} className="font-mono" disabled={!isAdmin} /></Field>
        </div>
        {isAdmin && <div className="flex justify-end"><Button type="submit" disabled={busy !== null || !endpoint.trim()}>{busy === "enable" ? "Turning on…" : "Turn on"}</Button></div>}
      </form>
    </Card>
  );
}

type Run = (key: string, fn: () => Promise<unknown>, done?: string) => Promise<void>;
type Ask = ReturnType<typeof useDialog>;

function ImportCard({ found, v, busy, run, ask }: { found: WgEasyFound[]; v: VpnState; busy: string | null; run: Run; ask: Ask }) {
  return (
    <Card title="Move from wg-easy" description="Takes over wg-easy's server key, port and addresses, so every device keeps working without being touched. Client private keys are not copied." className="lg:col-span-2">
      <ul className="divide-y divide-border text-sm">
        {found.filter((w) => w.plan).map((w) => (
          <li key={w.container} className="flex flex-wrap items-center gap-3 py-2.5">
            <div className="min-w-0 flex-1">
              <div className="font-medium">{w.name} <span className="font-mono text-xs text-ink-muted">{w.plan!.subnet}</span></div>
              <div className="text-xs text-ink-muted">{w.plan!.devices.length} device{w.plan!.devices.length === 1 ? "" : "s"}: {w.plan!.devices.join(", ") || "none"}{w.plan!.skipped?.length ? ` · skipped ${w.plan!.skipped.join(", ")}` : ""}</div>
            </div>
            <Button className="h-7 text-xs" disabled={busy !== null} onClick={async () => {
              const ok = await ask.confirm({
                title: `Import ${w.name}?`,
                body: "wg-easy is stopped, because it holds the UDP port, and Islet's VPN starts with its key and devices. Devices come in with internet-only access; raise them afterwards. If the import fails, wg-easy is started again.",
                confirmLabel: "Import", tone: "danger",
              });
              if (ok) await run("import", () => api.vpnImport({ container: w.container, endpoint: v.suggestedEndpoint, port: 51820 }), `Imported ${w.plan!.devices.length} ${w.plan!.devices.length === 1 ? "device" : "devices"} from wg-easy. You can remove the wg-easy app now.`);
            }}>{busy === "import" ? "Importing…" : "Import"}</Button>
          </li>
        ))}
      </ul>
    </Card>
  );
}

function LockdownCard({ v, busy, run, ask }: { v: VpnState; busy: string | null; run: Run; ask: Ask }) {
  const l = v.status.lockdown;
  const [panel, setPanel] = useState(true);
  const [ssh, setSsh] = useState(true);
  const [keep, setKeep] = useState(false);
  const admins = v.status.peers.filter((p) => p.access === "admin" && !p.expired);
  const everConnected = admins.some((p) => p.lastHandshake);
  const active = l.panel || l.ssh;
  return (
    <Card title="Lockdown" description="The panel and SSH answer only inside the VPN. Nobody on the internet can even try a password.">
      {active && !l.pending ? (
        <div className="flex flex-wrap items-center gap-3 text-sm">
          <span className="flex-1">
            <span className="font-medium text-success">On.</span> {[l.panel && "The panel", l.ssh && "SSH"].filter(Boolean).join(" and ")} {l.panel && l.ssh ? "answer" : "answers"} only inside the VPN{l.keepIp ? <>, and from <span className="font-mono">{l.keepIp}</span></> : ""}.
          </span>
          <Button variant="secondary" className="h-8 text-xs" disabled={busy !== null} onClick={async () => {
            const ok = await ask.confirm({ title: "Turn the lockdown off?", body: "The panel and SSH are reachable from the internet again, with the firewall rules they had before.", confirmLabel: "Turn off", tone: "danger" });
            if (ok) await run("lock", () => api.vpnLockdown({ panel: false, ssh: false, keepIp: "" }), "The lockdown is off.");
          }}>Turn off</Button>
        </div>
      ) : active ? (
        <p className="text-sm text-ink-muted">Waiting for confirmation — see above.</p>
      ) : (
        <div className="space-y-3 text-sm">
          {admins.length === 0 && <Alert tone="warning">Add a device with Admin access first, and connect it. The lockdown would otherwise lock everybody out.</Alert>}
          {admins.length > 0 && !everConnected && <Alert tone="warning">None of your admin devices has connected yet. Connect one before turning this on, or it will undo itself.</Alert>}
          <label className="flex items-center gap-2"><input type="checkbox" checked={panel} onChange={(e) => setPanel(e.target.checked)} /> The panel, including any domain it is published on</label>
          <label className="flex items-center gap-2"><input type="checkbox" checked={ssh} onChange={(e) => setSsh(e.target.checked)} /> SSH</label>
          <label className="flex items-start gap-2">
            <input type="checkbox" className="mt-1" checked={keep} onChange={(e) => setKeep(e.target.checked)} />
            <span>Also allow my current address, <span className="font-mono">{v.clientIp}</span><span className="block text-xs text-ink-muted">A way back in if the VPN fails. It reaches the panel at the server's IP and port, not through a domain. Leave it off if your address changes, or for the strictest setting.</span></span>
          </label>
          <p className="text-xs text-ink-muted">After you turn it on, open <span className="font-mono">https://{v.status.address}:{v.panelPort}</span> on a device connected to the VPN and confirm within two minutes. Unconfirmed, it undoes itself.</p>
          <div className="flex justify-end">
            <Button disabled={busy !== null || admins.length === 0 || (!panel && !ssh)} onClick={async () => {
              const ok = await ask.confirm({
                title: "Lock the panel and SSH to the VPN?",
                body: "This browser is cut off right away unless it is on the VPN. Confirm from a connected device within two minutes, or everything goes back as it was.",
                confirmLabel: "Turn on", tone: "danger",
              });
              if (ok) await run("lock", () => api.vpnLockdown({ panel, ssh, keepIp: keep ? v.clientIp : "" }));
            }}>{busy === "lock" ? "Applying…" : "Turn on lockdown"}</Button>
          </div>
        </div>
      )}
    </Card>
  );
}

function PendingLockdown({ v, busy, onConfirm }: { v: VpnState; busy: string | null; onConfirm: () => void }) {
  const l = v.status.lockdown;
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => { const id = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(id); }, []);
  const left = Math.max(0, Math.round((new Date(l.deadline ?? 0).getTime() - now) / 1000));
  return (
    <Alert tone="warning">
      <div className="flex flex-wrap items-center gap-3">
        <span className="flex-1">
          The lockdown is waiting for you to confirm from inside the VPN: <span className="font-mono">{Math.floor(left / 60)}:{String(left % 60).padStart(2, "0")}</span> left.{" "}
          {v.inTunnel ? "You are connected through it." : <>Open <span className="font-mono">https://{v.status.address}:{l.panelPort || v.panelPort}</span> on a device connected to the VPN and confirm there.</>}
        </span>
        {v.inTunnel && <Button className="h-8 text-xs" disabled={busy !== null} onClick={onConfirm}>{busy === "confirm" ? "Confirming…" : "Confirm"}</Button>}
      </div>
    </Alert>
  );
}
