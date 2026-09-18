// What the sidebar offers, in the order somebody reaches for it.
//
// It used to be ordered by the phase that built each entry, which is a fact
// about this project's history and not about anybody's day: a file browser, a
// shell and a tmux manager sat above the two things the product exists to do.
// The phase is still recorded because the roadmap refers to it, but it decides
// nothing here.
//
// Two entries are deliberately absent. Terminal is a button in the header, the
// way every editor puts it, because "open a shell" is an action rather than a
// place. The vault is Settings, Secrets: it is configuration touched once per
// secret, not a page anybody visits weekly.
export type NavGroup = "server" | "deploy" | "operate" | "account";

export interface NavItem {
  path: string;
  key: string; // translation key: nav.<key> and nav.<key>.blurb
  label: string;
  group: NavGroup;
  phase: string;
  blurb: string;
  ready?: boolean;
}

/** Group headings, in the order the sidebar draws them. "account" is pinned to
    the foot of the sidebar and shows no heading. */
export const NAV_GROUPS: { id: NavGroup; label: string }[] = [
  { id: "server", label: "Server" },
  { id: "deploy", label: "Deploy" },
  { id: "operate", label: "Operate" },
];

export const NAV: NavItem[] = [
  { path: "/", key: "overview", label: "Overview", group: "server", phase: "v0.1", ready: true, blurb: "Live CPU, memory, disk and network, plus what needs attention." },
  { path: "/files", key: "files", label: "Files", group: "server", phase: "v0.2", ready: true, blurb: "Browse, edit and move files without SSH." },
  { path: "/assistant", key: "assistant", label: "Assistant", group: "server", phase: "v0.12", ready: true, blurb: "Ask for what you want in words. It does what your account can, and nothing more." },
  { path: "/workspaces", key: "workspaces", label: "Workspaces", group: "server", phase: "v0.9", ready: true, blurb: "Sessions that keep running when you close the tab \u2014 for an agent, or anything long." },
  { path: "/containers", key: "containers", label: "Containers", group: "server", phase: "v0.2", ready: true, blurb: "Every container, image, volume and Compose stack on this server." },

  { path: "/apps", key: "apps", label: "Apps", group: "deploy", phase: "v0.3", ready: true, blurb: "One-click apps from the catalog and deploys from Git." },
  { path: "/domains", key: "domains", label: "Domains", group: "deploy", phase: "v0.3", ready: true, blurb: "Point a domain at a container and get HTTPS." },
  { path: "/databases", key: "databases", label: "Databases", group: "deploy", phase: "v0.4", ready: true, blurb: "Postgres, MySQL, Redis and friends with backups." },
  { path: "/runners", key: "runners", label: "Runners", group: "deploy", phase: "v0.5", ready: true, blurb: "GitHub Actions, GitLab and Gitea runners on this server." },
  { path: "/media", key: "media", label: "Media", group: "deploy", phase: "v0.27", ready: true, blurb: "An upload and image API your apps call, storing here or on S3, R2, MinIO." },

  { path: "/cron", key: "cron", label: "Cron", group: "operate", phase: "v0.4", ready: true, blurb: "Scheduled commands and scripts with run history." },
  { path: "/uptime", key: "uptime", label: "Logs & Uptime", group: "operate", phase: "v0.4", ready: true, blurb: "Whether it is up, and the logs that say why not." },
  { path: "/backups", key: "backups", label: "Backups", group: "operate", phase: "v0.6", ready: true, blurb: "Encrypted backups that are tested by restoring them." },
  { path: "/security", key: "security", label: "Security", group: "operate", phase: "v0.6", ready: true, blurb: "Score, firewall, SSH, intrusion prevention, scans." },

  { path: "/notifications", key: "notifications", label: "Notifications", group: "account", phase: "v0.4", ready: true, blurb: "Telegram, Discord, Slack, email and webhooks." },
  { path: "/servers", key: "servers", label: "Servers", group: "account", phase: "v0.3", ready: true, blurb: "Every machine this panel manages, and how to add another." },
  { path: "/settings", key: "settings", label: "Settings", group: "account", phase: "v0.1", ready: true, blurb: "Users, 2FA, API tokens, updates." },
];
