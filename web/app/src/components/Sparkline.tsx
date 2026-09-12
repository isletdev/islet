import { useId, useMemo, useState } from "react";

export interface SparkPoint { ts: number; value: number }

interface Props {
  points: SparkPoint[];
  max?: number;              // fixed ceiling (e.g. 100 for percent); otherwise data max
  format: (v: number) => string;
  height?: number;
  className?: string;
}

/** Single-series line with area fill, 2px stroke, and a crosshair tooltip on hover. */
export default function Sparkline({ points, max, format, height = 56, className = "" }: Props) {
  const id = useId();
  const [hover, setHover] = useState<number | null>(null);
  const W = 400, H = height, PAD = 2;

  const geo = useMemo(() => {
    if (points.length < 2) return null;
    const t0 = points[0].ts, t1 = points[points.length - 1].ts;
    const vmax = Math.max(max ?? 0, ...points.map((p) => p.value), 1e-9);
    const x = (ts: number) => PAD + ((ts - t0) / Math.max(t1 - t0, 1)) * (W - 2 * PAD);
    const y = (v: number) => H - PAD - (v / vmax) * (H - 2 * PAD);
    const d = points.map((p, i) => `${i ? "L" : "M"}${x(p.ts).toFixed(1)} ${y(p.value).toFixed(1)}`).join(" ");
    const area = `${d} L${x(t1).toFixed(1)} ${H - PAD} L${x(t0).toFixed(1)} ${H - PAD} Z`;
    return { d, area, x, y };
  }, [points, max, H]);

  if (!geo) {
    return <div className={`flex items-center text-xs text-ink-faint ${className}`} style={{ height }}>Collecting…</div>;
  }
  const hp = hover === null ? null : points[hover];

  return (
    <div className={`relative ${className}`}>
      <svg
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        className="block w-full"
        style={{ height }}
        onMouseMove={(e) => {
          const r = e.currentTarget.getBoundingClientRect();
          const fx = (e.clientX - r.left) / r.width;
          const idx = Math.round(fx * (points.length - 1));
          setHover(Math.max(0, Math.min(points.length - 1, idx)));
        }}
        onMouseLeave={() => setHover(null)}
        role="img"
        aria-label="Trend"
      >
        <defs>
          <linearGradient id={`${id}-g`} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stopColor="currentColor" stopOpacity="0.09" />
            <stop offset="1" stopColor="currentColor" stopOpacity="0" />
          </linearGradient>
        </defs>
        <path d={geo.area} fill={`url(#${id}-g)`} />
        <path d={geo.d} fill="none" stroke="currentColor" strokeWidth="1.75" vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
        {hp && (
          <>
            <line x1={geo.x(hp.ts)} x2={geo.x(hp.ts)} y1={0} y2={H} stroke="currentColor" strokeOpacity="0.35" strokeWidth="1" vectorEffect="non-scaling-stroke" />
            <circle cx={geo.x(hp.ts)} cy={geo.y(hp.value)} r="3.5" fill="currentColor" stroke="var(--islet-surface)" strokeWidth="2" vectorEffect="non-scaling-stroke" />
          </>
        )}
      </svg>
      {hp && (
        <div className="pointer-events-none absolute -top-7 right-0 rounded-sm border border-border bg-surface px-1.5 py-0.5 font-mono text-[11px] text-ink shadow-float">
          {format(hp.value)} · {new Date(hp.ts * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
        </div>
      )}
    </div>
  );
}
