package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// config is what `islet login` stores.
type config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func configPath() string {
	if v := os.Getenv("ISLET_CONFIG"); v != "" {
		return v
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "islet", "config.json")
}

func loadConfig() (config, error) {
	var c config
	if v := os.Getenv("ISLET_TOKEN"); v != "" {
		c.Token = v
		c.URL = daemonURL()
		return c, nil
	}
	b, err := os.ReadFile(configPath())
	if err != nil {
		if sock := localSocket(); sock != "" {
			c.URL = "http://islet"
			c.Token = "socket:" + sock
			return c, nil
		}
		return c, errors.New("not logged in: run `islet login --url https://your-server:9443 --token islet_…` (create a token in Settings → API tokens), or run as root on the server itself")
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if v := os.Getenv("ISLET_URL"); v != "" {
		c.URL = v
	}
	return c, nil
}

// client is an authenticated API client.
type client struct {
	cfg  config
	http *http.Client
}

func newClient() (*client, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	h := httpClient()
	h.Timeout = 0
	if sock, ok := strings.CutPrefix(cfg.Token, "socket:"); ok {
		h.Transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}
		cfg.Token = ""
	}
	return &client{cfg: cfg, http: h}, nil
}

// localSocket returns the daemon's Unix socket when this process can use it.
func localSocket() string {
	candidates := []string{os.Getenv("ISLET_SOCKET"), "/run/islet/isletd.sock", filepath.Join(dataDir(), "isletd.sock")}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil && st.Mode()&os.ModeSocket != 0 {
			return p
		}
	}
	return ""
}

func (c *client) do(method, path string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(c.cfg.URL, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	if body != nil || method == "POST" || method == "DELETE" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", c.cfg.URL, err)
	}
	if res.StatusCode >= 400 {
		defer res.Body.Close()
		var e struct{ Message string }
		b, _ := io.ReadAll(res.Body)
		_ = json.Unmarshal(b, &e)
		if e.Message == "" {
			e.Message = strings.TrimSpace(string(b))
		}
		return nil, fmt.Errorf("%s (HTTP %d)", e.Message, res.StatusCode)
	}
	return res, nil
}

func (c *client) get(path string, out any) error {
	res, err := c.do("GET", path, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return json.NewDecoder(res.Body).Decode(out)
}

func (c *client) post(path string, body, out any) error {
	res, err := c.do("POST", path, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// stream prints SSE lines until the end event; returns its error text.
func (c *client) stream(method, path string) error {
	res, err := c.do(method, path, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	event := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			var text string
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &text) != nil {
				continue
			}
			if event == "end" {
				if strings.HasPrefix(text, "error") {
					return errors.New(text)
				}
				return nil
			}
			fmt.Println(text)
		}
	}
	return sc.Err()
}

func flag(args []string, name string) (string, []string) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], append(append([]string{}, args[:i]...), args[i+2:]...)
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"="), append(append([]string{}, args[:i]...), args[i+1:]...)
		}
	}
	return "", args
}

func hasFlag(args []string, name string) (bool, []string) {
	for i, a := range args {
		if a == name {
			return true, append(append([]string{}, args[:i]...), args[i+1:]...)
		}
	}
	return false, args
}

func cmdLogin(args []string) error {
	u, args := flag(args, "--url")
	tok, _ := flag(args, "--token")
	if u == "" {
		u = daemonURL()
	}
	if tok == "" {
		fmt.Print("API token (Settings → API tokens): ")
		sc := bufio.NewScanner(os.Stdin)
		if sc.Scan() {
			tok = strings.TrimSpace(sc.Text())
		}
	}
	if !strings.HasPrefix(tok, "islet_") {
		return errors.New("that does not look like an Islet token")
	}
	c := &client{cfg: config{URL: u, Token: tok}, http: httpClient()}
	var me struct {
		User struct{ Username, Role string }
	}
	if err := c.get("/api/v1/auth/me", &me); err != nil {
		return err
	}
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c.cfg, "", "  ")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return err
	}
	fmt.Printf("logged in to %s as %s (%s); saved to %s\n", u, me.User.Username, me.User.Role, p)
	return nil
}

func cmdApps() error {
	c, err := newClient()
	if err != nil {
		return err
	}
	var apps []struct {
		ID, Name, Status, Framework, URL, Branch string
		LastRelease                              *struct {
			Number     int
			Status     string
			StartedAt  string
			DurationMs int64
		}
	}
	if err := c.get("/api/v1/apps", &apps); err != nil {
		return err
	}
	if len(apps) == 0 {
		fmt.Println("no apps yet")
		return nil
	}
	for _, a := range apps {
		last := "never deployed"
		if a.LastRelease != nil {
			last = fmt.Sprintf("#%d %s %s", a.LastRelease.Number, a.LastRelease.Status, a.LastRelease.StartedAt[:16])
		}
		fmt.Printf("%-20s %-8s %-14s %-40s %s\n", a.Name, a.Status, a.Framework, a.URL, last)
	}
	return nil
}

func appID(c *client, name string) (string, error) {
	var apps []struct{ ID, Name string }
	if err := c.get("/api/v1/apps", &apps); err != nil {
		return "", err
	}
	for _, a := range apps {
		if a.Name == name || a.ID == name {
			return a.ID, nil
		}
	}
	return "", fmt.Errorf("no app named %q", name)
}

func cmdDeploy(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: islet deploy <app> [--redeploy] [--rollback <release-number>]")
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	id, err := appID(c, args[0])
	if err != nil {
		return err
	}
	q := ""
	if re, rest := hasFlag(args[1:], "--redeploy"); re {
		q = "?redeploy=1"
		args = append(args[:1], rest...)
	}
	if n, _ := flag(args[1:], "--rollback"); n != "" {
		var rels []struct {
			ID     int64
			Number int
		}
		if err := c.get("/api/v1/apps/"+id+"/releases", &rels); err != nil {
			return err
		}
		for _, r := range rels {
			if fmt.Sprint(r.Number) == n {
				q = fmt.Sprintf("?release=%d", r.ID)
			}
		}
		if q == "" {
			return fmt.Errorf("no release #%s", n)
		}
	}
	return c.stream("POST", "/api/v1/apps/"+id+"/deploy"+q)
}

func cmdLogs(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: islet logs <container|app> [-f] [-n lines]")
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	follow, args := hasFlag(args, "-f")
	n, args := flag(args, "-n")
	if n == "" {
		n = "200"
	}
	target := args[0]
	// An app name resolves to its live container.
	var apps []struct{ Name, Container string }
	if err := c.get("/api/v1/apps", &apps); err == nil {
		for _, a := range apps {
			if a.Name == target && a.Container != "" {
				target = a.Container
			}
		}
	}
	f := "0"
	if follow {
		f = "1"
	}
	return c.stream("GET", "/api/v1/docker/containers/"+url.PathEscape(target)+"/logs?tail="+n+"&follow="+f)
}

func cmdRestart(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: islet restart <container|app>")
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	target := args[0]
	var apps []struct{ Name, Container string }
	if err := c.get("/api/v1/apps", &apps); err == nil {
		for _, a := range apps {
			if a.Name == target && a.Container != "" {
				target = a.Container
			}
		}
	}
	if err := c.post("/api/v1/docker/containers/"+url.PathEscape(target)+"/restart", nil, nil); err != nil {
		return err
	}
	fmt.Println("restarted", target)
	return nil
}

func cmdCron(args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "list" {
		var jobs []struct {
			ID, Name, Schedule, Described, NextRun string
			Enabled                                bool
			LastRun                                *struct{ Status, StartedAt string }
		}
		if err := c.get("/api/v1/cron/jobs", &jobs); err != nil {
			return err
		}
		for _, j := range jobs {
			last := "never"
			if j.LastRun != nil {
				last = j.LastRun.Status + " " + j.LastRun.StartedAt[:16]
			}
			state := "on "
			if !j.Enabled {
				state = "off"
			}
			fmt.Printf("%s %-28s %-16s %-36s last: %s\n", state, j.Name, j.Schedule, j.Described, last)
		}
		return nil
	}
	if args[0] == "run" && len(args) >= 2 {
		var jobs []struct{ ID, Name string }
		if err := c.get("/api/v1/cron/jobs", &jobs); err != nil {
			return err
		}
		for _, j := range jobs {
			if j.Name == args[1] || j.ID == args[1] {
				return c.stream("POST", "/api/v1/cron/jobs/"+j.ID+"/run")
			}
		}
		return fmt.Errorf("no job named %q", args[1])
	}
	return errors.New("usage: islet cron [list | run <job>]")
}

func cmdNotify(args []string) error {
	sev, args := flag(args, "--severity")
	msg, args := flag(args, "-m")
	subject, args := flag(args, "--subject")
	if len(args) < 1 {
		return errors.New(`usage: islet notify "title" [-m "message"] [--severity info|warning|critical] [--subject app-name]`)
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	if sev == "" {
		sev = "info"
	}
	if err := c.post("/api/v1/notify/emit", map[string]string{"severity": sev, "title": strings.Join(args, " "), "message": msg, "subject": subject}, nil); err != nil {
		return err
	}
	fmt.Println("sent")
	return nil
}

func cmdDB(args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "list" {
		var list []struct{ Name, Engine, State, Container, InternalUrl string }
		if err := c.get("/api/v1/databases", &list); err != nil {
			return err
		}
		for _, d := range list {
			fmt.Printf("%-16s %-9s %-8s %s\n", d.Name, d.Engine, d.State, d.Container)
		}
		return nil
	}
	if args[0] == "shell" && len(args) >= 2 {
		var d struct {
			Engine, Container, RootUser, RootPassword, Database string
		}
		if err := c.get("/api/v1/databases/"+url.PathEscape(args[1]), &d); err != nil {
			return err
		}
		var argv []string
		switch d.Engine {
		case "postgres":
			argv = []string{"docker", "exec", "-it", d.Container, "psql", "-U", d.RootUser, "-d", d.Database}
		case "mysql":
			argv = []string{"docker", "exec", "-it", d.Container, "mysql", "-uroot", "-p" + d.RootPassword, d.Database}
		case "redis":
			argv = []string{"docker", "exec", "-it", d.Container, "redis-cli", "-a", d.RootPassword, "--no-auth-warning"}
		case "mongo":
			argv = []string{"docker", "exec", "-it", d.Container, "mongosh", "-u", d.RootUser, "-p", d.RootPassword, "--authenticationDatabase", "admin"}
		default:
			return errors.New("no shell for this engine")
		}
		if _, err := exec.LookPath("docker"); err != nil {
			fmt.Println("docker is not available here; on the server run:")
			fmt.Println(strings.Join(argv, " "))
			return nil
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	return errors.New("usage: islet db [list | shell <instance>]")
}

func cmdBackup(args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	var o struct {
		Destinations []struct{ ID, Name, Repo, LastCheck string }
		Plans        []struct {
			ID, Name, Described, LastRunAt, LastStatus, DestinationID string
			Stale                                                     bool
		}
	}
	if err := c.get("/api/v1/backups", &o); err != nil {
		return err
	}
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list":
		for _, d := range o.Destinations {
			fmt.Printf("destination %-16s %s (checked %s)\n", d.Name, d.Repo, firstNonEmpty(d.LastCheck, "never"))
		}
		for _, p := range o.Plans {
			stale := ""
			if p.Stale {
				stale = "  STALE"
			}
			fmt.Printf("plan        %-16s %-28s last: %s %s%s\n", p.Name, p.Described, firstNonEmpty(p.LastStatus, "never"), p.LastRunAt, stale)
		}
		return nil
	case "run":
		if len(args) < 2 {
			return errors.New("usage: islet backup run <plan>")
		}
		for _, p := range o.Plans {
			if p.Name == args[1] || p.ID == args[1] {
				return c.stream("POST", "/api/v1/backups/plans/"+p.ID+"/run")
			}
		}
		return fmt.Errorf("no plan named %q", args[1])
	case "snapshots", "verify", "restore":
		if len(args) < 2 {
			return errors.New("usage: islet backup " + sub + " <destination> …")
		}
		var dest string
		for _, d := range o.Destinations {
			if d.Name == args[1] || d.ID == args[1] {
				dest = d.ID
			}
		}
		if dest == "" {
			return fmt.Errorf("no destination named %q", args[1])
		}
		switch sub {
		case "snapshots":
			var snaps []struct {
				ID, Time string
				Tags     []string
				Size     int64
			}
			if err := c.get("/api/v1/backups/destinations/"+dest+"/snapshots", &snaps); err != nil {
				return err
			}
			for _, s := range snaps {
				fmt.Printf("%s  %s  %8d MB  %s\n", s.ID, s.Time[:19], s.Size/1048576, strings.Join(s.Tags, ","))
			}
		case "verify":
			var r struct{ Output string }
			if err := c.post("/api/v1/backups/destinations/"+dest+"/verify", nil, &r); err != nil {
				return err
			}
			fmt.Println(strings.TrimSpace(r.Output))
		case "restore":
			if len(args) < 4 {
				return errors.New("usage: islet backup restore <destination> <snapshot|latest> </data/volumes/NAME | /data/path> [--volume NEWNAME]")
			}
			vol, _ := flag(args, "--volume")
			var r struct{ Target string }
			if err := c.post("/api/v1/backups/destinations/"+dest+"/restore", map[string]string{"snapshot": args[2], "include": args[3], "newVolume": vol}, &r); err != nil {
				return err
			}
			fmt.Println("restored to", r.Target)
		}
		return nil
	}
	return errors.New("usage: islet backup [list | run <plan> | snapshots <dest> | verify <dest> | restore <dest> <snapshot> <path> [--volume NAME]]")
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func cmdWhoami() error {
	c, err := newClient()
	if err != nil {
		return err
	}
	var me struct {
		User struct{ Username, Role string }
	}
	if err := c.get("/api/v1/auth/me", &me); err != nil {
		return err
	}
	fmt.Printf("%s (%s) at %s\n", me.User.Username, me.User.Role, c.cfg.URL)
	return nil
}

var _ = time.Second
