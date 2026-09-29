// virfieldd owns host VM lifecycle state and serves the v2 API.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mallexxx/virfield/internal/config"
	"github.com/mallexxx/virfield/internal/control"
	"github.com/mallexxx/virfield/internal/hostlock"
	"github.com/mallexxx/virfield/internal/httpapi"
	"github.com/mallexxx/virfield/internal/lume"
	"github.com/mallexxx/virfield/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("virfieldd stopped", "error", err)
		os.Exit(1)
	}
}
func run(log *slog.Logger) error {
	path := flag.String("config", "", "absolute path to v2 JSON config")
	flag.Parse()
	if *path == "" {
		return errors.New("pass -config /absolute/path/config.json")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	token, err := config.Token(cfg.TokenFile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.StateDir, 0700); err != nil {
		return err
	}
	st, err := os.Stat(cfg.StateDir)
	if err != nil {
		return err
	}
	if st.Mode().Perm()&0077 != 0 {
		return errors.New("state directory must be private (chmod 700)")
	}
	lock, err := hostlock.Acquire()
	if err != nil {
		return err
	}
	defer lock.Close()
	db, err := store.Open(filepath.Join(cfg.StateDir, "state.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	backend, err := lume.New(cfg.LumeURL)
	if err != nil {
		return err
	}
	manager, err := control.New(db, backend, cfg.Templates, cfg.MaxVMs, log)
	if err != nil {
		return err
	}
	// Bind before starting background effects: a conflicting port must not leave
	// an invisible controller running beside the real service.
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := &http.Server{Handler: httpapi.New(manager, token, log), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	controlDone := make(chan error, 1)
	httpDone := make(chan error, 1)
	go func() { controlDone <- manager.Run(ctx) }()
	go func() { httpDone <- server.Serve(listener) }()
	log.Info("virfieldd ready", "listen", listener.Addr().String(), "max_vms", cfg.MaxVMs)
	var runErr error
	controlFinished := false
	select {
	case <-ctx.Done():
	case err := <-httpDone:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	case runErr = <-controlDone:
		controlFinished = true
	}
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if err := server.Shutdown(shutdown); err != nil {
		server.Close()
		if runErr == nil {
			runErr = err
		}
	}
	if !controlFinished {
		if err := <-controlDone; runErr == nil {
			runErr = err
		}
	}
	return runErr
}
