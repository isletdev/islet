import type { NavItem } from "@/nav";
import { t } from "@/lib/i18n";

export default function Placeholder({ item }: { item: NavItem }) {
  return (
    <div className="mx-auto max-w-5xl">
      <h1 className="text-xl font-semibold tracking-[-0.02em]">{t("nav." + item.key)}</h1>
      <p className="mt-1 text-ink-muted">{t("nav." + item.key + ".blurb")}</p>
      <div className="mt-6 rounded-lg border border-dashed border-border-strong p-8 text-center">
        <p className="font-medium">Planned for {item.phase}</p>
        <p className="mt-1 text-ink-muted">
          The roadmap in the repository lists exactly what this screen will do.
        </p>
      </div>
    </div>
  );
}
