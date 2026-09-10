// Command islet is the CLI. It talks to a running isletd over HTTP.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/isletdev/islet/internal/version"
	"github.com/isletdev/islet/pkg/api"
)

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
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "islet: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Print(`islet — manage the server this daemon runs on

Usage:
  islet status      show whether isletd is running and its version
  islet version     print the CLI version

Environment:
  ISLET_URL         daemon address (default https://127.0.0.1:9443)
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
