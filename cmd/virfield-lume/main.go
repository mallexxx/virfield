// virfield-lume is the independent launchd entrypoint for Lume with bounded logs.
// It never manages VM state or retries a child; launchd owns service restart.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mallexxx/virfield/internal/config"
	"github.com/mallexxx/virfield/internal/logging"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	binary := flag.String("binary", "", "absolute path to the patched Lume binary")
	configPath := flag.String("config", "", "absolute path to Virfield config.json; used when -binary is empty")
	path := flag.String("log", "", "private bounded log path")
	port := flag.Int("port", 7777, "loopback Lume API port")
	flag.Parse()
	resolvedConfig := *configPath
	if resolvedConfig == "" {
		if executable, executableErr := os.Executable(); executableErr == nil {
			candidate := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "config.json"))
			if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
				resolvedConfig = candidate
			}
		}
	}
	resolved, err := resolveBinary(*binary, resolvedConfig)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(*path) || *port < 1 || *port > 65535 {
		return fmt.Errorf("absolute -log path and a port between 1 and 65535 are required")
	}
	w, err := logging.Open(*path, 10<<20)
	if err != nil {
		return err
	}
	defer w.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if resolvedConfig != "" {
		cfg, err := config.Load(resolvedConfig)
		if err != nil {
			return err
		}
		syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = syncStorageLocations(syncCtx, resolved, cfg.StoragePaths)
		cancel()
		if err != nil {
			return err
		}
	}
	cmd := exec.CommandContext(ctx, resolved, "serve", "--port", strconv.Itoa(*port))
	cmd.Stdout = w
	cmd.Stderr = w
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 15 * time.Second
	return cmd.Run()
}

func syncStorageLocations(ctx context.Context, binary string, configured map[string]string) error {
	if len(configured) == 0 {
		return nil
	}
	out, err := exec.CommandContext(ctx, binary, "config", "storage", "list").CombinedOutput()
	if err != nil {
		return fmt.Errorf("list Lume storage locations: %w", err)
	}
	existing := parseStorageLocations(out)
	names := make([]string, 0, len(configured))
	for name := range configured {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Clean(configured[name])
		if current, ok := existing[name]; ok {
			if current != path {
				return fmt.Errorf("Lume storage %s uses %s, config requires %s", name, current, path)
			}
			continue
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return fmt.Errorf("inspect storage path %s: %w", path, statErr)
		}
		if !info.IsDir() {
			return fmt.Errorf("storage path %s is not a directory", path)
		}
		if output, addErr := exec.CommandContext(ctx, binary, "config", "storage", "add", name, path).CombinedOutput(); addErr != nil {
			return fmt.Errorf("register Lume storage %s: %w: %s", name, addErr, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func parseStorageLocations(output []byte) map[string]string {
	locations := map[string]string{}
	home, _ := os.UserHomeDir()
	for _, raw := range bytes.Split(output, []byte{'\n'}) {
		line := strings.TrimSpace(string(raw))
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(line, "- "), ": ", 2)
		if len(parts) != 2 {
			continue
		}
		path := strings.TrimSuffix(parts[1], " (default)")
		if path == "~" {
			path = home
		} else if strings.HasPrefix(path, "~/") {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
		locations[parts[0]] = filepath.Clean(path)
	}
	return locations
}

func resolveBinary(binary, configPath string) (string, error) {
	if binary != "" {
		if !filepath.IsAbs(binary) {
			return "", fmt.Errorf("absolute -binary path is required")
		}
		return binary, nil
	}
	if !filepath.IsAbs(configPath) {
		return "", fmt.Errorf("absolute -config path is required when -binary is empty")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return "", err
	}
	if cfg.ImageTools == nil || !filepath.IsAbs(cfg.ImageTools.Lume) {
		return "", fmt.Errorf("config image_tools.lume must be an absolute path")
	}
	return cfg.ImageTools.Lume, nil
}
