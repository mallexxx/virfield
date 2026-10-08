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
	"github.com/mallexxx/virfield/internal/baremetal"
	"github.com/mallexxx/virfield/internal/catalog"
	"github.com/mallexxx/virfield/internal/client"
	"github.com/mallexxx/virfield/internal/config"
	"github.com/mallexxx/virfield/internal/control"
	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/fleet"
	"github.com/mallexxx/virfield/internal/guestssh"
	"github.com/mallexxx/virfield/internal/hostlock"
	"github.com/mallexxx/virfield/internal/hostresources"
	"github.com/mallexxx/virfield/internal/httpapi"
	"github.com/mallexxx/virfield/internal/images"
	"github.com/mallexxx/virfield/internal/logging"
	"github.com/mallexxx/virfield/internal/lume"
	"github.com/mallexxx/virfield/internal/mcpadapter"
	"github.com/mallexxx/virfield/internal/orchard"
	"github.com/mallexxx/virfield/internal/provider"
	"github.com/mallexxx/virfield/internal/registry"
	"github.com/mallexxx/virfield/internal/store"
	"github.com/mallexxx/virfield/internal/tart"
	"github.com/mallexxx/virfield/internal/tartimage"
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
	var backend provider.Runtime
	var lumeBackend *lume.Client
	var tartBackend *tart.Client
	var tartCredentials []tart.RegistryCredential
	switch {
	case cfg.Runtime == nil:
		lumeBackend, err = lume.New(cfg.LumeURL)
		backend = lumeBackend
	case cfg.Runtime.Provider == "tart":
		containers := make([]tart.ContainerConfig, 0, len(cfg.Runtime.Tart.Containers))
		for _, container := range cfg.Runtime.Tart.Containers {
			containers = append(containers, tart.ContainerConfig{ID: container.ID, Path: container.Path, Kind: container.Kind, VolumeUUID: container.VolumeUUID})
		}
		credentials := make([]tart.RegistryCredential, 0, len(cfg.Runtime.Tart.RegistryCredentials))
		for _, credential := range cfg.Runtime.Tart.RegistryCredentials {
			registryToken, tokenErr := config.Token(credential.TokenFile)
			if tokenErr != nil {
				return tokenErr
			}
			credentials = append(credentials, tart.RegistryCredential{Host: credential.Host, Username: credential.Username, Token: registryToken})
		}
		tartClient, tartErr := tart.New(tart.Config{Binary: cfg.Runtime.Tart.Binary, HostID: cfg.Runtime.Tart.HostID, HostName: cfg.Runtime.Tart.HostName, MaxVMs: cfg.Runtime.Tart.MaxVMs, CPULimit: cfg.Runtime.Tart.CPULimit, MemoryReserveBytes: cfg.Runtime.Tart.MemoryReserveBytes, Containers: containers, RegistryCredentials: credentials})
		backend, err = tartClient, tartErr
		tartBackend, tartCredentials = tartClient, credentials
	case cfg.Runtime.Provider == "orchard":
		orchardToken, tokenErr := config.Token(cfg.Runtime.Orchard.TokenFile)
		if tokenErr != nil {
			return tokenErr
		}
		hostLimits := make(map[string]orchard.HostLimit, len(cfg.Runtime.Orchard.HostLimits))
		for hostID, limit := range cfg.Runtime.Orchard.HostLimits {
			converted := orchard.HostLimit{MaxVMs: limit.MaxVMs, MemoryReserveBytes: limit.MemoryReserveBytes}
			if limit.WakeOnLAN != nil {
				converted.WakeOnLAN = &orchard.WakeOnLAN{MACAddress: limit.WakeOnLAN.MACAddress, BroadcastAddress: limit.WakeOnLAN.BroadcastAddress}
			}
			hostLimits[hostID] = converted
		}
		backend, err = orchard.New(orchard.Config{URL: cfg.Runtime.Orchard.URL, ServiceAccount: cfg.Runtime.Orchard.ServiceAccount, Token: orchardToken, PoolID: cfg.Runtime.Orchard.PoolID, WorkerLabels: cfg.Runtime.Orchard.WorkerLabels, HostLimits: hostLimits, MaxVMs: cfg.Runtime.Orchard.MaxVMs, WorkerOfflineAfterSeconds: cfg.Runtime.Orchard.WorkerOfflineAfterSeconds})
	}
	if err != nil {
		return err
	}
	// Physical nodes join the primary runtime through a fleet; their leases
	// use node-side SSH isolation instead of the VM credential rotation.
	var nodes *baremetal.Runtime
	if cfg.Runtime != nil && len(cfg.Runtime.Nodes) > 0 {
		signer, _, keyErr := baremetal.LoadOrCreateKey(filepath.Join(cfg.StateDir, "nodes", "management"))
		if keyErr != nil {
			return keyErr
		}
		configured := make([]baremetal.Node, 0, len(cfg.Runtime.Nodes))
		for _, n := range cfg.Runtime.Nodes {
			node := baremetal.Node{ID: n.ID, Name: n.Name, Address: n.Address, User: n.User, HostKey: n.HostKey, OS: n.OS, Arch: n.Arch, KeepAwakeFile: n.KeepAwakeFile}
			if n.WakeOnLAN != nil {
				node.WakeMAC, node.WakeBroadcast = n.WakeOnLAN.MAC, n.WakeOnLAN.Broadcast
			}
			configured = append(configured, node)
		}
		nodes = baremetal.New(configured, baremetal.SSHDialer(signer))
		backend = fleet.New(backend, nodes)
	}
	manager, err := control.New(db, backend, cfg.Templates, cfg.MaxVMs, log)
	if err != nil {
		return err
	}
	remoteRuntime := cfg.Runtime != nil && cfg.Runtime.Provider == "orchard"
	if cfg.ResourceLimits == nil && cfg.Runtime != nil && cfg.Runtime.Provider == "tart" {
		memory, err := hostresources.Memory()
		if err != nil {
			return err
		}
		cpu := cfg.Runtime.Tart.CPULimit
		if cpu == 0 {
			cpu = runtime.NumCPU()
		}
		if memory <= cfg.Runtime.Tart.MemoryReserveBytes {
			return errors.New("tart memory reserve leaves no allocatable RAM")
		}
		cfg.ResourceLimits = &domain.ResourceLimits{CPU: cpu, MemoryBytes: memory - cfg.Runtime.Tart.MemoryReserveBytes, DiskReserveBytes: cfg.Runtime.Tart.DiskReserveBytes}
	} else if cfg.ResourceLimits == nil && !remoteRuntime {
		memory, err := hostresources.Memory()
		if err != nil {
			return err
		}
		cfg.ResourceLimits = &domain.ResourceLimits{CPU: max(1, runtime.NumCPU()*3/4), MemoryBytes: memory * 3 / 4, DiskReserveBytes: 20 << 30}
	}
	if cfg.StoragePaths == nil && cfg.Runtime == nil {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		cfg.StoragePaths = map[string]string{"home": filepath.Join(home, ".lume")}
	}
	if lumeBackend != nil {
		lumeBackend.StoragePaths = cfg.StoragePaths
	}
	cloneLocations := cfg.StoragePaths
	if remoteRuntime {
		cloneLocations = map[string]string{cfg.Runtime.Orchard.PoolID: "remote"}
	}
	if err := manager.SetCloneLocations(cloneLocations, cfg.DefaultCloneLocation, cfg.FallbackCloneLocations); err != nil {
		return err
	}
	if cfg.ResourceLimits != nil && !remoteRuntime {
		manager.SetResources(*cfg.ResourceLimits, func() (map[string]int64, error) { return hostresources.Disk(cfg.StoragePaths) })
	}
	// Every local runtime hands out leases only after per-lease credential
	// isolation; Orchard leases are isolated by the Orchard worker path later.
	if !remoteRuntime {
		var preparer control.LeasePreparer = &guestssh.Manager{Dir: cfg.StateDir}
		if nodes != nil {
			preparer = fleet.NewPreparer(preparer, map[string]fleet.Preparer{nodes.ProviderName(): nodes})
		}
		manager.SetLeasePreparer(preparer)
	}
	registryClient, err := registry.New(cfg.Registries)
	if err != nil {
		return err
	}
	manager.SetRegistry(registryClient)
	var appleService *appleauth.Service
	if cfg.ImageTools != nil {
		if lumeBackend == nil {
			return errors.New("legacy image_tools are only supported by the lume runtime; Tart uses digest-pinned OCI images")
		}
		builder, err := images.New(cfg.StateDir, lumeBackend, *cfg.ImageTools)
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
	if tartBackend != nil && cfg.Runtime.Tart.Image != nil {
		homes := make(map[string]string, len(cfg.Runtime.Tart.Containers))
		for _, container := range cfg.Runtime.Tart.Containers {
			homes[container.ID] = container.Path
		}
		manager.SetImageBuilder(tartimage.New(tartimage.Config{
			StateDir:      cfg.StateDir,
			Packer:        cfg.Runtime.Tart.Image.Packer,
			TartBinary:    cfg.Runtime.Tart.Binary,
			XcodeArchives: cfg.Runtime.Tart.Image.XcodeArchives,
			Homes:         homes,
			Credentials:   tartCredentials,
			Runtime:       tartBackend,
		}))
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
		case <-time.After(10*time.Minute + 10*time.Second):
			log.Error("controller shutdown exceeded mutation drain deadline")
			if runErr == nil {
				runErr = errors.New("controller shutdown timed out")
			}
		}
	}
	return runErr
}
