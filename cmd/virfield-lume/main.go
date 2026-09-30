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

	"github.com/mallexxx/virfield/internal/logging"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	binary := flag.String("binary", "", "absolute path to the patched Lume binary (required)")
	path := flag.String("log", "", "private bounded log path")
	port := flag.Int("port", 7777, "loopback Lume API port")
	flag.Parse()
	if !filepath.IsAbs(*binary) || !filepath.IsAbs(*path) || *port < 1 || *port > 65535 {
		return fmt.Errorf("absolute -binary and -log paths and a port between 1 and 65535 are required")
	}
	w, err := logging.Open(*path, 10<<20)
	if err != nil {
		return err
	}
	defer w.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd := exec.CommandContext(ctx, *binary, "serve", "--port", strconv.Itoa(*port))
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
