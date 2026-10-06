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

	"github.com/mallexxx/virfield/internal/appleauth"
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
	"github.com/mallexxx/virfield/internal/registry"
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

func loadPrincipals(configured []config.Principal) ([]httpapi.Principal, error) {
	out := make([]httpapi.Principal, 0, len(configured))
	for _, p := range configured {
		token, err := config.Token(p.TokenFile)
		if err != nil {
			return nil, err
		}
		out = append(out, httpapi.Principal{Name: p.Name, Token: token, Scopes: p.Scopes})
	}
	return out, nil
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
	principals, err := loadPrincipals(cfg.Principals)
	if err != nil {
		return err
	}
	authSet, err := httpapi.NewAuthSet(token, principals)
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
	hostLock, err := hostlock.AcquireHost()
	if err != nil {
		return err
	}
	defer hostLock.Close()
	writer, err := logging.Open(filepath.Join(cfg.StateDir, "daemon.jsonl"), 10<<20)
	if err != nil {
		return err
	}
	defer writer.Close()
	log := slog.New(slog.NewJSONHandler(writer, nil))
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
	backend.StoragePaths = cfg.StoragePaths
	if err := manager.SetCloneLocations(cfg.StoragePaths, cfg.DefaultCloneLocation, cfg.FallbackCloneLocations); err != nil {
		return err
	}
	manager.SetResources(*cfg.ResourceLimits, func() (map[string]int64, error) { return hostresources.Disk(cfg.StoragePaths) })
	manager.SetLeasePreparer(&guestssh.Manager{Dir: cfg.StateDir})
	registryClient, err := registry.New(cfg.Registries)
	if err != nil {
		return err
	}
	manager.SetRegistry(registryClient)
	var appleService *appleauth.Service
	if cfg.ImageTools != nil {
		builder, err := images.New(cfg.StateDir, backend, *cfg.ImageTools)
		if err != nil {
			return err
		}
		builder.Registry = registryClient
		builder.StoragePaths = cfg.StoragePaths
		builder.AuthCookiePath = filepath.Join(cfg.StateDir, "apple-auth", "cookies.txt")
		if executable, execErr := os.Executable(); execErr == nil {
			if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
				executable = resolved
			}
			browser := filepath.Join(filepath.Dir(executable), "VirfieldAppleBrowser.app", "Contents", "MacOS", "VirfieldAppleBrowser")
			if st, statErr := os.Lstat(browser); statErr == nil && st.Mode().IsRegular() && st.Mode().Perm()&0111 != 0 {
				appleService, err = appleauth.New("", builder.AuthCookiePath, manager)
				if err != nil {
					return err
				}
			} else if statErr != nil && !os.IsNotExist(statErr) {
				return statErr
			} else {
				log.Warn("Apple browser sign-in unavailable; install VirfieldAppleBrowser.app beside virfieldd")
			}
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
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-hup:
				freshToken, tokenErr := config.Token(cfg.TokenFile)
				freshPrincipals, principalsErr := loadPrincipals(cfg.Principals)
				if tokenErr != nil || principalsErr != nil {
					log.Error("token reload failed", "owner_error", tokenErr, "principal_error", principalsErr)
					continue
				}
				if err := authSet.Replace(freshToken, freshPrincipals); err != nil {
					log.Error("token reload rejected", "error", err)
					continue
				}
				apiClient.SetToken(freshToken)
				log.Info("API principal tokens reloaded")
			}
		}
	}()
	mcpServer := mcpadapter.New(apiClient)
	// The shared HTTP handler enforces the explicit Host allowlist before MCP.
	mcpHTTP := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	backupHTTP := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		configPath, err := filepath.Abs(*path)
		if err != nil {
			http.Error(w, "Cannot resolve configuration path", http.StatusInternalServerError)
			return
		}
		id, err := manager.Backup(r.Context(), cfg.StateDir, configPath, cfg.TokenFile)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			log.Error("backup failed", "error", err)
			w.WriteHeader(409)
			json.NewEncoder(w).Encode(map[string]any{"error": domain.Err("backup_failed", "Backup requires idle manager, resolved jobs and writable private storage")})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"backup_id": id})
	})
	server := &http.Server{Handler: httpapi.New(manager, token, log, httpapi.Options{Auth: authSet, AllowedHosts: append([]string{cfg.Listen}, cfg.AllowedHosts...), MCP: mcpHTTP, Backup: backupHTTP, AppleAuth: appleService, Origin: "http://" + cfg.Listen}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 75 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
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
		select {
		case err := <-controlDone:
			if runErr == nil {
				runErr = err
			}
		case <-time.After(2*time.Minute + 10*time.Second):
			log.Error("controller shutdown exceeded mutation drain deadline")
			if runErr == nil {
				runErr = errors.New("controller shutdown timed out")
			}
		}
	}
	return runErr
}
