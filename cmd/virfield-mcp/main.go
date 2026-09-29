// virfield-mcp is a stdio adapter. It always calls virfieldd, never Lume.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mallexxx/virfield/internal/client"
	"github.com/mallexxx/virfield/internal/config"
	"github.com/mallexxx/virfield/internal/mcpadapter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	base := flag.String("url", "http://127.0.0.1:7780", "daemon URL")
	tokenPath := flag.String("token-file", "", "API token file")
	flag.Parse()
	token, err := config.Token(*tokenPath)
	if err != nil {
		return err
	}
	c, err := client.New(*base, token)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return mcpadapter.New(c).Run(ctx, &mcp.StdioTransport{})
}
