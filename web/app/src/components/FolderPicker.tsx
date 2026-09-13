import { useCallback, useEffect, useState } from "react";
import { api, RequestError, type FileEntry } from "@/lib/api";
import { Button, Input } from "@/components/ui";
import { FolderIcon, NewFolderIcon } from "@/components/icons";

/**
 * Pick a destination folder by walking the tree.
 *
 * Copying and moving used to ask for a path in a text box, which meant knowing
 * the answer before you started. This shows the same folders the file list
 * does, one level at a time, and hands back the one you are standing in.
 */

function sep(p: string) {
  return p.includes("\\") ? "\\" : "/";
}

function join(dir: string, name: string) {
  const s = sep(dir);
  return dir.endsWith(s) ? dir + name : dir + s + name;
}

function parent(p: string) {
  const s = sep(p);
  const i = p.lastIndexOf(s);
  if (i <= 0) return s === "/" ? "/" : p.slice(0, 3);
  const up = p.slice(0, i);
  return up.length === 2 && up.endsWith(":") ? up + "\\" : up;
}

export default function FolderPicker({
  title,
  action,
  start,
  moving,
  onPick,
  onCancel,
}: {
  title: string;
  action: string;
  start: string;
  /** What is being copied or moved, so it can be greyed out as a target. */
  moving?: string[];
  onPick: (dir: string) => void;
  onCancel: () => void;
}) {
  const [dir, setDir] = useState(start);
  const [rows, setRows] = useState<FileEntry[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [newName, setNewName] = useState("");
  const [adding, setAdding] = useState(false);

  const load = useCallback((p: string) => {
    setRows(null);
    setErr(null);
    api.filesList(p)
      .then((r) => { setRows(r.entries.filter((e) => e.isDir)); setDir(r.path); })
      .catch((e) => setErr(e instanceof RequestError ? e.message : String(e)));
  }, []);

  useEffect(() => { load(start); }, [load, start]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onCancel(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onCancel]);

  const create = async () => {
    const name = newName.trim();
    if (!name) return;
    try {
      await api.filesOp({ op: "mkdir", path: join(dir, name) });
      setNewName("");
      setAdding(false);
      load(dir);
    } catch (e) {
      setErr(e instanceof RequestError ? e.message : String(e));
    }
  };

  const atRoot = dir === "/" || /^[A-Za-z]:\\$/.test(dir);

  return (
    <div
      className="fixed inset-0 z-[60] flex items-start justify-center bg-black/40 p-4 pt-[10vh]"
      onMouseDown={(e) => { if (e.target === e.currentTarget) onCancel(); }}
      role="dialog"
      aria-modal="true"
      aria-label={title}
    >
      <div className="flex max-h-[70vh] w-full max-w-lg flex-col overflow-hidden rounded-lg border border-border bg-surface shadow-float">
        <div className="border-b border-border px-5 py-4">
          <h2 className="font-semibold">{title}</h2>
          <p className="mt-1 truncate font-mono text-xs text-ink-muted" title={dir}>{dir}</p>
        </div>

        <div className="min-h-0 flex-1 overflow-auto">
          {!atRoot && (
            <button type="button" onClick={() => load(parent(dir))} className="flex w-full items-center gap-2.5 px-5 py-2 text-left text-sm text-ink-muted hover:bg-surface-2 hover:text-ink">
              <FolderIcon className="h-4 w-4 shrink-0" />
              ..
            </button>
          )}
          {rows?.map((e) => {
            const blocked = moving?.includes(e.path);
            return (
              <button
                key={e.path}
                type="button"
                disabled={blocked}
                onClick={() => load(e.path)}
                title={blocked ? "This is one of the items being moved" : e.path}
                className={`flex w-full items-center gap-2.5 px-5 py-2 text-left text-sm ${blocked ? "cursor-not-allowed text-ink-faint" : "hover:bg-surface-2"}`}
              >
                <FolderIcon className="h-4 w-4 shrink-0 text-ink-muted" />
                <span className="truncate">{e.name}</span>
              </button>
            );
          })}
          {rows?.length === 0 && <p className="px-5 py-6 text-center text-sm text-ink-muted">No folders in here. You can still {action.toLowerCase()} into it.</p>}
          {rows === null && !err && <p className="px-5 py-6 text-center text-sm text-ink-muted">Reading…</p>}
          {err && <p className="px-5 py-6 text-center text-sm text-danger">{err}</p>}
        </div>

        <div className="border-t border-border px-5 py-3">
          {adding ? (
            <form onSubmit={(e) => { e.preventDefault(); void create(); }} className="flex items-center gap-2">
              <Input value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="Folder name" className="h-8 text-xs" autoFocus />
              <Button type="submit" className="h-8 px-2.5 text-xs">Create</Button>
              <Button type="button" variant="secondary" className="h-8 px-2.5 text-xs" onClick={() => { setAdding(false); setNewName(""); }}>Cancel</Button>
            </form>
          ) : (
            <div className="flex items-center justify-between gap-2">
              <Button variant="secondary" className="h-8 gap-2 px-2.5 text-xs" onClick={() => setAdding(true)}>
                <NewFolderIcon className="h-4 w-4" />
                New folder
              </Button>
              <div className="flex gap-2">
                <Button variant="secondary" onClick={onCancel}>Cancel</Button>
                <Button onClick={() => onPick(dir)}>{action} here</Button>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
