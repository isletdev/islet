import { useEffect, useRef, useState } from "react";
import { RequestError } from "@/lib/api";

/**
 * A queue of uploads, each with its own progress and its own cancel.
 *
 * The Files page used to send every chosen file in one request and say nothing
 * until it answered: a minute of silence for a large file, no way to stop one
 * without stopping all, and one refused name failing the lot. Now each file is
 * its own request, so each has a bar, a cancel button and its own outcome, and
 * a name that is already taken can be replaced without sending the others
 * again.
 *
 * Three run at once. One at a time leaves a fast connection idle between small
 * files; all at once makes every bar crawl together and finishes nothing early.
 */

export type UploadState = "queued" | "sending" | "saving" | "done" | "failed" | "cancelled";

export interface UploadItem {
  key: string;
  name: string;
  size: number;
  /** Where it is going, shown when that is not obvious from the page. */
  dest?: string;
  sent: number;
  state: UploadState;
  error?: string;
  /** The server refused because the name is taken; it can be sent again to replace it. */
  exists?: boolean;
}

export interface UploadJob {
  name: string;
  size: number;
  dest?: string;
  /** Send it. replace is true when the person chose to replace an existing file. */
  run: (onProgress: (fraction: number) => void, signal: AbortSignal, replace: boolean) => Promise<unknown>;
}

const PARALLEL = 3;
let seq = 0;

export function useUploads(onDone?: (job: UploadJob) => void) {
  const [items, setItems] = useState<UploadItem[]>([]);
  // The list lives in a ref and the state is a copy of it. Deciding what to
  // start has to read the latest states and then act, and doing that inside a
  // state updater runs it twice under StrictMode — two requests per file.
  const list = useRef<UploadItem[]>([]);
  const jobs = useRef(new Map<string, { job: UploadJob; ctl?: AbortController; replace: boolean }>());
  const doneRef = useRef(onDone);
  useEffect(() => { doneRef.current = onDone; }, [onDone]);

  const publish = () => setItems([...list.current]);
  const patch = (key: string, p: Partial<UploadItem>) => {
    list.current = list.current.map((x) => (x.key === key ? { ...x, ...p } : x));
    publish();
  };

  const pump = () => {
    let running = list.current.filter((x) => x.state === "sending" || x.state === "saving").length;
    for (const x of list.current) {
      if (running >= PARALLEL) break;
      if (x.state !== "queued") continue;
      running++;
      start(x.key);
    }
  };

  const start = (key: string) => {
    const entry = jobs.current.get(key);
    if (!entry) return;
    const ctl = new AbortController();
    entry.ctl = ctl;
    const { job } = entry;
    patch(key, { state: "sending", sent: 0, error: undefined, exists: undefined });
    job.run((f) => patch(key, f >= 1 ? { sent: job.size, state: "saving" } : { sent: Math.round(f * job.size) }), ctl.signal, entry.replace)
      .then(() => { patch(key, { sent: job.size, state: "done" }); doneRef.current?.(job); })
      .catch((e: unknown) => {
        if (e instanceof RequestError && e.body.error === "aborted") { patch(key, { state: "cancelled" }); return; }
        patch(key, { state: "failed", error: e instanceof Error ? e.message : String(e), exists: e instanceof RequestError && e.status === 409 });
      })
      .finally(() => { entry.ctl = undefined; pump(); });
  };

  const add = (jobsIn: UploadJob[]) => {
    for (const job of jobsIn) {
      const key = `u${++seq}`;
      jobs.current.set(key, { job, replace: false });
      list.current = [...list.current, { key, name: job.name, size: job.size, dest: job.dest, sent: 0, state: "queued" }];
    }
    publish();
    pump();
  };

  /** Stop one that is waiting or sending. */
  const cancel = (key: string) => {
    const entry = jobs.current.get(key);
    if (entry?.ctl) { entry.ctl.abort(); return; }
    patch(key, { state: "cancelled" });
  };

  const cancelAll = () => {
    for (const x of list.current) if (x.state === "queued" || x.state === "sending" || x.state === "saving") cancel(x.key);
  };

  /** Send it again; replace says to overwrite a file of the same name. */
  const retry = (key: string, replace = false) => {
    const entry = jobs.current.get(key);
    if (!entry) return;
    entry.replace = replace;
    patch(key, { state: "queued", sent: 0, error: undefined, exists: undefined });
    pump();
  };

  const dismiss = (key: string) => {
    jobs.current.delete(key);
    list.current = list.current.filter((x) => x.key !== key);
    publish();
  };

  /** Take away everything that has finished, one way or another. */
  const clear = () => {
    list.current = list.current.filter((x) => {
      const live = x.state === "queued" || x.state === "sending" || x.state === "saving";
      if (!live) jobs.current.delete(x.key);
      return live;
    });
    publish();
  };

  // Leaving the page stops what is still going: nothing would be left to show
  // it finishing, or to say it failed.
  useEffect(() => {
    const all = jobs.current;
    return () => { for (const e of all.values()) e.ctl?.abort(); };
  }, []);

  return { items, add, cancel, cancelAll, retry, dismiss, clear };
}
