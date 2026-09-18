package security

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/hostown"
)

// Dropping traffic from addresses that are known to be hostile, and from
// countries a server has no business hearing from, before it reaches anything.
//
// Two decisions shape everything here.
//
// The lists are fetched and applied by Islet rather than by a second daemon.
// CrowdSec is the obvious thing to reach for and it is a fine piece of
// software, but it means a third-party apt repository, an agent reading logs,
// and a bouncer writing firewall rules — on a box where Islet already owns the
// firewall and has spent real effort making Docker's published ports honour it.
// Two things writing iptables is how a server ends up in a state nobody can
// explain. What people actually want from it is on this page instead: the
// community's list of known-bad addresses, refreshed daily, dropped at the
// edge.
//
// And the set is rebuilt from cached files at startup rather than persisted
// into the firewall's own configuration. An ipset does not survive a reboot;
// the alternatives are ipset-persistent, a systemd unit, or the daemon doing at
// boot what it does on demand. The daemon is already there and already
// reconciles the proxy and the containers, so this is one more thing it puts
// back — and there is no saved file that can disagree with the running set.

// Source is one published list of addresses worth dropping.
type Source struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Note  string `json:"note"`
}

// Sources are the lists Islet offers. All free, none needs an account, and
// each is conservative enough to run in front of a web server: none of them
// lists an address for having scanned once.
var Sources = []Source{
	{
		ID: "firehol1", Title: "FireHOL level 1",
		URL:  "https://raw.githubusercontent.com/firehol/blocklist-ipsets/master/firehol_level1.netset",
		Note: "Fullbogons and networks that should never appear as a source. The safest list to start with.",
	},
	{
		ID: "spamhaus-drop", Title: "Spamhaus DROP",
		URL:  "https://www.spamhaus.org/drop/drop.txt",
		Note: "Networks hijacked or leased outright by criminal operations.",
	},
	{
		ID: "ipsum", Title: "IPsum (seen on three or more lists)",
		URL:  "https://raw.githubusercontent.com/stamparm/ipsum/master/levels/3.txt",
		Note: "Addresses several independent trackers agree are attacking. Refreshed daily.",
	},
}

// countryURL is where a country's networks come from. ipdeny publishes
// aggregated zone files, which keeps a country to a few thousand entries
// instead of tens of thousands.
func countryURL(cc string) string {
	return "https://www.ipdeny.com/ipblocks/data/aggregated/" + strings.ToLower(cc) + "-aggregated.zone"
}

const (
	setName = "islet-block"
	tmpSet  = "islet-block-new"
	// maxelem has to be set at creation and cannot grow, so it is set well
	// above what every list together comes to.
	maxElem = 262144
)

// BlocklistState is what the panel shows.
type BlocklistState struct {
	Available bool     `json:"available"` // ipset and iptables are present
	Enabled   bool     `json:"enabled"`
	Sources   []string `json:"sources"`
	Countries []string `json:"countries"`
	Entries   int      `json:"entries"`
	UpdatedAt string   `json:"updatedAt"`
	LastError string   `json:"lastError,omitempty"`
	Catalog   []Source `json:"catalog"`
}

// Blocklist reads the current state, from the system where it can.
func (s *Service) Blocklist(ctx context.Context) BlocklistState {
	st := BlocklistState{Catalog: Sources}
	if runtime.GOOS != "linux" {
		return st
	}
	st.Available = has("ipset") && has("iptables")
	en, _, _ := s.st.Setting(ctx, "security.blocklist.enabled")
	st.Enabled = en == "on"
	st.Sources = splitCSV(settingOf(ctx, s, "security.blocklist.sources"))
	st.Countries = splitCSV(settingOf(ctx, s, "security.blocklist.countries"))
	st.UpdatedAt = settingOf(ctx, s, "security.blocklist.at")
	st.LastError = settingOf(ctx, s, "security.blocklist.error")
	// The count comes from the running set, not from what was stored when it
	// was last written: those disagree after a reboot, and the one that matters
	// is what the kernel is actually matching on.
	if st.Available {
		if out, err := s.run.Read(ctx, "ipset", "list", setName, "-terse"); err == nil {
			for _, line := range strings.Split(out.Stdout, "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Number of entries:"); ok {
					st.Entries = atoiSafe(strings.TrimSpace(v))
				}
			}
		}
	}
	return st
}

// BlocklistOptions is what an admin chose.
type BlocklistOptions struct {
	Sources   []string `json:"sources"`
	Countries []string `json:"countries"`
	// AdminIP is never dropped, whatever a list says. It is the address of the
	// person pressing the button, and a firewall feature that can lock out the
	// only person who can undo it is not one worth having.
	AdminIP string `json:"-"`
}

// EnableBlocklist installs what it needs, fetches the lists and starts
// dropping. It is safe to call again: applying is how a refresh works too.
func (s *Service) EnableBlocklist(ctx context.Context, actor string, opt BlocklistOptions) (BlocklistState, error) {
	if runtime.GOOS != "linux" {
		return BlocklistState{}, errors.New("blocklists run on Linux servers only")
	}
	if len(opt.Sources) == 0 && len(opt.Countries) == 0 {
		return BlocklistState{}, errors.New("choose at least one list or country")
	}
	if !has("ipset") {
		if _, err := s.aptInstall(ctx, actor, "ipset"); err != nil {
			return BlocklistState{}, fmt.Errorf("install ipset: %w", err)
		}
	}
	_ = s.st.SetSetting(ctx, "security.blocklist.sources", strings.Join(opt.Sources, ","))
	_ = s.st.SetSetting(ctx, "security.blocklist.countries", strings.Join(opt.Countries, ","))
	_ = s.st.SetSetting(ctx, "security.blocklist.enabled", "on")
	if err := s.applyBlocklist(ctx, actor, opt.AdminIP); err != nil {
		_ = s.st.SetSetting(ctx, "security.blocklist.error", err.Error())
		return s.Blocklist(ctx), err
	}
	_ = s.st.SetSetting(ctx, "security.blocklist.error", "")
	_ = s.st.Audit(ctx, actor, "security.blocklist.enable", "",
		fmt.Sprintf("sources=%s countries=%s", strings.Join(opt.Sources, " "), strings.Join(opt.Countries, " ")))
	return s.Blocklist(ctx), nil
}

// DisableBlocklist takes the rules out and forgets the set.
func (s *Service) DisableBlocklist(ctx context.Context, actor string) error {
	if runtime.GOOS != "linux" {
		return errors.New("blocklists run on Linux servers only")
	}
	_ = s.st.SetSetting(ctx, "security.blocklist.enabled", "")
	_ = s.st.SetSetting(ctx, "security.blocklist.error", "")
	s.dropRules(ctx, actor)
	_, _ = s.run.Run(ctx, actor, "ipset", "destroy", setName)
	_ = s.st.Audit(ctx, actor, "security.blocklist.disable", "", "")
	return nil
}

// RefreshBlocklist re-fetches every chosen list and swaps the set under the
// running rules. Called daily, and by the button on the page.
func (s *Service) RefreshBlocklist(ctx context.Context, actor string) (BlocklistState, error) {
	if en, _, _ := s.st.Setting(ctx, "security.blocklist.enabled"); en != "on" {
		return s.Blocklist(ctx), errors.New("the blocklist is off")
	}
	if err := s.applyBlocklist(ctx, actor, ""); err != nil {
		_ = s.st.SetSetting(ctx, "security.blocklist.error", err.Error())
		return s.Blocklist(ctx), err
	}
	_ = s.st.SetSetting(ctx, "security.blocklist.error", "")
	return s.Blocklist(ctx), nil
}

// ReapplyBlocklist puts the set and the rules back after a restart, from the
// files already on disk. Nothing is fetched: a box that reboots without a
// network yet still comes up dropping what it dropped yesterday.
func (s *Service) ReapplyBlocklist(ctx context.Context) {
	if runtime.GOOS != "linux" {
		return
	}
	if en, _, _ := s.st.Setting(ctx, "security.blocklist.enabled"); en != "on" {
		return
	}
	nets := s.cachedNets(ctx)
	if len(nets) == 0 {
		return
	}
	if err := s.loadSet(ctx, "system", nets); err != nil {
		s.log.Warn("blocklist could not be restored", "err", err)
		return
	}
	s.installRules(ctx, "system")
	s.log.Info("blocklist restored", "entries", len(nets))
}

// applyBlocklist fetches, filters, loads and installs, in that order. The set
// is swapped in whole, so there is no moment where half a list is enforced.
func (s *Service) applyBlocklist(ctx context.Context, actor, adminIP string) error {
	// One ipset, one set of iptables rules, one machine. A second daemon
	// applying its own lists replaces whatever the first one was dropping, with
	// nothing to say it happened — and since the rules point at the set by
	// name, the first daemon goes on believing its own configuration is in
	// force.
	if err := hostown.Claim(s.dataDir); err != nil {
		return hostown.Refusal("firewall blocklist")
	}
	sources := splitCSV(settingOf(ctx, s, "security.blocklist.sources"))
	countries := splitCSV(settingOf(ctx, s, "security.blocklist.countries"))

	seen := map[string]bool{}
	var nets []string
	var failed []string
	fetch := func(name, url string) {
		body, err := s.fetchList(ctx, url)
		if err != nil {
			failed = append(failed, name+": "+err.Error())
			return
		}
		_ = os.WriteFile(s.listPath(name), body, 0o600)
		for _, n := range ParseList(string(body)) {
			if !seen[n] {
				seen[n] = true
				nets = append(nets, n)
			}
		}
	}
	for _, id := range sources {
		for _, src := range Sources {
			if src.ID == id {
				fetch(src.ID, src.URL)
			}
		}
	}
	for _, cc := range countries {
		if len(cc) == 2 {
			fetch("country-"+strings.ToLower(cc), countryURL(cc))
		}
	}
	// A list that did not answer must not quietly shrink what is being
	// dropped, so the cached copy from last time is used instead.
	if len(failed) > 0 {
		for _, n := range s.cachedNets(ctx) {
			if !seen[n] {
				seen[n] = true
				nets = append(nets, n)
			}
		}
	}
	nets = ExcludeAddress(nets, adminIP)
	if len(nets) == 0 {
		return errors.New("no usable addresses in any list: " + strings.Join(failed, "; "))
	}
	if err := s.loadSet(ctx, actor, nets); err != nil {
		return err
	}
	s.installRules(ctx, actor)
	_ = s.st.SetSetting(ctx, "security.blocklist.at", time.Now().UTC().Format(time.RFC3339))
	if len(failed) > 0 {
		return errors.New("using the cached copy for " + strings.Join(failed, "; "))
	}
	return nil
}

func (s *Service) listPath(name string) string {
	dir := filepath.Join(s.dataDir, "blocklists")
	_ = os.MkdirAll(dir, 0o700)
	return filepath.Join(dir, name+".list")
}

// cachedNets reads every list already on disk, which is what a restart and a
// failed fetch both fall back to.
func (s *Service) cachedNets(ctx context.Context) []string {
	names := append([]string{}, splitCSV(settingOf(ctx, s, "security.blocklist.sources"))...)
	for _, cc := range splitCSV(settingOf(ctx, s, "security.blocklist.countries")) {
		names = append(names, "country-"+strings.ToLower(cc))
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		b, err := os.ReadFile(s.listPath(n))
		if err != nil {
			continue
		}
		for _, cidr := range ParseList(string(b)) {
			if !seen[cidr] {
				seen[cidr] = true
				out = append(out, cidr)
			}
		}
	}
	return out
}

func (s *Service) fetchList(ctx context.Context, url string) ([]byte, error) {
	c, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(c, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "islet")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("%s said %d", url, res.StatusCode)
	}
	// 8 MB is several times the largest of these lists and still small enough
	// that a redirected or hostile URL cannot fill the disk.
	return io.ReadAll(io.LimitReader(res.Body, 8<<20))
}

// loadSet builds the new set beside the live one and swaps them, so the rules
// never point at a set that is half loaded.
func (s *Service) loadSet(ctx context.Context, actor string, nets []string) error {
	// One process for the whole list: ipset add per address would be tens of
	// thousands of commands, and every one of them audited.
	res, err := s.run.RunInput(ctx, actor, []byte(RestoreScript(nets)), "ipset", "restore")
	if err != nil {
		return fmt.Errorf("load the set: %s", strings.TrimSpace(res.Stderr+" "+res.Stdout))
	}
	return nil
}

// RestoreScript is the ipset program that loads a list.
//
// The live set is created if it is missing, the new one is filled beside it and
// the two are swapped: an atomic rename, so the rules never point at a set that
// is half loaded and there is no moment where part of a list is enforced.
func RestoreScript(nets []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "create %s hash:net family inet maxelem %d -exist\n", setName, maxElem)
	fmt.Fprintf(&b, "create %s hash:net family inet maxelem %d -exist\n", tmpSet, maxElem)
	fmt.Fprintf(&b, "flush %s\n", tmpSet)
	for _, n := range nets {
		fmt.Fprintf(&b, "add %s %s -exist\n", tmpSet, n)
	}
	fmt.Fprintf(&b, "swap %s %s\n", tmpSet, setName)
	fmt.Fprintf(&b, "destroy %s\n", tmpSet)
	return b.String()
}

// installRules puts the two DROP rules in front, once each.
//
// INPUT covers what the host itself listens on; DOCKER-USER covers the ports
// containers publish, which bypass INPUT entirely — the same split the firewall
// page has to deal with, for the same reason.
// ruleArgs is the match this installs, without the chain.
//
// --ctstate NEW is the part that took a real outage to learn. A DROP on a
// source address matches every packet from it, and the replies to a connection
// this server opened are packets from it: so turning the blocklist on stopped
// the server talking to anything a list happened to name. It was not
// theoretical and it was not obvious, because the thing it broke first was the
// blocklist itself — ipdeny.com, where the country zone files come from,
// resolves to an address that is in IPsum. Enabling country blocks therefore
// made country blocks impossible to download, silently, forever, and the panel
// went on reporting the lists as loaded because the cached copy was still
// there.
//
// The same shape would break anything else the server reaches out to that
// happens to be on a list: a registry pull, an ACME challenge, a git clone, a
// webhook. Only a connection somebody else opens is worth dropping.
func ruleArgs(chain string) []string {
	return []string{chain, "-m", "set", "--match-set", setName, "src", "-m", "conntrack", "--ctstate", "NEW", "-j", "DROP"}
}

// legacyRuleArgs is the rule this used to install, kept so an upgrade removes
// it rather than leaving a stateless DROP behind next to the new one.
func legacyRuleArgs(chain string) []string {
	return []string{chain, "-m", "set", "--match-set", setName, "src", "-j", "DROP"}
}

func (s *Service) installRules(ctx context.Context, actor string) {
	for _, chain := range []string{"INPUT", "DOCKER-USER"} {
		// The old stateless rule goes first, or both would match and the old
		// one would keep dropping the replies this fix exists to allow.
		for range 4 {
			if _, err := s.run.Read(ctx, "iptables", append([]string{"-C"}, legacyRuleArgs(chain)...)...); err != nil {
				break
			}
			if _, err := s.run.Run(ctx, actor, "iptables", append([]string{"-D"}, legacyRuleArgs(chain)...)...); err != nil {
				break
			}
		}
		if _, err := s.run.Read(ctx, "iptables", append([]string{"-C"}, ruleArgs(chain)...)...); err == nil {
			continue
		}
		if _, err := s.run.Run(ctx, actor, "iptables", append([]string{"-I", chain, "1"}, ruleArgs(chain)[1:]...)...); err != nil {
			s.log.Warn("blocklist rule could not be installed", "chain", chain, "err", err)
		}
	}
}

func (s *Service) dropRules(ctx context.Context, actor string) {
	for _, chain := range []string{"INPUT", "DOCKER-USER"} {
		for _, args := range [][]string{ruleArgs(chain), legacyRuleArgs(chain)} {
			for range 4 { // a rule could have been added more than once by hand
				if _, err := s.run.Read(ctx, "iptables", append([]string{"-C"}, args...)...); err != nil {
					break
				}
				if _, err := s.run.Run(ctx, actor, "iptables", append([]string{"-D"}, args...)...); err != nil {
					break
				}
			}
		}
	}
}

// ParseList turns a published list into CIDRs worth dropping.
//
// The formats differ — one address per line, a network per line, a network
// followed by a semicolon and prose — and what they have in common is that
// anything after # or ; is commentary. Everything that is not a usable public
// IPv4 network is dropped here rather than at the kernel, including the entries
// that would be catastrophic: a /0 would black-hole the internet, and a private
// range would cut the host off from its own containers.
func ParseList(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Some lists put the network first and data after whitespace.
		if i := strings.IndexAny(line, " \t"); i > 0 {
			line = line[:i]
		}
		cidr, ok := safeCIDR(line)
		if !ok || seen[cidr] {
			continue
		}
		seen[cidr] = true
		out = append(out, cidr)
	}
	sort.Strings(out)
	return out
}

// safeCIDR normalises one entry and says whether it may be dropped at all.
func safeCIDR(s string) (string, bool) {
	if !strings.Contains(s, "/") {
		if ip := net.ParseIP(s); ip != nil && ip.To4() != nil {
			s += "/32"
		} else {
			return "", false
		}
	}
	ip, n, err := net.ParseCIDR(s)
	if err != nil || ip.To4() == nil {
		return "", false // IPv6 needs its own set; a v4 set refuses it outright
	}
	ones, _ := n.Mask.Size()
	// Nothing wider than a /8. Every list here is built of /32s and small
	// networks; an entry wider than that is a mistake or an attack on the
	// reader, and either way it takes the server off the internet.
	if ones < 8 {
		return "", false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
		return "", false
	}
	// Docker's own ranges are private and already excluded, but carrier-grade
	// NAT is not private and a home connection can sit inside it.
	cgnat := net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
	if cgnat.Contains(ip) {
		return "", false
	}
	return n.String(), true
}

// ExcludeAddress takes one address out of a list of networks — the admin's own,
// so that pressing the button cannot be the last thing they do.
//
// A network that contains the address is dropped whole rather than split: these
// lists are mostly /32s and small networks, losing one of them costs nothing,
// and splitting a network correctly is a great deal of code to get subtly wrong
// in the one place where being wrong locks somebody out of their server.
func ExcludeAddress(nets []string, addr string) []string {
	ip := net.ParseIP(strings.TrimSpace(addr))
	if ip == nil {
		return nets
	}
	out := nets[:0]
	for _, n := range nets {
		if _, netw, err := net.ParseCIDR(n); err == nil && netw.Contains(ip) {
			continue
		}
		out = append(out, n)
	}
	return out
}

// splitCSV returns an empty slice rather than nil: these go straight out as
// JSON, and a null where the panel expects a list is a crash in the browser
// rather than an empty card.
func splitCSV(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func settingOf(ctx context.Context, s *Service, key string) string {
	v, _, _ := s.st.Setting(ctx, key)
	return v
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}
