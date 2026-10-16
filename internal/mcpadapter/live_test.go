package mcpadapter

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/docs"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	c.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(c)
}

// A live process can retain old embedded docs after its binary is replaced.
// Tool names and a healthy backend alone cannot attest the installed client.
func verifyInstalledRunbook(ctx context.Context, session *mcp.ClientSession, version string) error {
	info := session.InitializeResult().ServerInfo
	if info == nil || info.Name != "virfield" || info.Version != version {
		return fmt.Errorf("MCP server version differs from expected %s; reconnect the client after installing the candidate", version)
	}
	for _, document := range helpDocuments {
		want, err := docs.Files.ReadFile(document.file)
		if err != nil {
			return err
		}
		help, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "virfield_help", Arguments: map[string]any{"topic": document.topic}})
		if err != nil {
			return err
		}
		if help.IsError || len(help.Content) != 1 {
			return fmt.Errorf("MCP %s runbook unavailable", document.topic)
		}
		content, ok := help.Content[0].(*mcp.TextContent)
		if !ok || content.Text != string(want) {
			return fmt.Errorf("MCP %s runbook differs from the checked source revision; reconnect the client after installing the candidate", document.topic)
		}
		uri := "virfield://docs/" + document.topic
		resource, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			return err
		}
		if len(resource.Contents) != 1 || resource.Contents[0].URI != uri || resource.Contents[0].Text != string(want) {
			return fmt.Errorf("MCP %s resource differs from the checked source revision", document.topic)
		}
	}
	return nil
}

func TestLiveInstalledMCP(t *testing.T) {
	if os.Getenv("VIRFIELD_LIVE_MCP") != "1" {
		t.Skip("requires installed v2")
	}
	token, err := os.ReadFile(os.Getenv("VIRFIELD_LIVE_TOKEN_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	version := os.Getenv("VIRFIELD_LIVE_MCP_VERSION")
	if version == "" {
		t.Fatal("set VIRFIELD_LIVE_MCP_VERSION to the exact installed package version and run from its source revision")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, transport := range []mcp.Transport{&mcp.StreamableClientTransport{Endpoint: "http://127.0.0.1:7780/mcp", HTTPClient: &http.Client{Transport: bearerTransport{strings.TrimSpace(string(token))}, Timeout: 10 * time.Second}}, &mcp.CommandTransport{Command: exec.Command(os.Getenv("VIRFIELD_LIVE_MCP_BINARY"), "-token-file", os.Getenv("VIRFIELD_LIVE_TOKEN_FILE"))}} {
		session, err := mcp.NewClient(&mcp.Implementation{Name: "virfield-live-check", Version: "1"}, nil).Connect(ctx, transport, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := verifyInstalledRunbook(ctx, session, version); err != nil {
			session.Close()
			t.Fatal(err)
		}
		list, err := session.ListTools(ctx, nil)
		if err != nil {
			session.Close()
			t.Fatal("MCP tool discovery failed", err)
		}
		available := map[string]bool{}
		for _, tool := range list.Tools {
			available[tool.Name] = true
		}
		if len(list.Tools) != 20 {
			t.Fatalf("Installed MCP exposes %d tools, expected20", len(list.Tools))
		}
		for _, name := range []string{"virfield_help", "virfield_status", "vm_acquire", "vm_lease", "vm_revalidate", "vm_release", "vm_renew", "virfield_job", "virfield_events", "image_catalog", "image_create", "image_apple_auth", "image_build", "image_transfer", "image_pull", "image_publish", "registry_sources", "registry_resolve", "vm_tunnel", "vm_tunnel_close"} {
			if !available[name] {
				session.Close()
				t.Fatalf("Required MCP tool missing: %s", name)
			}
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
	t.Log("HTTP and stdio exact-version discovery/runbooks/resources/status passed; anonymous MCP rejected")
}

func TestInstalledRunbookRejectsStaleClient(t *testing.T) {
	for _, staleVersion := range []bool{true, false} {
		t.Run(fmt.Sprintf("stale-version-%t", staleVersion), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			version := Version
			if staleVersion {
				version = "2.0.0"
			}
			server := mcp.NewServer(&mcp.Implementation{Name: "virfield", Version: version}, nil)
			mcp.AddTool(server, &mcp.Tool{Name: "virfield_help"}, func(context.Context, *mcp.CallToolRequest, helpArgs) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "outdated embedded runbook"}}}, nil, nil
			})
			st, ct := mcp.NewInMemoryTransports()
			ss, err := server.Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "fixture", Version: "1"}, nil).Connect(ctx, ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			if err := verifyInstalledRunbook(ctx, cs, Version); err == nil {
				t.Fatal("Stale installed client passed readiness")
			}
		})
	}
}
