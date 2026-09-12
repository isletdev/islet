import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { api, RequestError, type FileEntry, type TrashItem } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { bytes } from "@/lib/format";
import { Alert, Button, Input } from "@/components/ui";
import CodeEditor from "@/components/CodeEditor";

const IMAGE = /\.(png|jpe?g|gif|webp|svg|bmp|ico)$/i;
const BINARY = /\.(zip|gz|tgz|tar|bz2|xz|7z|rar|exe|dll|so|bin|iso|img|pdf|mp[34]|mkv|mov|avi|woff2?|ttf|otf|db|sqlite)$/i;

function join(dir: string, name: string) {
  const sep = dir.includes("\\") ? "\\" : "/";
  return dir.endsWith(sep) ? dir + name : dir + sep + name;
}
function parent(p: string) {
  const sep = p.includes("\\") ? "\\" : "/";
  const i = p.lastIndexOf(sep);
  if (i <= 0) return sep === "/" ? "/" : p.slice(0, 3);
  const up = p.slice(0, i);
  return up.length === 2 && up.endsWith(":") ? up + "\\" : up;
}
function crumbs(p: string) {
  const sep = p.includes("\\") ? "\\" : "/";
  const parts = p.split(sep).filter(Boolean);
  const out: { label: string; path: string }[] = [{ label: sep === "/" ? "/" : parts[0] || p, path: sep === "/" ? "/" : parts[0] + "\\" }];
  let acc = out[0].path;
  for (const part of sep === "/" ? parts : parts.slice(1)) { acc = join(acc, part); out.push({ label: part, path: acc }); }
  return out;
}

export default function Files() {
  const { state } = useAuth();
  const isAdmin = state.status === "authed" && state.me.user.role === "admin";
  const [params, setParams] = useSearchParams();
  const path = params.get("path") || "";
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [cur, setCur] = useState(path);
  const [protectedDir, setProtectedDir] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [sel, setSel] = useState<Set<string>>(new Set());
  const [showHidden, setShowHidden] = useState(false);
  const [filter, setFilter] = useState("");
  const [open, setOpen] = useState<{ path: string; content: string; dirty: boolean; tail?: boolean } | null>(null);
  const [preview, setPreview] = useState<string | null>(null);
  const [trash, setTrash] = useState<TrashItem[] | null>(null);
  const [search, setSearch] = useState<{ q: string; content: boolean; hits: { path: string; isDir: boolean; line?: number; text?: string }[] } | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const upload = useRef<HTMLInputElement>(null);

  const load = useCallback((p: string) => api.filesList(p).then((r) => { setEntries(r.entries); setCur(r.path); setProtectedDir(r.protected); setErr(null); setSel(new Set()); }).catch((e) => setErr(e instanceof RequestError ? e.message : String(e))), []);
  useEffect(() => { void load(path); }, [path, load]);
  const go = (p: string) => { setOpen(null); setPreview(null); setTrash(null); setSearch(null); setParams({ path: p }); };

  const shown = useMemo(() => entries.filter((e) => (showHidden || !e.name.startsWith(".")) && (!filter || e.name.toLowerCase().includes(filter.toLowerCase()))), [entries, showHidden, filter]);

  const fail = (e: unknown) => setMsg(e instanceof RequestError ? e.message : String(e instanceof Error ? e.message : e));
  const openEntry = async (e: FileEntry) => {
    if (e.isDir) { go(e.path); return; }
    if (IMAGE.test(e.name)) { setPreview(e.path); setOpen(null); return; }
    if (BINARY.test(e.name)) { window.open(`/api/v1/files/download?path=${encodeURIComponent(e.path)}`); return; }
    try {
      if (e.size > 5 * 1024 * 1024) { const t = await api.filesTail(e.path); setOpen({ path: e.path, content: t.content, dirty: false, tail: true }); }
      else { const r = await api.filesRead(e.path); setOpen({ path: e.path, content: r.content, dirty: false }); }
      setPreview(null);
    } catch (er) { fail(er); }
  };
  const save = async () => {
    if (!open || open.tail) return;
    try { await api.filesWrite(open.path, open.content); setOpen({ ...open, dirty: false }); setMsg("Saved."); void load(cur); } catch (er) { fail(er); }
  };
  const op = async (body: Record<string, unknown>, then?: () => void) => {
    try { await api.filesOp(body); then?.(); void load(cur); } catch (er) { fail(er); }
  };
  const confirmProtected = (paths: string[]) => {
    const prot = paths.filter((p) => entries.find((e) => e.path === p)?.protected);
    if (prot.length === 0) return true;
    return prompt(`These are protected system paths:\n${prot.join("\n")}\n\nType DELETE to continue.`) === "DELETE";
  };
  const del = (paths: string[], permanent = false) => {
    if (paths.length === 0) return;
    if (!confirmProtected(paths)) return;
    if (permanent && !confirm(`Permanently delete ${paths.length} item(s)? This cannot be undone.`)) return;
    void op({ op: "delete", paths, permanent }, () => setMsg(permanent ? "Deleted." : `Moved ${paths.length} item(s) to trash.`));
  };
  const rename = (e: FileEntry) => { const n = prompt("New name", e.name); if (n && n !== e.name) void op({ op: "rename", path: e.path, to: join(cur, n) }); };
  const moveTo = (paths: string[]) => { const d = prompt("Move to directory", cur); if (d) for (const p of paths) void op({ op: "move", path: p, to: join(d, p.split(/[\\/]/).pop()!) }); };
  const copyTo = (e: FileEntry) => { const d = prompt("Copy to path", join(cur, "copy-of-" + e.name)); if (d) void op({ op: "copy", path: e.path, to: d }); };
  const chmod = (e: FileEntry) => { const m = prompt(`Mode for ${e.name} (octal)`, e.mode); if (m && m !== e.mode) void op({ op: "chmod", path: e.path, mode: m, recursive: e.isDir && confirm("Apply recursively to everything inside?") }); };
  const mkdir = () => { const n = prompt("Folder name"); if (n) void op({ op: "mkdir", path: join(cur, n) }); };
  const touch = () => { const n = prompt("File name"); if (n) void op({ op: "touch", path: join(cur, n) }, () => void openEntry({ name: n, path: join(cur, n), isDir: false, size: 0 } as FileEntry)); };
  const archive = (paths: string[]) => { const t = prompt("Archive name (.zip or .tar.gz)", join(cur, "archive.zip")); if (t) void op({ op: "archive", paths, to: t }); };
  const extract = (e: FileEntry) => { const d = prompt("Extract into", cur); if (d) void op({ op: "extract", path: e.path, to: d }); };
  const doUpload = async (files: FileList | null) => {
    if (!files || files.length === 0) return;
    const fd = new FormData(); for (const f of Array.from(files)) fd.append("file", f);
    try { const r = await fetch(`/api/v1/files/upload?path=${encodeURIComponent(cur)}`, { method: "POST", body: fd, credentials: "same-origin" }); if (!r.ok) throw new Error(((await r.json()) as { message: string }).message); setMsg(`Uploaded ${files.length} file(s).`); void load(cur); } catch (er) { fail(er); }
  };
  const runSearch = async (e: FormEvent) => {
    e.preventDefault(); if (!search) return;
    try { const hits = await api.filesSearch(cur, search.q, search.content); setSearch({ ...search, hits }); } catch (er) { fail(er); }
  };
  const loadTrash = () => api.trash().then(setTrash).catch(fail);

  const selected = Array.from(sel);
  const toggleAll = () => setSel(sel.size === shown.length ? new Set() : new Set(shown.map((e) => e.path)));

  return (
    <div className="mx-auto flex h-full max-w-7xl flex-col">
      <div className="flex flex-wrap items-center gap-2">
        <nav className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto text-sm" aria-label="Path">
          {crumbs(cur).map((c, i, arr) => (
            <span key={c.path} className="flex items-center gap-1">
              <button type="button" onClick={() => go(c.path)} className={`rounded-sm px-1 hover:bg-surface-2 ${i === arr.length - 1 ? "font-semibold text-ink" : "text-ink-muted"}`}>{c.label}</button>
              {i < arr.length - 1 && <span className="text-ink-faint">/</span>}
            </span>
          ))}
        </nav>
        <Input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter" className="h-8 w-40 text-xs" />
        <label className="flex items-center gap-1 text-xs text-ink-muted"><input type="checkbox" checked={showHidden} onChange={(e) => setShowHidden(e.target.checked)} />Hidden</label>
        <Button variant="secondary" className="h-8 px-2.5 text-xs" onClick={() => { setSearch({ q: "", content: false, hits: [] }); setTrash(null); }}>Search</Button>
        {isAdmin && <>
          <Button variant="secondary" className="h-8 px-2.5 text-xs" onClick={mkdir}>New folder</Button>
          <Button variant="secondary" className="h-8 px-2.5 text-xs" onClick={touch}>New file</Button>
          <Button variant="secondary" className="h-8 px-2.5 text-xs" onClick={() => upload.current?.click()}>Upload</Button>
          <input ref={upload} type="file" multiple hidden onChange={(e) => void doUpload(e.target.files)} />
          <Button variant="secondary" className="h-8 px-2.5 text-xs" onClick={() => { void loadTrash(); setSearch(null); }}>Trash</Button>
        </>}
        <Button variant="secondary" className="h-8 px-2.5 text-xs" onClick={() => void load(cur)}>Refresh</Button>
      </div>
      {protectedDir && <div className="mt-2"><Alert tone="warning">This is a protected system location. Changes here can break the server; deletions ask for typed confirmation.</Alert></div>}
      {(err || msg) && <div className="mt-2">{err ? <Alert>{err}</Alert> : <p className="text-sm text-ink-muted">{msg}</p>}</div>}

      {selected.length > 0 && isAdmin && (
        <div className="mt-2 flex items-center gap-2 rounded-md border border-border bg-surface px-3 py-1.5 text-xs">
          <span className="font-medium">{selected.length} selected</span>
          <Button variant="secondary" className="h-7 px-2 text-xs" onClick={() => moveTo(selected)}>Move to…</Button>
          <Button variant="secondary" className="h-7 px-2 text-xs" onClick={() => archive(selected)}>Archive</Button>
          <Button variant="secondary" className="h-7 px-2 text-xs" onClick={() => del(selected)}>Trash</Button>
          <Button variant="danger" className="h-7 px-2 text-xs" onClick={() => del(selected, true)}>Delete permanently</Button>
        </div>
      )}

      <div className="mt-3 grid min-h-0 flex-1 gap-4" style={{ gridTemplateColumns: (open || preview || trash || search) && window.innerWidth >= 768 ? "minmax(0,1fr) minmax(0,1.3fr)" : "1fr" }}>
        <div className="min-h-0 overflow-auto rounded-lg border border-border bg-surface">
          <table className="w-full min-w-[480px] text-sm">
            <thead className="sticky top-0 bg-surface text-left text-xs text-ink-muted">
              <tr>{isAdmin && <th className="w-8 px-3 py-2"><input type="checkbox" checked={shown.length > 0 && sel.size === shown.length} onChange={toggleAll} aria-label="Select all" /></th>}<th className="py-2 font-medium">Name</th><th className="py-2 text-right font-medium">Size</th><th className="py-2 pl-4 font-medium">Mode</th><th className="py-2 pl-4 font-medium">Owner</th><th className="py-2 pl-4 font-medium">Modified</th><th className="py-2 pr-3"></th></tr>
            </thead>
            <tbody className="divide-y divide-border">
              {cur !== "/" && !/^[A-Za-z]:\\$/.test(cur) && <tr><td colSpan={7} className="px-3 py-1.5"><button type="button" onClick={() => go(parent(cur))} className="text-ink-muted hover:text-ink">..</button></td></tr>}
              {shown.map((e) => (
                <tr key={e.path} className={`group hover:bg-surface-2 ${sel.has(e.path) ? "bg-surface-2" : ""}`}>
                  {isAdmin && <td className="px-3 py-1.5"><input type="checkbox" checked={sel.has(e.path)} onChange={() => setSel((s) => { const n = new Set(s); if (n.has(e.path)) n.delete(e.path); else n.add(e.path); return n; })} aria-label={`Select ${e.name}`} /></td>}
                  <td className="py-1.5"><button type="button" onClick={() => void openEntry(e)} className={`text-left hover:underline ${e.isDir ? "font-medium" : ""}`}>{e.isDir ? "▸ " : ""}{e.name}{e.isSymlink && <span className="ml-1 text-xs text-ink-faint">→ {e.target}</span>}</button>{e.protected && <span className="ml-2 rounded-sm bg-warning-soft px-1 text-[10px] text-warning">protected</span>}</td>
                  <td className="py-1.5 text-right font-mono text-xs tabular-nums text-ink-muted">{e.isDir ? "" : bytes(e.size)}</td>
                  <td className="py-1.5 pl-4 font-mono text-xs text-ink-muted"><button type="button" onClick={() => isAdmin && chmod(e)} title={e.perms} className={isAdmin ? "hover:text-ink" : ""}>{e.mode}</button></td>
                  <td className="py-1.5 pl-4 text-xs text-ink-muted">{e.owner}{e.group && `:${e.group}`}</td>
                  <td className="whitespace-nowrap py-1.5 pl-4 text-xs text-ink-muted">{e.modTime ? new Date(e.modTime).toLocaleString() : ""}</td>
                  <td className="py-1.5 pr-3 text-right whitespace-nowrap opacity-0 group-hover:opacity-100">
                    <a href={`/api/v1/files/download?path=${encodeURIComponent(e.path)}`} className="text-xs text-ink-muted hover:text-ink">Download</a>
                    {isAdmin && <>
                      <button type="button" onClick={() => rename(e)} className="ml-2 text-xs text-ink-muted hover:text-ink">Rename</button>
                      <button type="button" onClick={() => copyTo(e)} className="ml-2 text-xs text-ink-muted hover:text-ink">Copy</button>
                      {/\.(zip|tar|tgz|tar\.gz)$/i.test(e.name) && <button type="button" onClick={() => extract(e)} className="ml-2 text-xs text-ink-muted hover:text-ink">Extract</button>}
                      <button type="button" onClick={() => del([e.path])} className="ml-2 text-xs text-danger hover:underline">Trash</button>
                    </>}
                  </td>
                </tr>
              ))}
              {shown.length === 0 && <tr><td colSpan={7} className="px-3 py-6 text-center text-ink-muted">Empty folder.</td></tr>}
            </tbody>
          </table>
        </div>

        {open && (
          <div className="flex min-h-0 flex-col">
            <div className="mb-2 flex items-center justify-between gap-2 text-sm">
              <span className="truncate font-mono text-xs">{open.path}{open.dirty && " •"}{open.tail && <span className="ml-2 text-ink-muted">(last 64 KB of a large file, read-only)</span>}</span>
              <div className="flex gap-2">{!open.tail && isAdmin && <Button className="h-7 px-2.5 text-xs" onClick={() => void save()} disabled={!open.dirty}>Save</Button>}<Button variant="secondary" className="h-7 px-2.5 text-xs" onClick={() => { if (!open.dirty || confirm("Discard unsaved changes?")) setOpen(null); }}>Close</Button></div>
            </div>
            {open.tail ? <pre className="min-h-0 flex-1 overflow-auto rounded-lg border border-border bg-code-bg p-3 font-mono text-xs text-code-fg">{open.content}</pre>
              : <CodeEditor name={open.path.split(/[\\/]/).pop() || ""} value={open.content} onChange={(v) => setOpen((o) => o && { ...o, content: v, dirty: true })} onSave={() => void save()} className="min-h-0 flex-1" />}
          </div>
        )}
        {preview && (
          <div className="flex min-h-0 flex-col">
            <div className="mb-2 flex items-center justify-between text-sm"><span className="truncate font-mono text-xs">{preview}</span><Button variant="secondary" className="h-7 px-2.5 text-xs" onClick={() => setPreview(null)}>Close</Button></div>
            <div className="min-h-0 flex-1 overflow-auto rounded-lg border border-border bg-surface p-3"><img src={`/api/v1/files/download?path=${encodeURIComponent(preview)}&inline=1`} alt="" className="max-w-full" /></div>
          </div>
        )}
        {trash && (
          <div className="flex min-h-0 flex-col rounded-lg border border-border bg-surface">
            <div className="flex items-center justify-between border-b border-border px-4 py-2 text-sm"><span className="font-semibold">Trash</span><div className="flex gap-2"><Button variant="danger" className="h-7 px-2 text-xs" onClick={() => { if (confirm("Empty the trash permanently?")) api.trashOp({ op: "purge", id: "" }).then(loadTrash).catch(fail); }}>Empty</Button><Button variant="secondary" className="h-7 px-2 text-xs" onClick={() => setTrash(null)}>Close</Button></div></div>
            <ul className="min-h-0 flex-1 divide-y divide-border overflow-auto text-sm">
              {trash.map((t) => <li key={t.id} className="flex items-center justify-between gap-2 px-4 py-2"><div className="min-w-0"><div className="truncate font-medium">{t.name}</div><div className="truncate font-mono text-xs text-ink-muted">{t.original} · {new Date(t.deletedAt).toLocaleString()}</div></div><div className="flex gap-1"><Button variant="secondary" className="h-7 px-2 text-xs" onClick={() => api.trashOp({ op: "restore", id: t.id }).then(() => { void loadTrash(); void load(cur); }).catch(fail)}>Restore</Button><Button variant="danger" className="h-7 px-2 text-xs" onClick={() => api.trashOp({ op: "purge", id: t.id }).then(loadTrash).catch(fail)}>Delete</Button></div></li>)}
              {trash.length === 0 && <li className="px-4 py-6 text-center text-ink-muted">Trash is empty. Items are kept for 7 days.</li>}
            </ul>
          </div>
        )}
        {search && (
          <div className="flex min-h-0 flex-col rounded-lg border border-border bg-surface">
            <form onSubmit={runSearch} className="flex items-center gap-2 border-b border-border px-4 py-2"><Input value={search.q} onChange={(e) => setSearch({ ...search, q: e.target.value })} placeholder={`Search in ${cur}`} className="h-8 text-xs" autoFocus /><label className="flex items-center gap-1 whitespace-nowrap text-xs text-ink-muted"><input type="checkbox" checked={search.content} onChange={(e) => setSearch({ ...search, content: e.target.checked })} />contents</label><Button type="submit" className="h-8 px-2.5 text-xs">Go</Button><Button type="button" variant="secondary" className="h-8 px-2.5 text-xs" onClick={() => setSearch(null)}>Close</Button></form>
            <ul className="min-h-0 flex-1 divide-y divide-border overflow-auto text-sm">
              {search.hits.map((h, i) => <li key={i} className="px-4 py-1.5"><button type="button" onClick={() => h.isDir ? go(h.path) : void openEntry({ name: h.path.split(/[\\/]/).pop()!, path: h.path, isDir: false, size: 0 } as FileEntry)} className="font-mono text-xs hover:underline">{h.path}{h.line ? `:${h.line}` : ""}</button>{h.text && <div className="truncate font-mono text-[11px] text-ink-muted">{h.text}</div>}</li>)}
              {search.hits.length === 0 && <li className="px-4 py-6 text-center text-ink-muted">No results yet.</li>}
            </ul>
          </div>
        )}
      </div>
    </div>
  );
}
