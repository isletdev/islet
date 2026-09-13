export function bytes(n: number, digits = 1): string {
  if (!Number.isFinite(n) || n < 0) return "–";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0, v = n;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(i === 0 ? 0 : digits)} ${units[i]}`;
}

export function rate(n: number): string {
  return `${bytes(n, n >= 1024 * 1024 ? 1 : 0)}/s`;
}

export function pct(n: number): string {
  return `${n.toFixed(n >= 10 ? 0 : 1)}%`;
}

export function duration(s: number): string {
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
  if (d) return `${d}d ${h}h`;
  if (h) return `${h}h ${m}m`;
  return `${m}m ${Math.floor(s % 60)}s`;
}

/**
 * Turn Docker's own size strings — "1.09GB", "530MB", "10.4kB", "0B" — into a
 * number, so a column of them can be sorted.
 *
 * Docker reports decimal units for images and volumes. The exact base does not
 * matter for ordering, only that every row is measured the same way, so this
 * uses the units Docker printed rather than converting to anything.
 */
export function sizeToBytes(s: string): number | null {
  const m = /^\s*([0-9]*\.?[0-9]+)\s*([a-zA-Z]*)/.exec(s ?? "");
  if (!m) return null;
  const n = Number(m[1]);
  if (!Number.isFinite(n)) return null;
  const scale: Record<string, number> = {
    "": 1, b: 1,
    kb: 1e3, k: 1e3, kib: 1024,
    mb: 1e6, m: 1e6, mib: 1024 ** 2,
    gb: 1e9, g: 1e9, gib: 1024 ** 3,
    tb: 1e12, t: 1e12, tib: 1024 ** 4,
  };
  const unit = scale[m[2].toLowerCase()];
  return unit === undefined ? null : n * unit;
}
