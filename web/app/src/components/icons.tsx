import type { ReactNode } from "react";

/** One stroked icon family, drawn on a 24x24 grid so weights match across the app.
    No icon dependency: the panel ships what it draws. */
function Icon({ children, className = "h-[18px] w-[18px]" }: { children: ReactNode; className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      {children}
    </svg>
  );
}

export type IconProps = { className?: string };

/* ---- navigation ---- */

export const OverviewIcon = (p: IconProps) => (
  <Icon {...p}>
    <rect x="3" y="3" width="7.4" height="8.6" rx="1.4" />
    <rect x="13.6" y="3" width="7.4" height="5" rx="1.4" />
    <rect x="13.6" y="10.4" width="7.4" height="10.6" rx="1.4" />
    <rect x="3" y="14" width="7.4" height="7" rx="1.4" />
  </Icon>
);

export const ContainersIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M12 2.9 20.4 7.4v9.2L12 21.1 3.6 16.6V7.4z" />
    <path d="M3.8 7.5 12 12l8.2-4.5" />
    <path d="M12 12v9.1" />
  </Icon>
);

export const FilesIcon = (p: IconProps) => (
  <Icon {...p}>
    {/* The folder body ends at y=19.5 against a tab starting at 5.4: half a
        unit low, which is small and visible next to a row of icons that are
        not. */}
    <g transform="translate(0 -0.5)">
      <path d="M3 7.4c0-1.1.9-2 2-2h3.5c.63 0 1.22.29 1.6.79l1 1.31H19c1.1 0 2 .9 2 2v8.1c0 1.1-.9 2-2 2H5c-1.1 0-2-.9-2-2z" />
    </g>
  </Icon>
);

export const TerminalIcon = (p: IconProps) => (
  <Icon {...p}>
    <rect x="2.6" y="4" width="18.8" height="16" rx="2.2" />
    <path d="M7 9.6 10 12l-3 2.4" />
    <path d="M12.6 15h4.6" />
  </Icon>
);

export const DomainsIcon = (p: IconProps) => (
  <Icon {...p}>
    <circle cx="12" cy="12" r="9" />
    <path d="M3.3 9.4h17.4M3.3 14.6h17.4" />
    <path d="M12 3c2.25 2.5 3.4 5.6 3.4 9s-1.15 6.5-3.4 9c-2.25-2.5-3.4-5.6-3.4-9S9.75 5.5 12 3Z" />
  </Icon>
);

export const AppsIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M12 2.9 3.2 7.3 12 11.7l8.8-4.4z" />
    <path d="m3.2 12.1 8.8 4.4 8.8-4.4" />
    <path d="m3.2 16.7 8.8 4.4 8.8-4.4" />
  </Icon>
);

export const DatabasesIcon = (p: IconProps) => (
  <Icon {...p}>
    <ellipse cx="12" cy="6" rx="7.6" ry="3.2" />
    <path d="M4.4 6v12c0 1.77 3.4 3.2 7.6 3.2s7.6-1.43 7.6-3.2V6" />
    <path d="M4.4 12c0 1.77 3.4 3.2 7.6 3.2s7.6-1.43 7.6-3.2" />
  </Icon>
);

export const CronIcon = (p: IconProps) => (
  <Icon {...p}>
    <circle cx="12" cy="12" r="9" />
    <path d="M12 6.8v5.4l3.4 2" />
  </Icon>
);

export const NotificationsIcon = (p: IconProps) => (
  <Icon {...p}>
    <g transform="translate(0 0.45)">
      <path d="M18 9a6 6 0 1 0-12 0c0 4.9-2.1 6.3-2.1 6.3h16.2S18 13.9 18 9Z" />
      <path d="M13.8 19a2 2 0 0 1-3.6 0" />
    </g>
  </Icon>
);

export const RunnersIcon = (p: IconProps) => (
  <Icon {...p}>
    <circle cx="12" cy="12" r="9" />
    <path d="M10.2 8.5 15.7 12l-5.5 3.5z" />
  </Icon>
);

export const UptimeIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M2.6 12.2h4L9 5.2l4.6 13.6 2.4-6.6h5.4" />
  </Icon>
);

export const BackupsIcon = (p: IconProps) => (
  <Icon {...p}>
    <rect x="2.6" y="3.6" width="18.8" height="4.8" rx="1.6" />
    <path d="M4.4 8.4v10.2a2 2 0 0 0 2 2h11.2a2 2 0 0 0 2-2V8.4" />
    <path d="M9.8 12.6h4.4" />
  </Icon>
);

export const SecurityIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M12 2.8 20 6v6c0 4.6-3.2 8.2-8 9.2C7.2 20.2 4 16.6 4 12V6z" />
    <path d="m8.9 12.1 2.2 2.2 4.1-4.3" />
  </Icon>
);

export const LogsIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M14 2.9H7a2 2 0 0 0-2 2v14.2a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7.9z" />
    <path d="M14 2.9V8h5" />
    <path d="M8.5 13h7M8.5 16.6h4.4" />
  </Icon>
);

export const SettingsIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M4 6.5h8.2M18.4 6.5H20M4 12h2.2M12.4 12H20M4 17.5h7.2M17.4 17.5H20" />
    <circle cx="15.3" cy="6.5" r="2.2" />
    <circle cx="9.3" cy="12" r="2.2" />
    <circle cx="14.3" cy="17.5" r="2.2" />
  </Icon>
);

/* ---- interface ---- */

export const MenuIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M3.5 7h17M3.5 12h17M3.5 17h17" />
  </Icon>
);

export const CloseIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="m6.6 6.6 10.8 10.8M17.4 6.6 6.6 17.4" />
  </Icon>
);

export const SunIcon = (p: IconProps) => (
  <Icon {...p}>
    <circle cx="12" cy="12" r="4.1" />
    <path d="M12 2.6v2.2M12 19.2v2.2M4.35 4.35l1.55 1.55M18.1 18.1l1.55 1.55M2.6 12h2.2M19.2 12h2.2M4.35 19.65 5.9 18.1M18.1 5.9l1.55-1.55" />
  </Icon>
);

export const MoonIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M20.4 14.3A8.7 8.7 0 0 1 9.7 3.6a8.8 8.8 0 1 0 10.7 10.7Z" />
  </Icon>
);

export const UserIcon = (p: IconProps) => (
  <Icon {...p}>
    <circle cx="12" cy="8.3" r="3.7" />
    <path d="M4.9 20.1a7.3 7.3 0 0 1 14.2 0" />
  </Icon>
);

export const ChevronDownIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="m6.8 9.7 5.2 5.2 5.2-5.2" />
  </Icon>
);

export const SignOutIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M9.6 3.6H6a2 2 0 0 0-2 2v12.8a2 2 0 0 0 2 2h3.6" />
    <path d="M15.4 7.9 19.9 12l-4.5 4.1" />
    <path d="M19.9 12H9.2" />
  </Icon>
);

export const ShieldAlertIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M12 2.8 20 6v6c0 4.6-3.2 8.2-8 9.2C7.2 20.2 4 16.6 4 12V6z" />
    <path d="M12 8.4v4.2" />
    <path d="M12 15.9h.01" />
  </Icon>
);

export const ExternalIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M13.8 4.4h5.8v5.8" />
    <path d="M19.6 4.4 11 13" />
    <path d="M18 14.2v4.4a2 2 0 0 1-2 2H5.4a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4.4" />
  </Icon>
);

export const SearchIcon = (p: IconProps) => (
  <Icon {...p}>
    <circle cx="10.8" cy="10.8" r="6.6" />
    <path d="m15.6 15.6 4.3 4.3" />
  </Icon>
);

/* ---- files ---- */

export const FolderIcon = FilesIcon;

export const FileIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M13.8 2.9H7a2 2 0 0 0-2 2v14.2a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8.1z" />
    <path d="M13.8 2.9V8.2h5" />
  </Icon>
);

export const ImageFileIcon = (p: IconProps) => (
  <Icon {...p}>
    <rect x="3" y="4.4" width="18" height="15.2" rx="2" />
    <circle cx="8.6" cy="9.6" r="1.6" />
    <path d="m3.4 17.4 4.8-4.6 3.4 3.2 3.6-3.8 5.4 5.2" />
  </Icon>
);

export const ArchiveFileIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M13.8 2.9H7a2 2 0 0 0-2 2v14.2a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8.1z" />
    <path d="M13.8 2.9V8.2h5" />
    <path d="M10 6h1.4M10 9h1.4M10 12h1.4M10 15h1.4" />
  </Icon>
);

export const DownloadIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M12 3.6v10.8" />
    <path d="m7.8 10.4 4.2 4.2 4.2-4.2" />
    <path d="M4.4 19.4h15.2" />
  </Icon>
);

export const CopyIcon = (p: IconProps) => (
  <Icon {...p}>
    <rect x="8.6" y="8.6" width="11.4" height="11.4" rx="2" />
    <path d="M15.4 5.4a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v7.4a2 2 0 0 0 2 2" />
  </Icon>
);

export const MoveIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M12 3.4v17.2M3.4 12h17.2" />
    <path d="m9 6.4 3-3 3 3M9 17.6l3 3 3-3M6.4 9l-3 3 3 3M17.6 9l3 3-3 3" />
  </Icon>
);

export const RenameIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M4 20.2h16" />
    <path d="M16.4 3.6a2.1 2.1 0 0 1 3 3L9.6 16.4l-4 1 1-4z" />
  </Icon>
);

export const TrashIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M3.8 6.4h16.4" />
    <path d="M9 6.4V4.8a1.6 1.6 0 0 1 1.6-1.6h2.8A1.6 1.6 0 0 1 15 4.8v1.6" />
    <path d="M6.2 6.4v13a2 2 0 0 0 2 2h7.6a2 2 0 0 0 2-2v-13" />
    <path d="M10.2 10.6v6.4M13.8 10.6v6.4" />
  </Icon>
);

export const ExtractIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M12 20.4V9.6" />
    <path d="m7.8 13.8 4.2-4.2 4.2 4.2" />
    <path d="M4.4 4.6h15.2" />
  </Icon>
);

export const LinkIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M10.2 13.8a4 4 0 0 0 5.7 0l3-3a4 4 0 1 0-5.7-5.7l-1.3 1.3" />
    <path d="M13.8 10.2a4 4 0 0 0-5.7 0l-3 3a4 4 0 1 0 5.7 5.7l1.3-1.3" />
  </Icon>
);

export const UploadIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M12 20.4V9.6" />
    <path d="m7.8 13.8 4.2-4.2 4.2 4.2" />
    <path d="M4.4 4.6h15.2" />
  </Icon>
);

export const NewFolderIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M3 7.4c0-1.1.9-2 2-2h3.5c.63 0 1.22.29 1.6.79l1 1.31H19c1.1 0 2 .9 2 2v8.1c0 1.1-.9 2-2 2H5c-1.1 0-2-.9-2-2z" />
    <path d="M12 11.6v5M9.5 14.1h5" />
  </Icon>
);

export const PlusIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M12 5v14M5 12h14" />
  </Icon>
);

export const PlayIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M8 5.5v13l11-6.5-11-6.5Z" />
  </Icon>
);

export const StopIcon = (p: IconProps) => (
  <Icon {...p}>
    <rect x="6.5" y="6.5" width="11" height="11" rx="1.5" />
  </Icon>
);

export const RefreshIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M20.4 12a8.4 8.4 0 1 1-2.5-6" />
    <path d="M20.4 4.2V10h-5.8" />
  </Icon>
);

export const SearchFileIcon = SearchIcon;

/* ---- catalog categories ---- */

export const CodeIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="m8.6 8.4-4.2 3.6 4.2 3.6" />
    <path d="m15.4 8.4 4.2 3.6-4.2 3.6" />
    <path d="m13.4 5.4-2.8 13.2" />
  </Icon>
);

export const CmsIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M12.8 3.4H6.4a2 2 0 0 0-2 2v13.2a2 2 0 0 0 2 2h11.2a2 2 0 0 0 2-2v-6.4" />
    <path d="M8.4 8.6h5.2M8.4 12.4h3" />
    <path d="M17.6 3.2 21 6.6l-5.6 5.6h-3.4V8.8z" />
  </Icon>
);

export const ChartIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M4 20.2h16.4" />
    <path d="M7 20.2v-6.4M12 20.2V6.6M17 20.2v-9.6" />
  </Icon>
);

export const StorageIcon = (p: IconProps) => (
  <Icon {...p}>
    <rect x="3" y="4.2" width="18" height="6.2" rx="1.8" />
    <rect x="3" y="13.4" width="18" height="6.2" rx="1.8" />
    <path d="M7 7.3h.01M7 16.5h.01" />
  </Icon>
);

export const AutomationIcon = (p: IconProps) => (
  <Icon {...p}>
    <circle cx="5.6" cy="6.4" r="2.6" />
    <circle cx="18.4" cy="12" r="2.6" />
    <circle cx="5.6" cy="17.6" r="2.6" />
    <path d="M8.2 6.4h4a2 2 0 0 1 2 2v1.2M8.2 17.6h4a2 2 0 0 0 2-2v-1.2" />
  </Icon>
);

/** Catalog category to icon, for anything without a brand mark. */
export const CATEGORY_ICONS: Record<string, (p: IconProps) => ReactNode> = {
  database: DatabasesIcon,
  developer: CodeIcon,
  cms: CmsIcon,
  analytics: ChartIcon,
  monitoring: UptimeIcon,
  storage: StorageIcon,
  network: DomainsIcon,
  networking: DomainsIcon,
  automation: AutomationIcon,
  security: SecurityIcon,
  // recipe categories
  "web app": DomainsIcon,
  api: CodeIcon,
  operations: UptimeIcon,
};

/** Nav key to icon, so nav.ts stays plain data. */
export const ServersIcon = (p: IconProps) => (
  <Icon {...p}>
    <rect x="3" y="3.6" width="18" height="7" rx="1.8" />
    <rect x="3" y="13.4" width="18" height="7" rx="1.8" />
    <path d="M6.7 7.1h.01M6.7 16.9h.01" />
    <path d="M16.4 7.1h1.9M16.4 16.9h1.9" />
  </Icon>
);

export const SqlIcon = (p: IconProps) => (
  <Icon {...p}>
    <ellipse cx="12" cy="5.6" rx="7.5" ry="2.8" />
    <path d="M4.5 5.6v5.2c0 1.55 3.36 2.8 7.5 2.8s7.5-1.25 7.5-2.8V5.6" />
    <path d="M4.5 10.8v5.2c0 1.55 3.36 2.8 7.5 2.8" />
    <path d="M15.4 16.6h4.1M17.45 14.5v4.2" />
  </Icon>
);

export const TableIcon = (p: IconProps) => (
  <Icon {...p}>
    <rect x="3" y="4.5" width="18" height="15" rx="1.8" />
    <path d="M3 9.4h18M3 14.5h18M9.2 9.4V19.5" />
  </Icon>
);

/** A window with a second one behind it: a session that is still there. */
export const WorkspacesIcon = (p: IconProps) => (
  // Same two corrections: the shared stroke weight, and down 1.5 — the back
  // window reaches y=3 while nothing goes below y=18, so it sat high.
  <Icon {...p}>
    <g transform="translate(0 1.5)">
      <rect x="2" y="6" width="14" height="12" rx="2" />
      <path d="M8 3h12a2 2 0 0 1 2 2v10" />
      <path d="m5.5 11 2 2-2 2" />
      <path d="M10 15h3" />
    </g>
  </Icon>
);

/** A speech mark with a spark: something you ask, that acts. */
export const AssistantIcon = (p: IconProps) => (
  // Through the shared Icon, so the stroke weight matches its neighbours, and
  // lifted: the bubble's tail hangs to y=22 against a body starting at y=4, so
  // the drawn shape's middle was 14.2 on a grid whose middle is 12 — nearly two
  // pixels below every label it sits beside.
  <Icon {...p}>
    <g transform="translate(0 -2.2)">
      <path d="M21 12a8 8 0 0 1-8 8H8l-4 2 1-4.2A8 8 0 1 1 21 12Z" />
      <path d="m12 8 1 2.5 2.5 1-2.5 1L12 15l-1-2.5-2.5-1 2.5-1Z" />
    </g>
  </Icon>
);

/** A padlock over a slab: a thing kept shut. */
export const VaultIcon = (p: IconProps) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" {...p}>
    <rect x="3" y="10" width="18" height="11" rx="2" />
    <path d="M8 10V7a4 4 0 0 1 8 0v3" />
    <circle cx="12" cy="15.5" r="1.25" />
  </svg>
);

export const NAV_ICONS: Record<string, (p: IconProps) => ReactNode> = {
  assistant: AssistantIcon,
  vault: VaultIcon,
  workspaces: WorkspacesIcon,
  overview: OverviewIcon,
  containers: ContainersIcon,
  files: FilesIcon,
  terminal: TerminalIcon,
  domains: DomainsIcon,
  apps: AppsIcon,
  databases: DatabasesIcon,
  cron: CronIcon,
  notifications: NotificationsIcon,
  runners: RunnersIcon,
  uptime: UptimeIcon,
  backups: BackupsIcon,
  security: SecurityIcon,
  logs: LogsIcon,
  servers: ServersIcon,
  settings: SettingsIcon,
};
