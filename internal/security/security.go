// Package security computes the Security Score, applies one-click fixes
// (firewall, fail2ban, unattended upgrades, SSH hardening with a rollback
// timer), scans images with Trivy and offers the panic button.
package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/internal/store"
)

// Check is one line of the Security Score.
type Check struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Detail  string `json:"detail"`
	Weight  int    `json:"weight"`
	Status  string `json:"status"` // pass | fail | warn | unknown
	Fix     string `json:"fix,omitempty"`
	FixNote string `json:"fixNote,omitempty"`
}

// Report is the score with its checks.
type Report struct {
	Score      int     `json:"score"`
	Max        int     `json:"max"`
	Checks     []Check `json:"checks"`
	Linux      bool    `json:"linux"`
	ComputedAt string  `json:"computedAt"`
}

// FirewallRule is one allowed port.
type FirewallRule struct {
	Port  string `json:"port"`
	Proto string `json:"proto"`
	From  string `json:"from"`
	// Routed marks a rule on the forward chain, which is the only kind that
	// reaches a port published by a container.
	Routed  bool   `json:"routed"`
	Comment string `json:"comment"`
}

// Firewall is the ufw state.
type Firewall struct {
	Installed bool           `json:"installed"`
	Active    bool           `json:"active"`
	Rules     []FirewallRule `json:"rules"`
	DockerOK  bool           `json:"dockerAware"`
	// MissingRoutes names proxy ports that have no forward rule, which is the
	// state where every site behind the proxy answers nothing.
	MissingRoutes []string `json:"missingRoutes"`
}

// Finding is a Trivy vulnerability.
type Finding struct {
	ID       string `json:"id"`
	Package  string `json:"package"`
	Version  string `json:"version"`
	Fixed    string `json:"fixed"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
}

// Scan is the stored result of a Trivy run.
type Scan struct {
	Target    string    `json:"target"`
	At        string    `json:"at"`
	Critical  int       `json:"critical"`
	High      int       `json:"high"`
	Medium    int       `json:"medium"`
	Low       int       `json:"low"`
	Findings  []Finding `json:"findings"`
	Error     string    `json:"error,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
}

// Service is the security facade.
type Service struct {
	st      *store.Store
	run     *cmdrun.Runner
	bus     *notify.Bus
	log     *slog.Logger
	dataDir string

	mu          sync.Mutex
	rollback    *time.Timer
	prevSSHD    []byte
	sshdPath    string
	scans       map[string]Scan
	hasPlan     func(context.Context) bool
	has2FA      func(context.Context) bool
	panelCert   func() bool
	proxyPorts  func() (string, string)
	panelPort   func() string
	panelRouted func(context.Context) bool
	proxySubnet func(context.Context) string
}

// Hooks lets other packages answer questions the score needs.
type Hooks struct {
	HasBackupPlan func(context.Context) bool
	Admin2FA      func(context.Context) bool
	PanelHasCert  func() bool
	// ProxyPorts reports the published HTTP and HTTPS ports of the proxy, so
	// firewall rules match what is really listening rather than a guess.
	ProxyPorts func() (string, string)
	// PanelPort is the port the daemon itself listens on.
	PanelPort func() string
	// PanelRouted says whether a domain reaches the panel through the proxy.
	// When it does, the panel port does not have to be open to the internet.
	PanelRouted func(context.Context) bool
	// ProxySubnet is the address range of the proxy network. The proxy dials
	// the daemon for forward auth and the panel route, so that range needs the
	// panel port even when the internet does not.
	ProxySubnet func(context.Context) string
}

// New builds the service.
func New(st *store.Store, run *cmdrun.Runner, bus *notify.Bus, dataDir string, h Hooks, log *slog.Logger) *Service {
	abs, _ := filepath.Abs(dataDir)
	s := &Service{st: st, run: run, bus: bus, log: log, dataDir: abs, sshdPath: "/etc/ssh/sshd_config.d/00-islet.conf", scans: map[string]Scan{}, hasPlan: h.HasBackupPlan, has2FA: h.Admin2FA, panelCert: h.PanelHasCert, proxyPorts: h.ProxyPorts, panelPort: h.PanelPort, panelRouted: h.PanelRouted, proxySubnet: h.ProxySubnet}
	if b, err := os.ReadFile(filepath.Join(abs, "scans.json")); err == nil {
		_ = json.Unmarshal(b, &s.scans)
	}
	return s
}

func (s *Service) sh(ctx context.Context, actor string, name string, args ...string) (string, error) {
	res, err := s.run.Run(ctx, actor, name, args...)
	if err != nil {
		var ce *cmdrun.Error
		if errors.As(err, &ce) {
			return res.Stdout, errors.New(strings.TrimSpace(ce.Result.Stderr + " " + ce.Result.Stdout))
		}
		return "", err
	}
	return res.Stdout, nil
}

func has(bin string) bool { _, err := exec.LookPath(bin); return err == nil }

func unitActive(ctx context.Context, s *Service, unit string) bool {
	out, _ := s.sh(ctx, "system", "systemctl", "is-active", unit)
	return strings.TrimSpace(out) == "active"
}

// ---- score ----

// Report computes the checks. Non-Linux hosts get the panel-only checks.
func (s *Service) Report(ctx context.Context) Report {
	linux := runtime.GOOS == "linux"
	var checks []Check
	add := func(c Check) { checks = append(checks, c) }

	// Panel checks work everywhere.
	if s.has2FA != nil {
		st := "fail"
		if s.has2FA(ctx) {
			st = "pass"
		}
		add(Check{ID: "panel-2fa", Title: "Two-factor on every admin account", Detail: "A stolen password alone should not hand over the server.", Weight: 10, Status: st, FixNote: "Settings → Two-factor authentication"})
	}
	if s.panelCert != nil {
		st := "warn"
		if s.panelCert() {
			st = "pass"
		}
		add(Check{ID: "panel-cert", Title: "Panel served with a trusted certificate", Detail: "Route a domain to the panel with Let's Encrypt instead of the self-signed certificate.", Weight: 4, Status: st, FixNote: "Domains → add a domain with target Panel"})
	}
	if s.hasPlan != nil {
		st := "fail"
		if s.hasPlan(ctx) {
			st = "pass"
		}
		add(Check{ID: "backups", Title: "A backup plan exists", Detail: "Security includes getting the data back.", Weight: 8, Status: st, FixNote: "Backups → New plan"})
	}
	// Publicly exposed databases (Docker port bindings on all interfaces).
	if out, err := s.sh(ctx, "system", "docker", "ps", "--format", "{{.Names}}\t{{.Ports}}"); err == nil {
		var exposed []string
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			name, ports, _ := strings.Cut(l, "\t")
			for _, p := range PublishedDBPorts(ports) {
				exposed = append(exposed, name+" port "+p)
			}
		}
		st, detail := "pass", "No database ports are published on public interfaces."
		if len(exposed) > 0 {
			st, detail = "fail", "Reachable on every interface: "+strings.Join(exposed, ", ")+". Prefer an SSH tunnel or an IP allowlist."
		}
		add(Check{ID: "db-exposed", Title: "No databases reachable from the internet", Detail: detail, Weight: 10, Status: st, FixNote: "Republish the port on 127.0.0.1, or add the Docker firewall rules below"})
	}
	if out, err := s.sh(ctx, "system", "docker", "ps", "--format", "{{.Names}}\t{{.Mounts}}"); err == nil {
		var sock []string
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			name, mounts, _ := strings.Cut(l, "\t")
			if strings.Contains(mounts, "docker.sock") && name != "islet-proxy" && !strings.HasPrefix(name, "islet-runner-") {
				sock = append(sock, name)
			}
		}
		st, detail := "pass", "Only Islet's proxy and runners you allowed can talk to Docker."
		if len(sock) > 0 {
			st, detail = "warn", "Containers with the Docker socket mounted (root on the host): "+strings.Join(sock, ", ")
		}
		add(Check{ID: "docker-sock", Title: "Docker socket not handed to random containers", Detail: detail, Weight: 5, Status: st})
	}

	if linux {
		// SSH
		cfg := s.readSSHD(ctx)
		st := "fail"
		if !cfg.PermitRoot {
			st = "pass"
		}
		add(Check{ID: "ssh-root", Title: "Root cannot log in over SSH with a password", Detail: "Attackers try root first. Log in as a sudo user, or root with a key only.", Weight: 10, Status: st, Fix: "ssh-harden", FixNote: "Turns off root password login and password authentication (keeps keys)"})
		st = "fail"
		if !cfg.PasswordAuth {
			st = "pass"
		}
		add(Check{ID: "ssh-password", Title: "SSH password authentication off", Detail: "Keys only. Brute force becomes pointless.", Weight: 10, Status: st, Fix: "ssh-harden"})
		st = "warn"
		if cfg.Port != 22 {
			st = "pass"
		}
		add(Check{ID: "ssh-port", Title: "SSH on a non-default port", Detail: "Not security by itself, but it cuts log noise by 95%. Optional.", Weight: 2, Status: st, FixNote: "Security → SSH settings"})
		// Firewall
		fw := s.FirewallStatus(ctx)
		st = "fail"
		if fw.Active {
			st = "pass"
		}
		add(Check{ID: "firewall", Title: "Firewall enabled with only the needed ports", Detail: "ufw allowing SSH and the proxy ports and nothing else, on both the input and the forward chain so published container ports are covered. The panel port is reached from your own address, not the internet.", Weight: 12, Status: st, Fix: "firewall", FixNote: "Opens SSH and the proxy ports; the panel from your address only"})
		if fw.Active {
			st = "warn"
			if fw.DockerOK {
				st = "pass"
			}
			add(Check{ID: "firewall-docker", Title: "Docker cannot bypass the firewall", Detail: "Without the DOCKER-USER rules, any published port is open regardless of ufw.", Weight: 5, Status: st, Fix: "firewall"})
		}
		if fw.Active && fw.DockerOK {
			// A port published by a container arrives on the forward chain.
			// "ufw allow 443/tcp" only writes an input rule, so without a
			// forward rule every site behind the proxy goes dark while the
			// host itself keeps answering. This check names that state.
			st = "pass"
			detail := "Ports the proxy publishes need a ufw route rule, not just an allow rule. Without one, every domain stops answering while the server still responds on its own ports."
			if len(fw.MissingRoutes) > 0 {
				st = "fail"
				detail = "No forward rule for port " + strings.Join(fw.MissingRoutes, ", ") + ". Every domain behind the proxy is unreachable right now, although the server itself still answers."
			}
			add(Check{ID: "firewall-routes", Title: "Proxy ports reach the proxy", Detail: detail, Weight: 10, Status: st, Fix: "firewall-routes", FixNote: "Adds the missing route rules"})
		}
		// fail2ban
		st = "fail"
		if unitActive(ctx, s, "fail2ban") {
			st = "pass"
		}
		add(Check{ID: "fail2ban", Title: "Brute-force protection (fail2ban)", Detail: "Bans IPs after repeated failed SSH logins.", Weight: 8, Status: st, Fix: "fail2ban", FixNote: "Installs fail2ban with an sshd jail"})
		// Unattended upgrades
		st = "fail"
		if _, err := os.Stat("/etc/apt/apt.conf.d/20auto-upgrades"); err == nil && unitActive(ctx, s, "unattended-upgrades") {
			st = "pass"
		} else if !has("apt-get") {
			st = "unknown"
		}
		add(Check{ID: "auto-updates", Title: "Security updates install automatically", Detail: "unattended-upgrades applies security patches nightly.", Weight: 8, Status: st, Fix: "auto-updates"})
		// Pending updates
		if has("apt-get") {
			out, _ := s.sh(ctx, "system", "sh", "-c", "apt-get -s upgrade 2>/dev/null | grep -c '^Inst.*security' || true")
			n, _ := strconv.Atoi(strings.TrimSpace(out))
			st, detail := "pass", "No pending security updates."
			if n > 0 {
				st, detail = "warn", fmt.Sprintf("%d security updates are pending.", n)
			}
			add(Check{ID: "pending-updates", Title: "No pending security updates", Detail: detail, Weight: 4, Status: st, Fix: "apt-upgrade", FixNote: "Runs apt-get upgrade for security packages"})
		}
		st, detail := "pass", "No reboot pending."
		if _, err := os.Stat("/var/run/reboot-required"); err == nil {
			st, detail = "warn", "A kernel or core library update is waiting for a reboot."
		}
		add(Check{ID: "reboot", Title: "No reboot pending", Detail: detail, Weight: 2, Status: st})
		if has("canonical-livepatch") {
			st, detail := "warn", "Livepatch is installed but not enabled."
			if out, _ := s.sh(ctx, "system", "canonical-livepatch", "status"); strings.Contains(out, "running: true") || strings.Contains(out, "state: applied") || strings.Contains(out, "checkState: checked") {
				st, detail = "pass", "Kernel livepatch is active; kernel fixes apply without a reboot."
			}
			add(Check{ID: "livepatch", Title: "Kernel livepatch", Detail: detail, Weight: 1, Status: st})
		}
		if v, _, _ := s.st.Setting(ctx, "security.lynis_score"); v != "" {
			st := "warn"
			n, _ := strconv.Atoi(v)
			if n >= 70 {
				st = "pass"
			}
			add(Check{ID: "lynis", Title: "Lynis hardening index", Detail: "Last audit scored " + v + "/100.", Weight: 3, Status: st, FixNote: "Security → Run Lynis audit"})
		}
		// Sudo user
		st = "fail"
		if out, err := os.ReadFile("/etc/group"); err == nil {
			for _, l := range strings.Split(string(out), "\n") {
				if (strings.HasPrefix(l, "sudo:") || strings.HasPrefix(l, "wheel:")) && strings.TrimSpace(l[strings.LastIndex(l, ":")+1:]) != "" {
					st = "pass"
				}
			}
		}
		add(Check{ID: "sudo-user", Title: "A non-root sudo user exists", Detail: "Day-to-day logins should not be root.", Weight: 4, Status: st, FixNote: "Terminal: adduser NAME && usermod -aG sudo NAME, then copy your key"})
		// Swap and time
		st = "warn"
		if out, err := os.ReadFile("/proc/swaps"); err == nil && len(strings.Split(strings.TrimSpace(string(out)), "\n")) > 1 {
			st = "pass"
		}
		add(Check{ID: "swap", Title: "Swap configured", Detail: "A small swap file keeps the OOM killer from taking out the database during a spike.", Weight: 2, Status: st, Fix: "swap", FixNote: "Creates a 2 GB swap file"})
		st = "warn"
		if out, _ := s.sh(ctx, "system", "timedatectl", "show", "-p", "NTPSynchronized", "--value"); strings.TrimSpace(out) == "yes" {
			st = "pass"
		}
		add(Check{ID: "ntp", Title: "Clock synchronised", Detail: "TOTP codes and certificates depend on correct time.", Weight: 2, Status: st, Fix: "ntp", FixNote: "Enables systemd-timesyncd"})
	}

	score, max := 0, 0
	for _, c := range checks {
		if c.Status == "unknown" {
			continue
		}
		max += c.Weight
		if c.Status == "pass" {
			score += c.Weight
		}
	}
	pct := 0
	if max > 0 {
		pct = score * 100 / max
	}
	return Report{Score: pct, Max: 100, Checks: checks, Linux: linux, ComputedAt: time.Now().UTC().Format(time.RFC3339)}
}

// ---- fixes ----

// FixAll runs the safe fixes in order and reports each result. SSH
// hardening is left out on purpose: it needs the admin's key in place.
func (s *Service) FixAll(ctx context.Context, actor, clientIP string) []map[string]string {
	var out []map[string]string
	for _, id := range []string{"auto-updates", "fail2ban", "swap", "ntp", "firewall"} {
		res := map[string]string{"fix": id, "status": "ok"}
		if _, err := s.Fix(ctx, actor, id, clientIP); err != nil {
			res["status"], res["error"] = "failed", err.Error()
		}
		out = append(out, res)
	}
	return out
}

// StartSchedules runs Lynis weekly when it has been run once by hand.
func (s *Service) StartSchedules(ctx context.Context) {
	if runtime.GOOS != "linux" {
		return
	}
	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if v, _, _ := s.st.Setting(ctx, "security.lynis_score"); v == "" {
				continue
			}
			last, _, _ := s.st.Setting(ctx, "security.lynis_at")
			if tm, err := time.Parse(time.RFC3339, last); err == nil && time.Since(tm) < 7*24*time.Hour {
				continue
			}
			if _, _, err := s.Lynis(ctx, "system"); err == nil {
				_ = s.st.SetSetting(ctx, "security.lynis_at", time.Now().UTC().Format(time.RFC3339))
			}
		}
	}()
}

// Fix applies a one-click fix by id.
func (s *Service) Fix(ctx context.Context, actor, id, clientIP string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errors.New("fixes run on Linux servers only")
	}
	var out string
	var err error
	switch id {
	case "firewall":
		out, err = s.EnableFirewall(ctx, actor, clientIP, FirewallOptions{})
	case "firewall-routes":
		out, err = s.RepairRoutes(ctx, actor)
	case "fail2ban":
		out, err = s.aptInstall(ctx, actor, "fail2ban")
		if err == nil {
			jail := "[sshd]\nenabled = true\nmaxretry = 5\nfindtime = 10m\nbantime = 1h\n[recidive]\nenabled = true\nbantime = 1w\nfindtime = 1d\nmaxretry = 3\n"
			_ = os.WriteFile("/etc/fail2ban/jail.d/islet.conf", []byte(jail), 0o644)
			_, err = s.sh(ctx, actor, "systemctl", "enable", "--now", "fail2ban")
			if err == nil {
				_, _ = s.sh(ctx, actor, "systemctl", "restart", "fail2ban")
			}
		}
	case "auto-updates":
		out, err = s.aptInstall(ctx, actor, "unattended-upgrades")
		if err == nil {
			_ = os.WriteFile("/etc/apt/apt.conf.d/20auto-upgrades", []byte("APT::Periodic::Update-Package-Lists \"1\";\nAPT::Periodic::Unattended-Upgrade \"1\";\nAPT::Periodic::AutocleanInterval \"7\";\n"), 0o644)
			_, err = s.sh(ctx, actor, "systemctl", "enable", "--now", "unattended-upgrades")
		}
	case "apt-upgrade":
		out, err = s.sh(ctx, actor, "sh", "-c", "DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold upgrade")
	case "swap":
		if _, statErr := os.Stat("/swapfile"); statErr == nil {
			return "", errors.New("/swapfile already exists")
		}
		out, err = s.sh(ctx, actor, "sh", "-c", "fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile && grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab && sysctl -w vm.swappiness=10 && grep -q vm.swappiness /etc/sysctl.conf || echo 'vm.swappiness=10' >> /etc/sysctl.conf")
	case "ntp":
		out, err = s.sh(ctx, actor, "sh", "-c", "timedatectl set-ntp true && systemctl enable --now systemd-timesyncd 2>/dev/null; timedatectl")
	case "ssh-harden":
		cfg := s.readSSHD(ctx)
		cfg.PermitRoot, cfg.PasswordAuth, cfg.PubkeyAuth = false, false, true
		out, err = s.ApplySSH(ctx, actor, cfg, true)
	default:
		return "", errors.New("unknown fix")
	}
	if err != nil {
		return out, err
	}
	_ = s.st.Audit(ctx, actor, "security.fix", id, "")
	return out, nil
}

func (s *Service) aptInstall(ctx context.Context, actor, pkg string) (string, error) {
	if !has("apt-get") {
		return "", errors.New("only Debian and Ubuntu are supported for automatic installs; install " + pkg + " with your package manager")
	}
	return s.sh(ctx, actor, "sh", "-c", "DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "+pkg)
}

// ---- firewall ----

var ufwRuleRe = regexp.MustCompile(`^(\S+)(?:/(tcp|udp))?\s+ALLOW IN\s+(\S+)(?:\s+#\s*(.*))?$`)

// FirewallStatus reads ufw.
func (s *Service) FirewallStatus(ctx context.Context) Firewall {
	fw := Firewall{Rules: []FirewallRule{}}
	if runtime.GOOS != "linux" || !has("ufw") {
		return fw
	}
	fw.Installed = true
	out, err := s.sh(ctx, "system", "ufw", "status")
	if err != nil {
		return fw
	}
	fw.Active, fw.Rules = ParseUFWStatus(out)
	if b, err := os.ReadFile("/etc/ufw/after.rules"); err == nil && strings.Contains(string(b), "BEGIN UFW AND DOCKER") {
		fw.DockerOK = true
	}
	httpP, httpsP := s.ports()
	fw.MissingRoutes = MissingRoutes(fw, httpP, httpsP)
	return fw
}

// ports reports the proxy's published ports, falling back to the defaults when
// nothing told us otherwise.
func (s *Service) ports() (string, string) {
	if s.proxyPorts != nil {
		if a, b := s.proxyPorts(); a != "" && b != "" {
			return a, b
		}
	}
	return "80", "443"
}

// dockerRules is the widely used ufw-docker snippet: published container
// ports only accept traffic that ufw allowed.
const dockerRules = `
# BEGIN UFW AND DOCKER
*filter
:ufw-user-forward - [0:0]
:ufw-docker-logging-deny - [0:0]
:DOCKER-USER - [0:0]
-A DOCKER-USER -j ufw-user-forward
-A DOCKER-USER -j RETURN -s 10.0.0.0/8
-A DOCKER-USER -j RETURN -s 172.16.0.0/12
-A DOCKER-USER -j RETURN -s 192.168.0.0/16
-A DOCKER-USER -p udp -m udp --sport 53 --dport 1024:65535 -j RETURN
-A DOCKER-USER -j ufw-docker-logging-deny -p tcp -m tcp --tcp-flags FIN,SYN,RST,ACK SYN -d 192.168.0.0/16
-A DOCKER-USER -j ufw-docker-logging-deny -p tcp -m tcp --tcp-flags FIN,SYN,RST,ACK SYN -d 10.0.0.0/8
-A DOCKER-USER -j ufw-docker-logging-deny -p tcp -m tcp --tcp-flags FIN,SYN,RST,ACK SYN -d 172.16.0.0/12
-A DOCKER-USER -j ufw-docker-logging-deny -p udp -m udp --dport 0:32767 -d 192.168.0.0/16
-A DOCKER-USER -j ufw-docker-logging-deny -p udp -m udp --dport 0:32767 -d 10.0.0.0/8
-A DOCKER-USER -j ufw-docker-logging-deny -p udp -m udp --dport 0:32767 -d 172.16.0.0/12
-A DOCKER-USER -j RETURN
-A ufw-docker-logging-deny -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix "[UFW DOCKER BLOCK] "
-A ufw-docker-logging-deny -j DROP
COMMIT
# END UFW AND DOCKER
`

// FirewallOptions tunes how far EnableFirewall opens the panel port. Everything
// else is derived from what this server runs.
type FirewallOptions struct {
	// PanelPublic opens the panel port to the internet. Off by default: the
	// panel is reached on its domain through the proxy, or from the admin's own
	// address, so the only ports the world sees are HTTP and HTTPS.
	PanelPublic bool
}

// Plan reports the openings EnableFirewall would apply, without touching
// anything. The Security page shows it before asking to go ahead.
func (s *Service) Plan(ctx context.Context, clientIP string, opts FirewallOptions) ([]Opening, string) {
	httpP, httpsP := s.ports()
	panelP := "9443"
	if s.panelPort != nil {
		if p := s.panelPort(); p != "" {
			panelP = p
		}
	}
	routed := false
	if s.panelRouted != nil {
		routed = s.panelRouted(ctx)
	}
	admin := clientIP
	if admin == "127.0.0.1" || admin == "::1" {
		admin = ""
	}
	subnet := ""
	if s.proxySubnet != nil {
		subnet = s.proxySubnet(ctx)
	}
	return FirewallPlan(PlanInput{
		SSHPort:     s.readSSHD(ctx).Port,
		HTTPPort:    httpP,
		HTTPSPort:   httpsP,
		PanelPort:   panelP,
		PanelCIDR:   s.PanelCIDR(ctx),
		PanelPublic: opts.PanelPublic,
		PanelDomain: routed,
		AdminIP:     admin,
		ProxySubnet: subnet,
	})
}

// EnableFirewall installs ufw, opens exactly the ports this server needs, makes
// Docker honour it and turns it on.
//
// Ports published by a container get a route rule as well as an input rule.
// Without the route rule, `ufw default deny routed` plus the ufw-docker rules
// leave every site behind the proxy unreachable while the host still answers,
// which is a silent outage that looks like a proxy fault.
func (s *Service) EnableFirewall(ctx context.Context, actor, clientIP string, opts FirewallOptions) (string, error) {
	if !has("ufw") {
		if _, err := s.aptInstall(ctx, actor, "ufw"); err != nil {
			return "", err
		}
	}
	openings, warn := s.Plan(ctx, clientIP, opts)
	for _, o := range openings {
		if err := o.Validate(); err != nil {
			return "", err
		}
	}

	var log strings.Builder
	if warn != "" {
		log.WriteString("[islet] " + warn + "\n")
	}
	cmds := [][]string{
		{"ufw", "--force", "reset"},
		{"ufw", "default", "deny", "incoming"},
		{"ufw", "default", "allow", "outgoing"},
		{"ufw", "default", "deny", "routed"},
	}
	for _, o := range openings {
		cmds = append(cmds, o.Commands()...)
	}
	// Keep the admin reachable whatever else changes.
	if admin := clientIP; admin != "" && admin != "127.0.0.1" && admin != "::1" {
		cmds = append(cmds, []string{"ufw", "allow", "from", admin, "comment", "current admin"})
	}

	// The Docker rules have to be in place before ufw is enabled, or the first
	// reload leaves forwarding denied with nothing to allow it.
	if b, err := os.ReadFile("/etc/ufw/after.rules"); err == nil && !strings.Contains(string(b), "BEGIN UFW AND DOCKER") {
		if err := os.WriteFile("/etc/ufw/after.rules", append(b, []byte(dockerRules)...), 0o640); err != nil {
			return log.String(), fmt.Errorf("write /etc/ufw/after.rules: %w", err)
		}
		log.WriteString("[islet] added the ufw-docker rules to /etc/ufw/after.rules\n")
	}

	for _, c := range cmds {
		out, err := s.sh(ctx, actor, c[0], c[1:]...)
		log.WriteString(out)
		if err != nil {
			return log.String(), fmt.Errorf("%s: %w", strings.Join(c, " "), err)
		}
	}
	out, err := s.sh(ctx, actor, "ufw", "--force", "enable")
	log.WriteString(out)
	if err != nil {
		return log.String(), err
	}
	_, _ = s.sh(ctx, actor, "ufw", "reload")

	// Say plainly what the internet can now reach.
	fw := s.FirewallStatus(ctx)
	if len(fw.MissingRoutes) > 0 {
		log.WriteString("[islet] warning: no forward rule for port " + strings.Join(fw.MissingRoutes, ", ") + "; sites behind the proxy will not answer\n")
	}
	_ = s.st.Audit(ctx, actor, "firewall.enable", strconv.Itoa(len(openings))+" openings", warn)
	return log.String(), nil
}

// RepairRoutes adds the missing forward rules without resetting anything else.
// This is the small, safe fix for a server that is already firewalled and has
// gone dark behind the proxy.
func (s *Service) RepairRoutes(ctx context.Context, actor string) (string, error) {
	fw := s.FirewallStatus(ctx)
	if !fw.Installed {
		return "", errors.New("ufw is not installed")
	}
	if len(fw.MissingRoutes) == 0 {
		return "Nothing to repair: every proxy port already has a forward rule.\n", nil
	}
	var log strings.Builder
	for _, port := range fw.MissingRoutes {
		protos := []string{"tcp"}
		_, httpsP := s.ports()
		if port == httpsP {
			protos = append(protos, "udp")
		}
		for _, proto := range protos {
			o := Opening{Port: port, Proto: proto, Routed: true, Comment: "islet proxy"}
			if err := o.Validate(); err != nil {
				return log.String(), err
			}
			for _, c := range o.Commands() {
				out, err := s.sh(ctx, actor, c[0], c[1:]...)
				log.WriteString(out)
				if err != nil {
					return log.String(), fmt.Errorf("%s: %w", strings.Join(c, " "), err)
				}
			}
		}
	}
	_, _ = s.sh(ctx, actor, "ufw", "reload")
	_ = s.st.Audit(ctx, actor, "firewall.repair", strings.Join(fw.MissingRoutes, ","), "")
	return log.String(), nil
}

// AllowPort adds a rule; from may be "any" or a CIDR. routed adds the forward
// rule a port published by a container needs instead of an input rule.
func (s *Service) AllowPort(ctx context.Context, actor, port, proto, from, comment string, routed bool) error {
	o := Opening{Port: port, Proto: proto, From: from, Comment: comment, Host: true, Routed: routed}
	if err := o.Validate(); err != nil {
		return err
	}
	for _, c := range o.Commands() {
		if _, err := s.sh(ctx, actor, c[0], c[1:]...); err != nil {
			return err
		}
	}
	_ = s.st.Audit(ctx, actor, "firewall.allow", port+"/"+proto, from)
	return nil
}

// DenyPort removes an allow rule from the chain it lives on.
func (s *Service) DenyPort(ctx context.Context, actor, port, proto, from string, routed bool) error {
	if from == "Anywhere" {
		from = ""
	}
	o := Opening{Port: port, Proto: proto, From: from, Host: true, Routed: routed}
	if err := o.Validate(); err != nil {
		return err
	}
	for _, c := range o.DeleteCommands() {
		if _, err := s.sh(ctx, actor, c[0], c[1:]...); err != nil {
			return err
		}
	}
	_ = s.st.Audit(ctx, actor, "firewall.delete", port+"/"+proto, from)
	return nil
}

// BannedIPs lists fail2ban's current bans.
func (s *Service) BannedIPs(ctx context.Context) []string {
	if runtime.GOOS != "linux" || !has("fail2ban-client") {
		return []string{}
	}
	out, err := s.sh(ctx, "system", "fail2ban-client", "banned")
	if err != nil {
		return []string{}
	}
	var raw []map[string][]string
	ips := []string{}
	if json.Unmarshal([]byte(strings.ReplaceAll(out, "'", `"`)), &raw) == nil {
		for _, jail := range raw {
			for name, list := range jail {
				for _, ip := range list {
					ips = append(ips, ip+" ("+name+")")
				}
			}
		}
	}
	sort.Strings(ips)
	return ips
}

// Unban lifts a fail2ban ban.
func (s *Service) Unban(ctx context.Context, actor, ip string) error {
	if !regexp.MustCompile(`^[0-9a-fA-F.:]+$`).MatchString(ip) {
		return errors.New("invalid IP")
	}
	_, err := s.sh(ctx, actor, "fail2ban-client", "unban", ip)
	return err
}

// ---- SSH ----

func (s *Service) readSSHD(ctx context.Context) SSHSettings {
	if b, err := os.ReadFile(s.sshdPath); err == nil {
		return ParseSSHD(string(b))
	}
	// Effective config as sshd sees it.
	if out, err := s.sh(ctx, "system", "sshd", "-T"); err == nil {
		// sshd -T prints lowercase keys; normalise the ones we manage.
		var b strings.Builder
		for _, l := range strings.Split(out, "\n") {
			k, v, ok := strings.Cut(l, " ")
			if !ok {
				continue
			}
			for mk := range managedKeys {
				if strings.EqualFold(mk, k) {
					b.WriteString(mk + " " + v + "\n")
				}
			}
		}
		return ParseSSHD(b.String())
	}
	if b, err := os.ReadFile("/etc/ssh/sshd_config"); err == nil {
		return ParseSSHD(string(b))
	}
	return SSHSettings{Port: 22, PermitRoot: true, PasswordAuth: true, PubkeyAuth: true, MaxAuthTries: 6, ClientAliveMax: 3}
}

// SSH returns the current settings and whether keys are present.
func (s *Service) SSH(ctx context.Context) (SSHSettings, bool, bool) {
	s.mu.Lock()
	pending := s.rollback != nil
	s.mu.Unlock()
	return s.readSSHD(ctx), s.hasAuthorizedKeys(), pending
}

func (s *Service) hasAuthorizedKeys() bool {
	globs := []string{"/root/.ssh/authorized_keys", "/home/*/.ssh/authorized_keys"}
	for _, g := range globs {
		matches, _ := filepath.Glob(g)
		for _, m := range matches {
			if b, err := os.ReadFile(m); err == nil && strings.Contains(string(b), "ssh-") {
				return true
			}
		}
	}
	return false
}

// ApplySSH validates, writes the managed file, tests it with sshd -t and
// reloads. Unless confirmed within five minutes the previous file is
// restored, so a mistake cannot lock the admin out for good.
func (s *Service) ApplySSH(ctx context.Context, actor string, cfg SSHSettings, withRollback bool) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errors.New("SSH settings apply on Linux servers only")
	}
	if err := cfg.Validate(s.hasAuthorizedKeys()); err != nil {
		return "", err
	}
	prev, _ := os.ReadFile(s.sshdPath)
	if err := os.MkdirAll(filepath.Dir(s.sshdPath), 0o755); err != nil {
		return "", err
	}
	// The main config must include the drop-in directory (Ubuntu and Debian do by default).
	if main, err := os.ReadFile("/etc/ssh/sshd_config"); err == nil && !strings.Contains(string(main), "sshd_config.d") {
		_ = os.WriteFile("/etc/ssh/sshd_config", append([]byte("Include /etc/ssh/sshd_config.d/*.conf\n"), main...), 0o644)
	}
	if err := os.WriteFile(s.sshdPath, []byte(RenderSSHD(cfg)), 0o644); err != nil {
		return "", err
	}
	if out, err := s.sh(ctx, actor, "sshd", "-t"); err != nil {
		s.restoreSSHD(prev)
		return out, fmt.Errorf("sshd rejected the configuration, nothing changed: %w", err)
	}
	if _, err := s.sh(ctx, actor, "systemctl", "reload", "ssh"); err != nil {
		if _, err2 := s.sh(ctx, actor, "systemctl", "reload", "sshd"); err2 != nil {
			s.restoreSSHD(prev)
			return "", fmt.Errorf("could not reload sshd, restored the previous file: %w", err2)
		}
	}
	if cfg.Port != 22 && has("ufw") {
		_, _ = s.sh(ctx, actor, "ufw", "limit", strconv.Itoa(cfg.Port)+"/tcp", "comment", "SSH")
	}
	_ = s.st.Audit(ctx, actor, "ssh.apply", "", fmt.Sprintf("port=%d root=%v password=%v", cfg.Port, cfg.PermitRoot, cfg.PasswordAuth))
	msg := "applied"
	if withRollback {
		s.mu.Lock()
		if s.rollback != nil {
			s.rollback.Stop()
		}
		s.prevSSHD = prev
		s.rollback = time.AfterFunc(5*time.Minute, func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.rollback = nil
			s.restoreSSHD(s.prevSSHD)
			_, _ = s.sh(context.Background(), "system", "systemctl", "reload", "ssh")
			if s.bus != nil {
				s.bus.Emit(context.Background(), notify.Event{Category: "security", Severity: notify.Warning, Title: "SSH change rolled back", Message: "The new SSH settings were not confirmed within five minutes, so the previous configuration is back.", Link: "/security"})
			}
		})
		s.mu.Unlock()
		msg = "applied; open a new SSH session now and confirm within 5 minutes, or the change rolls back"
	}
	return msg, nil
}

// ConfirmSSH cancels the rollback timer.
func (s *Service) ConfirmSSH() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rollback == nil {
		return false
	}
	s.rollback.Stop()
	s.rollback = nil
	s.prevSSHD = nil
	return true
}

func (s *Service) restoreSSHD(prev []byte) {
	if len(prev) == 0 {
		_ = os.Remove(s.sshdPath)
		return
	}
	_ = os.WriteFile(s.sshdPath, prev, 0o644)
}

// RestrictPanel allows the panel port only from a CIDR (a VPN range),
// dropping the public rule. An empty cidr restores public access.
func (s *Service) RestrictPanel(ctx context.Context, actor, cidr, clientIP string) error {
	if runtime.GOOS != "linux" || !has("ufw") {
		return errors.New("needs ufw on a Linux server")
	}
	if cidr != "" {
		if !regexp.MustCompile(`^[0-9a-fA-F.:]+/\d{1,3}$`).MatchString(cidr) {
			return errors.New("cidr must look like 10.8.0.0/24 or 100.64.0.0/10")
		}
		if _, err := s.sh(ctx, actor, "ufw", "allow", "from", cidr, "to", "any", "port", "9443", "proto", "tcp", "comment", "Islet panel via VPN"); err != nil {
			return err
		}
		if clientIP != "" {
			_, _ = s.sh(ctx, actor, "ufw", "allow", "from", clientIP, "to", "any", "port", "9443", "proto", "tcp", "comment", "Islet panel current admin")
		}
		_, _ = s.sh(ctx, actor, "ufw", "delete", "allow", "9443/tcp")
		_ = s.st.SetSetting(ctx, "security.panel_cidr", cidr)
	} else {
		if _, err := s.sh(ctx, actor, "ufw", "allow", "9443/tcp", "comment", "Islet panel"); err != nil {
			return err
		}
		if prev, _, _ := s.st.Setting(ctx, "security.panel_cidr"); prev != "" {
			_, _ = s.sh(ctx, actor, "ufw", "delete", "allow", "from", prev, "to", "any", "port", "9443", "proto", "tcp")
		}
		_ = s.st.SetSetting(ctx, "security.panel_cidr", "")
	}
	_ = s.st.Audit(ctx, actor, "firewall.panel", cidr, "")
	return nil
}

// PanelCIDR returns the VPN range the panel is restricted to, if any.
func (s *Service) PanelCIDR(ctx context.Context) string {
	v, _, _ := s.st.Setting(ctx, "security.panel_cidr")
	return v
}

// Lynis installs lynis if needed, runs a quick system audit and records
// the hardening index. Returns the report tail.
func (s *Service) Lynis(ctx context.Context, actor string) (string, int, error) {
	if runtime.GOOS != "linux" {
		return "", 0, errors.New("Lynis runs on Linux servers only")
	}
	if !has("lynis") {
		if _, err := s.aptInstall(ctx, actor, "lynis"); err != nil {
			return "", 0, err
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	out, err := s.sh(cctx, actor, "lynis", "audit", "system", "--quick", "--no-colors", "--quiet")
	if err != nil && out == "" {
		return "", 0, err
	}
	score := 0
	if m := regexp.MustCompile(`Hardening index : (\d+)`).FindStringSubmatch(out); m != nil {
		score, _ = strconv.Atoi(m[1])
	} else if b, err := os.ReadFile("/var/log/lynis.log"); err == nil {
		if m := regexp.MustCompile(`Hardening index : \[(\d+)\]`).FindSubmatch(b); m != nil {
			score, _ = strconv.Atoi(string(m[1]))
		}
	}
	if score > 0 {
		prev, _, _ := s.st.Setting(ctx, "security.lynis_history")
		_ = s.st.SetSetting(ctx, "security.lynis_score", strconv.Itoa(score))
		_ = s.st.SetSetting(ctx, "security.lynis_history", strings.TrimLeft(prev+","+time.Now().UTC().Format("2006-01-02")+":"+strconv.Itoa(score), ","))
	}
	_ = s.st.Audit(ctx, actor, "security.lynis", strconv.Itoa(score), "")
	_ = s.st.SetSetting(ctx, "security.lynis_at", time.Now().UTC().Format(time.RFC3339))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 80 {
		lines = lines[len(lines)-80:]
	}
	return strings.Join(lines, "\n"), score, nil
}

// ---- Trivy ----

// ScanImage runs Trivy in a container against a local image and stores the result.
func (s *Service) ScanImage(ctx context.Context, actor, image string) (*Scan, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/@-]{0,255}$`).MatchString(image) {
		return nil, errors.New("invalid image reference")
	}
	sock := "/var/run/docker.sock"
	if runtime.GOOS == "windows" {
		sock = "//var/run/docker.sock"
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	out, err := s.sh(cctx, actor, "docker", "run", "--rm", "--name", "islet-trivy-"+strconv.FormatInt(time.Now().UnixNano()%1000000, 36), "-v", sock+":/var/run/docker.sock", "-v", "islet-trivy-cache:/root/.cache", "aquasec/trivy:0.65.0", "image", "--quiet", "--format", "json", "--scanners", "vuln", "--severity", "LOW,MEDIUM,HIGH,CRITICAL", image)
	sc := Scan{Target: image, At: time.Now().UTC().Format(time.RFC3339), Findings: []Finding{}}
	if err != nil {
		sc.Error = err.Error()
		s.saveScan(sc)
		return &sc, errors.New("trivy failed: " + strings.TrimSpace(err.Error()))
	}
	var raw struct {
		Results []struct {
			Vulnerabilities []struct {
				ID       string `json:"VulnerabilityID"`
				Pkg      string `json:"PkgName"`
				Version  string `json:"InstalledVersion"`
				Fixed    string `json:"FixedVersion"`
				Severity string `json:"Severity"`
				Title    string `json:"Title"`
			} `json:"Vulnerabilities"`
		} `json:"Results"`
	}
	if i := strings.Index(out, "{"); i >= 0 {
		out = out[i:]
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		sc.Error = "could not parse trivy output"
		s.saveScan(sc)
		return &sc, errors.New(sc.Error)
	}
	for _, r := range raw.Results {
		for _, v := range r.Vulnerabilities {
			switch v.Severity {
			case "CRITICAL":
				sc.Critical++
			case "HIGH":
				sc.High++
			case "MEDIUM":
				sc.Medium++
			default:
				sc.Low++
			}
			if len(sc.Findings) < 300 && (v.Severity == "CRITICAL" || v.Severity == "HIGH") {
				sc.Findings = append(sc.Findings, Finding{ID: v.ID, Package: v.Pkg, Version: v.Version, Fixed: v.Fixed, Severity: v.Severity, Title: v.Title})
			}
		}
	}
	sc.Truncated = sc.Critical+sc.High > len(sc.Findings)
	s.saveScan(sc)
	_ = s.st.Audit(ctx, actor, "security.scan", image, fmt.Sprintf("critical=%d high=%d", sc.Critical, sc.High))
	if sc.Critical > 0 && s.bus != nil {
		s.bus.Emit(ctx, notify.Event{Category: "security", Severity: notify.Warning, Title: "Critical CVEs in " + image, Message: fmt.Sprintf("%d critical and %d high findings. Rebuild on a newer base image.", sc.Critical, sc.High), Link: "/security"})
	}
	return &sc, nil
}

func (s *Service) saveScan(sc Scan) {
	s.mu.Lock()
	s.scans[sc.Target] = sc
	b, _ := json.Marshal(s.scans)
	s.mu.Unlock()
	_ = os.WriteFile(filepath.Join(s.dataDir, "scans.json"), b, 0o600)
}

// Scans returns stored scan results, newest first.
func (s *Service) Scans() []Scan {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Scan, 0, len(s.scans))
	for _, sc := range s.scans {
		out = append(out, sc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out
}

// ---- panic ----

// Panic blocks all inbound traffic except from the caller and the panel
// port from that IP, then returns what the caller must do next. Sessions and
// tokens are rotated by the API layer.
func (s *Service) Panic(ctx context.Context, actor, clientIP string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errors.New("the panic button works on Linux servers only")
	}
	if clientIP == "" || clientIP == "127.0.0.1" || clientIP == "::1" {
		return "", errors.New("cannot determine your public IP; the panic button would lock you out too")
	}
	if !has("ufw") {
		if _, err := s.aptInstall(ctx, actor, "ufw"); err != nil {
			return "", err
		}
	}
	var log strings.Builder
	for _, c := range [][]string{{"ufw", "--force", "reset"}, {"ufw", "default", "deny", "incoming"}, {"ufw", "default", "deny", "routed"}, {"ufw", "allow", "from", clientIP, "comment", "panic: admin"}, {"ufw", "--force", "enable"}} {
		out, err := s.sh(ctx, actor, c[0], c[1:]...)
		log.WriteString(out)
		if err != nil {
			return log.String(), err
		}
	}
	_ = s.st.Audit(ctx, actor, "security.panic", clientIP, "")
	if s.bus != nil {
		s.bus.Emit(ctx, notify.Event{Category: "security", Severity: notify.Critical, Title: "Panic button pressed", Message: "All inbound traffic is blocked except from " + clientIP + ". Sessions and API tokens were revoked.", Link: "/security"})
	}
	return log.String(), nil
}
