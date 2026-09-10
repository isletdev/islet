// Command isletd is the Islet daemon: one process that serves the panel,
// the API, and manages the server it runs on.
package main

import (
	"context"
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
	"syscall"
	"time"

	"github.com/isletdev/islet/internal/api"
	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/store"
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

	srv := &http.Server{
		Addr:              *listen,
		Handler:           api.New(st, as, web.Handler(), log),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      0, // streaming endpoints (logs, terminal) manage their own deadlines
		IdleTimeout:       120 * time.Second,
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", *listen, err)
	}
	log.Info("isletd started", "version", version.Version, "listen", ln.Addr().String(), "data", *dataDir, "server", st.ServerID)
	if needs, _ := as.NeedsSetup(ctx); needs {
		if tok, err := as.SetupToken(); err == nil {
			log.Info("no admin yet: open the setup link to create one", "url", "http://"+displayAddr(ln.Addr().String())+"/setup?token="+tok)
		}
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

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
