import { bytes } from "@/lib/format";
import type { UploadItem } from "@/lib/uploads";
import { CheckIcon, CloseIcon, FileIcon, UploadIcon } from "@/components/icons";

/**
 * What is uploading, one row and one bar per file.
 *
 * The header carries the totals — how many are done, how many bytes have gone
 * — because with twenty files the question is "how long", and no single row
 * answers it. Each row's X stops that file and only that file; once a row has
 * finished, the same X takes it off the list.
 */
export default function UploadList({ items, onCancel, onCancelAll, onRetry, onDismiss, onClear }: {
  items: UploadItem[];
  onCancel: (key: string) => void;
  onCancelAll: () => void;
  onRetry: (key: string, replace: boolean) => void;
  onDismiss: (key: string) => void;
  onClear: () => void;
}) {
  if (items.length === 0) return null;
  const live = items.filter((x) => x.state === "queued" || x.state === "sending" || x.state === "saving");
  const done = items.filter((x) => x.state === "done").length;
  const failed = items.filter((x) => x.state === "failed").length;
  const total = items.reduce((n, x) => n + (x.state === "cancelled" ? 0 : x.size), 0);
  const sent = items.reduce((n, x) => n + (x.state === "cancelled" || x.state === "failed" ? 0 : x.sent), 0);
  const counted = items.filter((x) => x.state !== "cancelled").length;
  const title = live.length > 0
    ? `Uploading ${counted === 1 ? "1 file" : `${done} of ${counted} files`}`
    : failed > 0 ? `${done} uploaded, ${failed} failed` : `${done === 1 ? "1 file" : `${done} files`} uploaded`;

  return (
    <section className="rounded-lg border border-border bg-surface" aria-label="Uploads">
      <header className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-border px-3 py-2">
        <UploadIcon className="h-4 w-4 shrink-0 text-ink-muted" />
        <span className="text-sm font-medium">{title}</span>
        {live.length > 0 && total > 0 && (
          <span className="font-mono text-xs tabular-nums text-ink-muted">{bytes(sent)} of {bytes(total)}</span>
        )}
        <span className="ml-auto flex gap-3 text-xs">
          {live.length > 1 && <button type="button" onClick={onCancelAll} className="-my-1 py-1 text-ink-muted hover:text-danger">Cancel all</button>}
          {live.length < items.length && <button type="button" onClick={onClear} className="-my-1 py-1 text-ink-muted hover:text-ink">Clear finished</button>}
        </span>
      </header>
      <ul className="max-h-72 divide-y divide-border overflow-y-auto" role="list">
        {items.map((x) => <Row key={x.key} item={x} onCancel={onCancel} onRetry={onRetry} onDismiss={onDismiss} />)}
      </ul>
    </section>
  );
}

function Row({ item: x, onCancel, onRetry, onDismiss }: {
  item: UploadItem;
  onCancel: (key: string) => void;
  onRetry: (key: string, replace: boolean) => void;
  onDismiss: (key: string) => void;
}) {
  const active = x.state === "queued" || x.state === "sending" || x.state === "saving";
  const pct = x.size > 0 ? Math.min(100, (x.sent / x.size) * 100) : x.state === "done" ? 100 : 0;
  const status =
    x.state === "queued" ? "Waiting"
    : x.state === "sending" ? `${Math.floor(pct)}% · ${bytes(x.sent)} of ${bytes(x.size)}`
    : x.state === "saving" ? "Saving…"
    : x.state === "done" ? bytes(x.size)
    : x.state === "cancelled" ? "Cancelled"
    : x.exists ? "A file with this name already exists" : (x.error ?? "Failed");
  const bar =
    x.state === "done" ? "bg-success"
    : x.state === "failed" ? "bg-danger"
    : x.state === "cancelled" ? "bg-ink-faint"
    : "bg-accent";
  const label = active ? `Cancel uploading ${x.name}` : `Remove ${x.name} from the list`;

  return (
    <li className="px-3 py-2">
      <div className="flex items-center gap-2">
        {x.state === "done"
          ? <CheckIcon className="h-4 w-4 shrink-0 text-success" />
          : <FileIcon className="h-4 w-4 shrink-0 text-ink-faint" />}
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm" title={x.dest ? `${x.dest}/${x.name}` : x.name}>{x.name}</span>
          <span className={`block truncate text-xs tabular-nums ${x.state === "failed" ? "text-danger" : "text-ink-muted"}`}>{status}</span>
        </span>
        {x.state === "failed" && (
          <button type="button" onClick={() => onRetry(x.key, !!x.exists)} className="-my-1 shrink-0 py-1 text-xs font-medium text-accent hover:underline">
            {x.exists ? "Replace" : "Retry"}
          </button>
        )}
        {x.state === "cancelled" && (
          <button type="button" onClick={() => onRetry(x.key, false)} className="-my-1 shrink-0 py-1 text-xs font-medium text-accent hover:underline">Retry</button>
        )}
        <button
          type="button"
          onClick={() => (active ? onCancel(x.key) : onDismiss(x.key))}
          aria-label={label}
          title={active ? "Cancel" : "Remove"}
          className="-m-1 shrink-0 rounded-sm p-1.5 text-ink-muted hover:bg-surface-2 hover:text-danger"
        >
          <CloseIcon className="h-4 w-4" />
        </button>
      </div>
      {/* Every row has a bar, finished or not, so a list of twenty reads as
          twenty bars at a glance; a row that stopped keeps how far it got. */}
      <div
        className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-surface-2"
        role="progressbar"
        aria-label={`${x.name} upload`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(pct)}
      >
        <div
          className={`h-full rounded-full transition-[width] duration-200 ${bar} ${x.state === "saving" ? "animate-pulse" : ""}`}
          style={{ width: `${x.state === "queued" ? 0 : Math.max(x.state === "sending" ? 2 : 0, pct)}%` }}
        />
      </div>
    </li>
  );
}
