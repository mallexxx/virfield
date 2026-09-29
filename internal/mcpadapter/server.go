// Package mcpadapter exposes API operations using the official MCP Go SDK.
// There is deliberately no import of store, control, lume, SSH or os/exec.
package mcpadapter

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/mallexxx/virfield/internal/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type empty struct{}
type acquireArgs struct {
	SSHPublicKey string `json:"ssh_public_key" jsonschema:"Fresh Ed25519 public key for this lease. Never submit a private key."`
	Template     string `json:"template"`
	TTLSeconds   int    `json:"ttl_seconds"`
	Key          string `json:"idempotency_key"`
}
type idArgs struct {
	ID string `json:"id"`
}
type releaseArgs struct {
	ID  string `json:"id"`
	Key string `json:"idempotency_key"`
}
type renewArgs struct {
	ID        string `json:"id"`
	ExpiresAt string `json:"expires_at"`
}
type eventsArgs struct {
	After   int64  `json:"after,omitempty"`
	LeaseID string `json:"lease_id,omitempty"`
}

func result(b json.RawMessage, err error) (*mcp.CallToolResult, any, error) {
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
}
func New(c *client.Client) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "virfield", Version: "2.0.0-dev"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "virfield_status", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, Description: "Show Lume health, VM slot usage, active leases and durable jobs"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "GET", "status", nil, ""))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "vm_acquire", Annotations: &mcp.ToolAnnotations{IdempotentHint: true}, Description: "Reserve one of at most two VM slots and asynchronously clone/start an allowlisted golden image. Supply a fresh per-lease Ed25519 public key; keep its private key local. Use the SAME idempotency key on retries. Returns capacity_exhausted when full."}, func(ctx context.Context, _ *mcp.CallToolRequest, a acquireArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "POST", "leases", map[string]any{"template": a.Template, "ttl_seconds": a.TTLSeconds, "ssh_public_key": a.SSHPublicKey}, a.Key))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "vm_lease", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, Description: "Read a durable lease, readiness and error"}, func(ctx context.Context, _ *mcp.CallToolRequest, a idArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "GET", "leases/"+url.PathEscape(a.ID), nil, ""))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "vm_release", Annotations: &mcp.ToolAnnotations{IdempotentHint: true}, Description: "Stop and permanently delete ONLY the VM owned by this lease, after exporting artifacts. Requires user authorization. Retry with the same idempotency key."}, func(ctx context.Context, _ *mcp.CallToolRequest, a releaseArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "POST", "leases/"+url.PathEscape(a.ID)+"/release", nil, a.Key))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "vm_renew", Annotations: &mcp.ToolAnnotations{IdempotentHint: true}, Description: "Extend an active lease to an absolute RFC3339 deadline, at most 24 hours ahead"}, func(ctx context.Context, _ *mcp.CallToolRequest, a renewArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "PUT", "leases/"+url.PathEscape(a.ID)+"/expiry", map[string]string{"expires_at": a.ExpiresAt}, ""))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "virfield_job", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, Description: "Read a durable job and diagnostic state"}, func(ctx context.Context, _ *mcp.CallToolRequest, a idArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "GET", "jobs/"+url.PathEscape(a.ID), nil, ""))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "virfield_events", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, Description: "Read ordered durable events after a cursor; persist next_cursor after processing"}, func(ctx context.Context, _ *mcp.CallToolRequest, a eventsArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "GET", "events?after="+strconv.FormatInt(a.After, 10)+"&lease_id="+url.QueryEscape(a.LeaseID), nil, ""))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "image_build", Annotations: &mcp.ToolAnnotations{IdempotentHint: true}, Description: "Build a NEW allowlisted golden image from its pinned Apple IPSW profile. Downloads and installs macOS, completes setup, applies the operator-configured guest SIP policy and verifies credentials. Requires no active leases. Never overwrites an existing VM. Use the same idempotency key on retries."}, func(ctx context.Context, _ *mcp.CallToolRequest, a releaseArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "POST", "images/"+url.PathEscape(a.ID)+"/build", empty{}, a.Key))
	})
	return s
}
