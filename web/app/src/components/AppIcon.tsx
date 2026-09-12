import { BRAND_MARKS } from "@/components/brandIcons";
import { CATEGORY_ICONS, AppsIcon } from "@/components/icons";

/** Words in a recipe or database name that name a technology we have a mark for. */
const KEYWORDS: [RegExp, string][] = [
  // The framework or tool names the thing; the database it happens to use does
  // not, so "Next.js with Postgres" wears the Next.js mark.
  [/next\.?js/i, "nextjs"],
  [/django/i, "django"],
  [/laravel/i, "laravel"],
  [/rails|ruby/i, "rails"],
  [/node/i, "node"],
  [/\bgo\b|golang/i, "go"],
  [/wordpress/i, "wordpress"],
  [/ghost/i, "ghost"],
  [/gitea/i, "gitea"],
  [/n8n/i, "n8n"],
  [/umami/i, "umami"],
  [/plausible/i, "plausible"],
  [/metabase/i, "metabase"],
  [/minio/i, "minio"],
  [/nextcloud/i, "nextcloud"],
  [/vaultwarden|bitwarden/i, "vaultwarden"],
  [/wireguard|wg-/i, "wg-easy"],
  [/tailscale/i, "tailscale"],
  [/cloudflare/i, "cloudflared"],
  [/adminer/i, "adminer"],
  [/grafana/i, "grafana"],
  [/docker|image/i, "docker"],
  [/static|html/i, "html"],
  // Databases last, so they only decide when nothing else did.
  [/postgres|postgis|pgsql/i, "postgres"],
  [/mariadb/i, "mariadb"],
  [/mysql/i, "mysql"],
  [/mongo/i, "mongodb"],
  [/redis/i, "redis"],
];

/** The brand key for a slug, or the first technology its text names. */
export function markKey(slug: string, ...text: string[]): string | undefined {
  if (BRAND_MARKS[slug]) return slug;
  const hay = [slug, ...text].join(" ");
  for (const [re, key] of KEYWORDS) if (re.test(hay)) return key;
  return undefined;
}

export type AppIconSize = "sm" | "md";

/**
 * The square that identifies an app, a recipe or a database: the project's own
 * mark where there is one, otherwise the icon for its category. Monochrome, so
 * a catalog page stays black and white like the rest of the panel.
 */
export default function AppIcon({
  slug,
  category,
  name,
  size = "md",
  className = "",
}: {
  slug: string;
  category?: string;
  name?: string;
  size?: AppIconSize;
  className?: string;
}) {
  const key = markKey(slug, name ?? "", category ?? "");
  const mark = key ? BRAND_MARKS[key] : undefined;
  const Fallback = (category && CATEGORY_ICONS[category]) || AppsIcon;
  const box = size === "sm" ? "h-8 w-8 rounded-md" : "h-10 w-10 rounded-lg";
  const glyph = size === "sm" ? "h-4 w-4" : "h-5 w-5";

  return (
    <span
      className={`flex shrink-0 items-center justify-center border border-border bg-bg text-ink ${box} ${className}`}
      title={mark?.title ?? name ?? slug}
    >
      {mark ? (
        <svg viewBox="0 0 24 24" fill="currentColor" className={glyph} role="img" aria-label={mark.title}>
          <path d={mark.path} />
        </svg>
      ) : (
        <Fallback className={glyph} />
      )}
    </span>
  );
}
