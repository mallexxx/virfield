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

// Version is overridden by the release packager through Go linker flags.
var Version = "2.1.0-dev"

type empty struct{}
type createImageArgs struct {
	Security string `json:"security,omitempty" jsonschema:"Guest policy: default keeps protections; sip-disabled disables SIP; automation also configures Gatekeeper, AMFI and Terminal/SSH TCC grants. Applied only inside the VM and verified after reboot."`
	ID       string `json:"id" jsonschema:"New golden image ID and VM name"`
	MacOS    string `json:"macos" jsonschema:"macOS exact version, build or codename such as monterey; inspect image_catalog first"`
	Xcode    string `json:"xcode,omitempty" jsonschema:"Optional exact stable Xcode version, such as 13.4.1"`
	Location string `json:"location,omitempty" jsonschema:"Operator-configured storage name; default home"`
	Key      string `json:"idempotency_key"`
}
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
	s := mcp.NewServer(&mcp.Implementation{Name: "virfield", Version: Version}, &mcp.ServerOptions{Instructions: instructions})
	addHelp(s)
	addRegistryTools(s, c)
	mcp.AddTool(s, &mcp.Tool{Name: "virfield_status", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, Description: "Show Lume health, VM slot usage, active leases and durable jobs"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "GET", "status", nil, ""))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "vm_acquire", Annotations: &mcp.ToolAnnotations{IdempotentHint: true}, Description: "Reserve one of at most two VM slots and asynchronously clone/start a verified golden image. Use image_create if the desired macOS/Xcode is not in templates. Supply a fresh per-lease Ed25519 public key; keep its private key local. Use the SAME idempotency key on retries. Returns capacity_exhausted when full."}, func(ctx context.Context, _ *mcp.CallToolRequest, a acquireArgs) (*mcp.CallToolResult, any, error) {
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
	mcp.AddTool(s, &mcp.Tool{Name: "image_catalog", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, Description: "List downloadable Apple silicon macOS restore versions and stable Xcode releases with minimum macOS requirements. Not limited to existing templates. Xcode archives may require Apple Developer authentication configured by the host operator."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "GET", "images/catalog", nil, ""))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "image_create", Annotations: &mcp.ToolAnnotations{IdempotentHint: true}, Description: "Create and build a new golden image by macOS version/build/codename and optional exact Xcode version. Example: macos monterey, xcode 13.4.1. Resolves and pins official Apple downloads; persists the template across restart. Requires all leases released. Select security default, sip-disabled or automation. Omission keeps SIP enabled. No arbitrary host paths, no overwriting VMs. Poll virfield_job for progress and actionable download/authentication errors. Retry with the same idempotency key."}, func(ctx context.Context, _ *mcp.CallToolRequest, a createImageArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "POST", "images", map[string]string{"id": a.ID, "macos": a.MacOS, "xcode": a.Xcode, "location": a.Location, "security": a.Security}, a.Key))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "image_build", Annotations: &mcp.ToolAnnotations{IdempotentHint: true}, Description: "Build an absent VM from an already registered pinned image profile. To choose macOS/Xcode versions, use image_create instead. Downloads and installs macOS, completes setup, applies the operator-configured guest SIP policy and verifies credentials. Requires no active leases. Never overwrites an existing VM. Use the same idempotency key on retries."}, func(ctx context.Context, _ *mcp.CallToolRequest, a releaseArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "POST", "images/"+url.PathEscape(a.ID)+"/build", empty{}, a.Key))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "vm_tunnel", Description: "Open or return an ephemeral loopback TCP tunnel to this ready lease's SSH port. SSH authentication and pinned host key remain mandatory. Reopen after daemon restart.", Annotations: &mcp.ToolAnnotations{IdempotentHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, a idArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "POST", "leases/"+url.PathEscape(a.ID)+"/tunnel", nil, ""))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "vm_tunnel_close", Description: "Close this lease's loopback SSH tunnel and all its connections", Annotations: &mcp.ToolAnnotations{IdempotentHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, a idArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "DELETE", "leases/"+url.PathEscape(a.ID)+"/tunnel", nil, ""))
	})
	return s
}
