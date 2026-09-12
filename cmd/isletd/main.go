// Command isletd is the Islet daemon: one process that serves the panel,
// the API, and manages the server it runs on.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/isletdev/islet/internal/api"
	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/backup"
	"github.com/isletdev/islet/internal/catalog"
	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/cron"
	"github.com/isletdev/islet/internal/db"
	"github.com/isletdev/islet/internal/deploy"
	"github.com/isletdev/islet/internal/docker"
	"github.com/isletdev/islet/internal/files"
	"github.com/isletdev/islet/internal/github"
	"github.com/isletdev/islet/internal/metrics"
	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/internal/provider"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/internal/runner"
	"github.com/isletdev/islet/internal/security"
	"github.com/isletdev/islet/internal/store"
	"github.com/isletdev/islet/internal/tlsutil"
	"github.com/isletdev/islet/internal/uptime"
	"github.com/isletdev/islet/internal/version"
	"github.com/isletdev/islet/internal/watch"
	"github.com/isletdev/islet/internal/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "isletd:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		listen  = flag.String("listen", envOr("ISLET_LISTEN", "127.0.0.1:9443"), "address to listen on")
		dataDir = flag.String("data-dir", envOr("ISLET_DATA_DIR", defaultDataDir()), "directory for state")
		logLvl  = flag.String("log-level", envOr("ISLET_LOG_LEVEL", "info"), "debug, info, warn, error")
		tlsMode = flag.String("tls", envOr("ISLET_TLS", "on"), "on: self-signed HTTPS from the data dir; off: plain HTTP (development only)")
		showVer = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	if *showVer {
		fmt.Printf("isletd %s (%s, %s)\n", version.Version, version.Commit, version.Date)
		return nil
	}

	log := newLogger(*logLvl)
	if abs, err := filepath.Abs(*dataDir); err == nil {
		*dataDir = abs
	}
	if err := os.MkdirAll(*dataDir, 0o750); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, filepath.Join(*dataDir, "islet.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	_ = st.Audit(ctx, "system", "daemon.start", "", "version="+version.Version)

	keys, err := auth.LoadOrCreateKeys(*dataDir)
	if err != nil {
		return err
	}
	as, err := auth.New(st, keys, *dataDir)
	if err != nil {
		return err
	}
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				as.PurgeExpired(ctx)
			}
		}
	}()

	// The port the panel ends up on is known only once it is listening, and the
	// firewall rules need it, so it is published here for the hooks to read.
	var panelPort atomic.Value
	panelPort.Store("9443")

	collector := metrics.NewCollector()
	sampler := metrics.NewSampler(collector, st, log)
	go sampler.Run(ctx)
	cmds := cmdrun.New(st, log)
	dk := docker.New(cmds, filepath.Join(*dataDir, "stacks"))
	fl := files.New(*dataDir)
	px := proxy.New(cmds, st, *dataDir, os.Getenv("ISLET_PROXY_PORTS"))
	px.Keys = keys
	cat := catalog.New(dk, px, filepath.Join(*dataDir, "stacks"))
	dbs := db.New(cmds, dk, cat, *dataDir)
	bus := notify.New(st, keys, log)
	go bus.Run(ctx)
	gh := github.New(st, keys)
	dep := deploy.New(st, keys, cmds, dk, px, bus, *dataDir, log)
	dep.CloneAuth = gh.CloneURL
	if err := dep.Start(ctx); err != nil {
		return fmt.Errorf("deploy: %w", err)
	}
	rn := runner.New(st, keys, cmds, bus, log)
	rn.RegToken = gh.RunnerToken
	rn.AppConfigured = gh.Configured
	rn.Start(ctx)
	bk := backup.New(st, keys, cmds, dbs, bus, *dataDir, log)
	bk.Start(ctx)
	prov := provider.New(st, keys)
	sec := security.New(st, cmds, bus, *dataDir, security.Hooks{
		BeforeRisky: func(ctx context.Context, op string) string {
			desc, err := prov.Snapshot(ctx, "system", op)
			if err != nil {
				if errors.Is(err, provider.ErrNotConfigured) {
					return ""
				}
				return "provider snapshot failed: " + err.Error()
			}
			return "provider snapshot " + desc + " requested before " + op
		},
		ProxyPorts:    px.Ports,
		PanelRouted:   px.PanelRouted,
		PanelPort:     func() string { p, _ := panelPort.Load().(string); return p },
		Admin2FA:      as.AllAdminsHave2FA,
		PanelHasCert:  func() bool { return *tlsMode == "off" || panelHasTrustedCert(ctx, px) },
		HasBackupPlan: bk.HasPlan,
	}, log)
	sec.StartSchedules(ctx)
	up := uptime.New(st, bus)
	if err := up.Start(ctx); err != nil {
		return fmt.Errorf("uptime: %w", err)
	}
	cr := cron.New(st, bus, *dataDir, log)
	if err := cr.Start(ctx); err != nil {
		return fmt.Errorf("cron: %w", err)
	}
	go watch.Docker(ctx, cmds, bus, log)
	go watch.Resources(ctx, sampler, bus)
	go watch.Daily(ctx, px, bus, log)
	go func() {
		for {
			bus.Purge(ctx, 30*24*time.Hour)
			cr.Purge(ctx, 30*24*time.Hour)
			select {
			case <-ctx.Done():
				return
			case <-time.After(12 * time.Hour):
			}
		}
	}()
	go func() {
		for {
			fl.PurgeOlderThan(7 * 24 * time.Hour)
			select {
			case <-ctx.Done():
				return
			case <-time.After(6 * time.Hour):
			}
		}
	}()
	if dst := dk.Status(ctx); dst.Available {
		log.Info("docker available", "version", dst.Version, "compose", dst.ComposeVersion)
	} else {
		log.Warn("docker not available", "err", dst.Error)
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           api.New(api.Deps{Store: st, Keys: keys, Auth: as, Metrics: collector, Sampler: sampler, Docker: dk, Files: fl, Runner: cmds, Proxy: px, Catalog: cat, Notify: bus, Cron: cr, DB: dbs, Uptime: up, Deploy: dep, Runners: rn, Security: sec, Provider: prov, Backup: bk, GitHub: gh, UI: web.Handler(), Log: log}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       0, // streams (deploys, logs) outlive any fixed read deadline; headers are still bounded
		WriteTimeout:      0, // streaming endpoints (logs, terminal) manage their own deadlines
		IdleTimeout:       120 * time.Second,
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", *listen, err)
	}
	scheme := "http"
	if _, port, err := net.SplitHostPort(ln.Addr().String()); err == nil {
		px.SetPanelURL(map[bool]string{true: "https", false: "http"}[*tlsMode != "off"], port)
		panelPort.Store(port)
	}
	if err := px.Reconcile(ctx); err != nil {
		log.Warn("proxy reconcile failed", "err", err)
	}
	if *tlsMode != "off" {
		cert, names, err := tlsutil.LoadOrCreate(*dataDir, st.Hostname)
		if err != nil {
			return fmt.Errorf("tls: %w", err)
		}
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		scheme = "https"
		fp, _ := tlsutil.Fingerprint(cert)
		log.Info("tls: self-signed certificate", "names", strings.Join(names, ","), "sha256", fp)
	} else {
		log.Warn("tls is off: cookies and passwords travel in clear text; use only on localhost")
	}
	bus.SetPanelURL(scheme + "://" + displayAddr(ln.Addr().String()))
	bus.Emit(ctx, notify.Event{Category: "system", Severity: notify.Info, Title: "Islet started", Message: "isletd " + version.Version + " is up.", Link: "/"})
	log.Info("isletd started", "version", version.Version, "listen", scheme+"://"+ln.Addr().String(), "data", *dataDir, "server", st.ServerID)
	if needs, _ := as.NeedsSetup(ctx); needs {
		if tok, err := as.SetupToken(); err == nil {
			log.Info("no admin yet: open the setup link to create one", "url", scheme+"://"+displayAddr(ln.Addr().String())+"/setup?token="+tok)
		}
	}

	errc := make(chan error, 1)
	go func() {
		if srv.TLSConfig != nil {
			errc <- srv.ServeTLS(ln, "", "")
		} else {
			errc <- srv.Serve(ln)
		}
	}()
	// Root-only Unix socket for the local CLI: no token, no TLS, same handler.
	if sockPath := socketPath(*dataDir); sockPath != "" {
		_ = os.Remove(sockPath)
		if uln, err := net.Listen("unix", sockPath); err == nil {
			_ = os.Chmod(sockPath, 0o600)
			usrv := &http.Server{Handler: srv.Handler, ReadHeaderTimeout: 10 * time.Second, ConnContext: func(ctx context.Context, c net.Conn) context.Context { return api.LocalConn(ctx) }}
			go func() { _ = usrv.Serve(uln) }()
			defer func() { _ = usrv.Close(); _ = os.Remove(sockPath) }()
			log.Info("local socket ready", "path", sockPath)
		} else {
			log.Warn("local socket unavailable", "err", err)
		}
	}

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = st.Audit(shutdownCtx, "system", "daemon.stop", "", "")
	return srv.Shutdown(shutdownCtx)
}

// displayAddr turns a listen address into something a person can open.
// panelHasTrustedCert is true when a domain routes to the panel with Let's Encrypt.
func panelHasTrustedCert(ctx context.Context, px *proxy.Manager) bool {
	doms, err := px.Domains(ctx)
	if err != nil {
		return false
	}
	for _, d := range doms {
		if d.TargetType == "panel" && d.TLS == "letsencrypt" && d.Enabled {
			return true
		}
	}
	return false
}

// socketPath is where the local CLI socket lives: /run/islet/isletd.sock on
// Linux, <data>/isletd.sock elsewhere, or ISLET_SOCKET; empty disables it.
func socketPath(dataDir string) string {
	if v, ok := os.LookupEnv("ISLET_SOCKET"); ok {
		return v
	}
	if runtime.GOOS == "linux" {
		if err := os.MkdirAll("/run/islet", 0o700); err == nil {
			return "/run/islet/isletd.sock"
		}
	}
	abs, _ := filepath.Abs(dataDir)
	return filepath.Join(abs, "isletd.sock")
}

func displayAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "<server-ip>"
	}
	return net.JoinHostPort(host, port)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func defaultDataDir() string {
	if runtime.GOOS == "linux" {
		return "/var/lib/islet"
	}
	return ".data"
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
