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
	"syscall"
	"time"

	"github.com/isletdev/islet/internal/api"
	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/docker"
	"github.com/isletdev/islet/internal/metrics"
	"github.com/isletdev/islet/internal/store"
	"github.com/isletdev/islet/internal/tlsutil"
	"github.com/isletdev/islet/internal/version"
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

	collector := metrics.NewCollector()
	sampler := metrics.NewSampler(collector, st, log)
	go sampler.Run(ctx)
	runner := cmdrun.New(st, log)
	dk := docker.New(runner, filepath.Join(*dataDir, "stacks"))
	if dst := dk.Status(ctx); dst.Available {
		log.Info("docker available", "version", dst.Version, "compose", dst.ComposeVersion)
	} else {
		log.Warn("docker not available", "err", dst.Error)
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           api.New(api.Deps{Store: st, Auth: as, Metrics: collector, Sampler: sampler, Docker: dk, UI: web.Handler(), Log: log}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      0, // streaming endpoints (logs, terminal) manage their own deadlines
		IdleTimeout:       120 * time.Second,
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", *listen, err)
	}
	scheme := "http"
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
