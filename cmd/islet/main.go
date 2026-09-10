// Command islet is the CLI. It talks to a running isletd over HTTP.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/update"
	"github.com/isletdev/islet/internal/version"
	"github.com/isletdev/islet/pkg/api"
)

func doUpdate(args []string) error {
	check, beta := false, false
	for _, a := range args {
		switch a {
		case "--check":
			check = true
		case "--beta":
			beta = true
		default:
			return fmt.Errorf("unknown flag %q", a)
		}
	}
	ch := update.Stable
	if beta {
		ch = update.Beta
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	rel, err := update.Latest(ctx, ch)
	if err != nil {
		return err
	}
	if !update.IsNewer(rel.Version, version.Version) {
		fmt.Printf("islet %s is up to date (latest %s)\n", version.Version, rel.Version)
		return nil
	}
	fmt.Printf("update available: %s -> %s (%s)\n", version.Version, rel.Version, rel.PublishedAt.Format("2006-01-02"))
	if check {
		return nil
	}
	res, err := update.Apply(ctx, rel, version.Version, func(msg string, kv ...any) { fmt.Println(msg) })
	if err != nil {
		return err
	}
	fmt.Printf("installed %s at %s\n", res.To, res.Path)
	if runtime.GOOS == "linux" {
		if out, err := exec.Command("systemctl", "restart", "isletd").CombinedOutput(); err != nil {
			fmt.Printf("restart isletd yourself: %s\n", strings.TrimSpace(string(out)))
		} else {
			fmt.Println("isletd restarted")
		}
	}
	return nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Printf("islet %s (%s, %s)\n", version.Version, version.Commit, version.Date)
	case "status":
		if err := status(); err != nil {
			fmt.Fprintln(os.Stderr, "islet:", err)
			os.Exit(1)
		}
	case "update":
		if err := doUpdate(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "islet:", err)
			os.Exit(1)
		}
	case "login":
		run(cmdLogin(os.Args[2:]))
	case "whoami":
		run(cmdWhoami())
	case "apps":
		run(cmdApps())
	case "deploy":
		run(cmdDeploy(os.Args[2:]))
	case "logs":
		run(cmdLogs(os.Args[2:]))
	case "restart":
		run(cmdRestart(os.Args[2:]))
	case "cron":
		run(cmdCron(os.Args[2:]))
	case "notify":
		run(cmdNotify(os.Args[2:]))
	case "db":
		run(cmdDB(os.Args[2:]))
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "islet: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func run(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "islet:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`islet — manage a server running isletd

Usage:
  islet login --url https://host:9443 --token islet_…
                            save an API token (Settings → API tokens)
  islet whoami              show who the saved token belongs to
  islet apps                list deployed apps
  islet deploy <app> [--redeploy] [--rollback <n>]
                            deploy the latest commit, redeploy, or roll back; streams the log
  islet logs <app|container> [-f] [-n 200]
  islet restart <app|container>
  islet cron [list | run <job>]
  islet notify "title" [-m "message"] [--severity info|warning|critical]
  islet db [list | shell <instance>]
  islet status              show whether isletd is running and its version
  islet update [--check] [--beta]
                            install the latest signed release and restart isletd
  islet version             print the CLI version

Environment:
  ISLET_URL         daemon address (default https://127.0.0.1:9443)
  ISLET_TOKEN       API token, instead of the saved login
  ISLET_CONFIG      config file (default ~/.config/islet/config.json)
  ISLET_DATA_DIR    where the daemon keeps its certificate (default /var/lib/islet)
`)
}

func daemonURL() string {
	if v := os.Getenv("ISLET_URL"); v != "" {
		return v
	}
	return "https://127.0.0.1:9443"
}

func dataDir() string {
	if v := os.Getenv("ISLET_DATA_DIR"); v != "" {
		return v
	}
	if runtime.GOOS == "linux" {
		return "/var/lib/islet"
	}
	return ".data"
}

// httpClient trusts the daemon's self-signed certificate when it can read
// it from the data directory, and the system roots otherwise.
func httpClient() *http.Client {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if pemBytes, err := os.ReadFile(filepath.Join(dataDir(), "tls", "cert.pem")); err == nil {
		pool, _ := x509.SystemCertPool()
		if pool == nil {
			pool = x509.NewCertPool()
		}
		pool.AppendCertsFromPEM(pemBytes)
		tlsCfg.RootCAs = pool
	}
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsCfg}}
}

func status() error {
	client := httpClient()
	resp, err := client.Get(daemonURL() + "/api/v1/health")
	if err != nil {
		return fmt.Errorf("isletd not reachable at %s: %w", daemonURL(), err)
	}
	defer resp.Body.Close()
	var h api.Health
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return fmt.Errorf("bad response: %w", err)
	}
	fmt.Printf("isletd %s  status=%s  host=%s  up=%s  server=%s\n",
		h.Version, h.Status, h.Hostname, (time.Duration(h.UptimeSeconds) * time.Second).String(), h.ServerID[:8])
	return nil
}
