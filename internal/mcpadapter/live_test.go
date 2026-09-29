package mcpadapter

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	c.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(c)
}
func TestLiveInstalledMCP(t *testing.T) {
	if os.Getenv("VIRFIELD_LIVE_MCP") != "1" {
		t.Skip("requires installed v2")
	}
	token, err := os.ReadFile(os.Getenv("VIRFIELD_LIVE_TOKEN_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, transport := range []mcp.Transport{&mcp.StreamableClientTransport{Endpoint: "http://127.0.0.1:7780/mcp", HTTPClient: &http.Client{Transport: bearerTransport{strings.TrimSpace(string(token))}, Timeout: 10 * time.Second}}, &mcp.CommandTransport{Command: exec.Command(os.Getenv("VIRFIELD_LIVE_MCP_BINARY"), "-token-file", os.Getenv("VIRFIELD_LIVE_TOKEN_FILE"))}} {
		session, err := mcp.NewClient(&mcp.Implementation{Name: "virfield-live-check", Version: "1"}, nil).Connect(ctx, transport, nil)
		if err != nil {
			t.Fatal(err)
		}
		list, err := session.ListTools(ctx, nil)
		if err != nil || len(list.Tools) != 10 {
			session.Close()
			t.Fatal("MCP tool discovery failed", err)
		}
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "virfield_status", Arguments: map[string]any{}})
		if err != nil || result.IsError {
			session.Close()
			t.Fatal("MCP status failed", err)
		}
		session.Close()
	}
	resp, err := http.Post("http://127.0.0.1:7780/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("unauthenticated MCP accepted", resp.StatusCode)
	}
	t.Log("HTTP and stdio discovery/status passed; anonymous MCP rejected")
}
