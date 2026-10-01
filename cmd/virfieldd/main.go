// virfieldd owns host VM lifecycle state and serves the v2 API.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/mallexxx/virfield/internal/catalog"
	"github.com/mallexxx/virfield/internal/client"
	"github.com/mallexxx/virfield/internal/config"
	"github.com/mallexxx/virfield/internal/control"
	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
	"github.com/mallexxx/virfield/internal/hostlock"
	"github.com/mallexxx/virfield/internal/hostresources"
	"github.com/mallexxx/virfield/internal/httpapi"
	"github.com/mallexxx/virfield/internal/images"
	"github.com/mallexxx/virfield/internal/logging"
	"github.com/mallexxx/virfield/internal/lume"
	"github.com/mallexxx/virfield/internal/mcpadapter"
	"github.com/mallexxx/virfield/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(); err != nil {
		log.Error("virfieldd stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
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
	writer, err := logging.Open(filepath.Join(cfg.StateDir, "daemon.jsonl"), 10<<20)
	if err != nil {
		return err
	}
	defer writer.Close()
	log := slog.New(slog.NewJSONHandler(writer, nil))
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
	if cfg.ResourceLimits == nil {
		memory, err := hostresources.Memory()
		if err != nil {
			return err
		}
		cfg.ResourceLimits = &domain.ResourceLimits{CPU: max(1, runtime.NumCPU()*3/4), MemoryBytes: memory * 3 / 4, DiskReserveBytes: 20 << 30}
	}
	if cfg.StoragePaths == nil {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		cfg.StoragePaths = map[string]string{"home": filepath.Join(home, ".lume")}
	}
	manager.SetResources(*cfg.ResourceLimits, func() (map[string]int64, error) { return hostresources.Disk(cfg.StoragePaths) })
	manager.SetLeasePreparer(&guestssh.Manager{Dir: cfg.StateDir})
	if cfg.ImageTools != nil {
		builder, err := images.New(cfg.StateDir, backend, *cfg.ImageTools)
		if err != nil {
			return err
		}
		manager.SetImageBuilder(builder)
		locations := []string{}
		for name := range cfg.StoragePaths {
			locations = append(locations, name)
		}
		manager.SetImageCatalog(catalog.New(), locations)
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
	apiClient, err := client.New("http://"+cfg.Listen, token)
	if err != nil {
		return err
	}
	mcpServer := mcpadapter.New(apiClient)
	mcpHTTP := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	backupHTTP := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		configPath, err := filepath.Abs(*path)
		if err != nil {
			http.Error(w, "Cannot resolve configuration path", http.StatusInternalServerError)
			return
		}
		id, err := manager.Backup(r.Context(), cfg.StateDir, configPath, cfg.TokenFile)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(409)
			json.NewEncoder(w).Encode(map[string]any{"error": domain.Err("backup_failed", "Backup requires idle manager, resolved jobs and writable private storage")})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"backup_id": id})
	})
	server := &http.Server{Handler: httpapi.New(manager, token, log, httpapi.Options{MCP: mcpHTTP, Backup: backupHTTP}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
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
