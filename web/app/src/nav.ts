// The sidebar is the roadmap. Each entry names the phase that delivers it.
export interface NavItem {
  path: string;
  key: string; // translation key: nav.<key> and nav.<key>.blurb
  label: string;
  phase: string;
  blurb: string;
  ready?: boolean;
}

export const NAV: NavItem[] = [
  { path: "/", key: "overview", label: "Overview", phase: "v0.1", ready: true, blurb: "Live CPU, memory, disk and network, plus what needs attention." },
  { path: "/containers", key: "containers", label: "Containers", phase: "v0.2", ready: true, blurb: "Every container, image, volume and Compose stack on this server." },
  { path: "/files", key: "files", label: "Files", phase: "v0.2", ready: true, blurb: "Browse, edit and move files without SSH." },
  { path: "/terminal", key: "terminal", label: "Terminal", phase: "v0.1", ready: true, blurb: "A shell on this server, in the browser." },
  { path: "/domains", key: "domains", label: "Domains", phase: "v0.3", ready: true, blurb: "Point a domain at a container and get HTTPS." },
  { path: "/apps", key: "apps", label: "Apps", phase: "v0.3", ready: true, blurb: "One-click apps from the catalog and deploys from Git." },
  { path: "/databases", key: "databases", label: "Databases", phase: "v0.4", ready: true, blurb: "Postgres, MySQL, Redis and friends with backups." },
  { path: "/cron", key: "cron", label: "Cron", phase: "v0.4", ready: true, blurb: "Scheduled commands and scripts with run history." },
  { path: "/notifications", key: "notifications", label: "Notifications", phase: "v0.4", ready: true, blurb: "Telegram, Discord, Slack, email and webhooks." },
  { path: "/runners", key: "runners", label: "Runners", phase: "v0.5", ready: true, blurb: "GitHub Actions, GitLab and Gitea runners on this server." },
  { path: "/uptime", key: "uptime", label: "Uptime", phase: "v0.4", ready: true, blurb: "HTTP, TCP and keyword checks from this server." },
  { path: "/backups", key: "backups", label: "Backups", phase: "v0.6", ready: true, blurb: "Encrypted backups that are tested by restoring them." },
  { path: "/security", key: "security", label: "Security", phase: "v0.6", ready: true, blurb: "Score, firewall, SSH, intrusion prevention, scans." },
  { path: "/logs", key: "logs", label: "Logs", phase: "v0.4", ready: true, blurb: "One viewer across system, Docker and app logs." },
  { path: "/settings", key: "settings", label: "Settings", phase: "v0.1", ready: true, blurb: "Users, 2FA, API tokens, updates." },
];
