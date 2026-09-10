// The sidebar is the roadmap. Each entry names the phase that delivers it.
export interface NavItem {
  path: string;
  label: string;
  phase: string;
  blurb: string;
  ready?: boolean;
}

export const NAV: NavItem[] = [
  { path: "/", label: "Overview", phase: "v0.1", ready: true, blurb: "Live CPU, memory, disk and network, plus what needs attention." },
  { path: "/containers", label: "Containers", phase: "v0.2", ready: true, blurb: "Every container, image, volume and Compose stack on this server." },
  { path: "/files", label: "Files", phase: "v0.2", ready: true, blurb: "Browse, edit and move files without SSH." },
  { path: "/terminal", label: "Terminal", phase: "v0.1", ready: true, blurb: "A shell on this server, in the browser." },
  { path: "/domains", label: "Domains", phase: "v0.3", blurb: "Point a domain at a container and get HTTPS." },
  { path: "/apps", label: "Apps", phase: "v0.3", blurb: "One-click apps from the catalog and deploys from Git." },
  { path: "/databases", label: "Databases", phase: "v0.4", blurb: "Postgres, MySQL, Redis and friends with backups." },
  { path: "/cron", label: "Cron", phase: "v0.4", blurb: "Scheduled commands and scripts with run history." },
  { path: "/notifications", label: "Notifications", phase: "v0.4", blurb: "Telegram, Discord, Slack, email and webhooks." },
  { path: "/backups", label: "Backups", phase: "v0.6", blurb: "Encrypted backups that are tested by restoring them." },
  { path: "/security", label: "Security", phase: "v0.6", blurb: "Score, firewall, SSH, intrusion prevention, scans." },
  { path: "/logs", label: "Logs", phase: "v0.4", blurb: "One viewer across system, Docker and app logs." },
  { path: "/settings", label: "Settings", phase: "v0.1", ready: true, blurb: "Users, 2FA, API tokens, updates." },
];
