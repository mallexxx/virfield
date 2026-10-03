// virfield-lume is the independent launchd entrypoint for Lume with bounded logs.
// It never manages VM state or retries a child; launchd owns service restart.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
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
	resolved, err := resolveBinary(*binary, *configPath)
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
