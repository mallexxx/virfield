// Package mcpadapter exposes API operations using the official MCP Go SDK.
// There is deliberately no import of store, control, runtime, SSH or os/exec.
package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"

	"github.com/mallexxx/virfield/internal/client"
	"github.com/mallexxx/virfield/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is overridden by the release packager through Go linker flags.
var Version = "2.1.0-dev"

type empty struct{}
type createImageArgs struct {
	Toolchain string `json:"toolchain,omitempty" jsonschema:"Optional legacy-ios-v1: exact Monterey 12.7.6/Xcode 13.4.1, pinned native Ruby/CocoaPods/FFI/Ethon and armv7 build qualification; Tart only"`
	Security  string `json:"security,omitempty" jsonschema:"Guest policy: default keeps protections; sip-disabled disables SIP; automation also configures Gatekeeper, AMFI and Terminal/SSH TCC grants. Applied only inside the VM and verified after reboot."`
	ID        string `json:"id" jsonschema:"New golden image ID and VM name"`
	MacOS     string `json:"macos" jsonschema:"macOS exact version, build or codename such as sequoia; inspect image_catalog first"`
	Xcode     string `json:"xcode,omitempty" jsonschema:"Optional exact stable Xcode version, such as 16.2"`
	Location  string `json:"location,omitempty" jsonschema:"Optional operator-configured storage name; omission uses the configured default"`
	Key       string `json:"idempotency_key"`
}
type acquireArgs struct {
	DestinationLocation string `json:"destination_location,omitempty" jsonschema:"Optional operator-configured storage name. Explicit selection is strict; omission uses configured default and fallbacks."`
	SSHPublicKey        string `json:"ssh_public_key" jsonschema:"Fresh Ed25519 public key for this lease. Never submit a private key."`
	Template            string `json:"template"`
	TTLSeconds          int    `json:"ttl_seconds"`
	Key                 string `json:"idempotency_key"`
}
type idArgs struct {
	ID string `json:"id"`
}
type tunnelArgs struct {
	ID      string `json:"id"`
	Service string `json:"service,omitempty" jsonschema:"ssh (default) or desktop. A desktop tunnel returns a vnc:// URL that signs in to the VM lease's Screen Sharing with the lease password."`
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
		var fault *domain.Error
		if errors.As(err, &fault) {
			body, _ := json.Marshal(map[string]any{"error": fault})
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}, StructuredContent: json.RawMessage(body)}, nil, nil
		}
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, StructuredContent: json.RawMessage(b)}, nil, nil
}
func New(c *client.Client) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "virfield", Version: Version}, &mcp.ServerOptions{Instructions: instructions})
	additive := false
	addHelp(s)
	addRegistryTools(s, c)
	addTransferTool(s, c)
	addRevalidationTool(s, c)
	mcp.AddTool(s, &mcp.Tool{Name: "virfield_status", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, Description: "Show runtime health, VM slots, leases/jobs and per-template resource_admission estimates: CPU/RAM, each storage's observed free, reserved growth, requested full potential, safety reserve, headroom and image-cache quota. Unknown inputs are explicit. Estimates require verified inventory and are rechecked during admission/dispatch."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "GET", "status", nil, ""))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "vm_acquire", Annotations: &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: &additive}, Description: "Reserve capacity on the selected VM host or physical node and asynchronously prepare a lease. VM leases require a verified golden; VM readiness verifies isolated SSH and native GUI/SSH keychain access. Tart guests also verify a 1920x1080 logical desktop. Physical leases require the requested booted environment. For new Tart profiles, image_create selects operator-approved bases. Supply a fresh per-lease Ed25519 public key; keep its private key local. Use the SAME idempotency key on retries. Capacity and resource limits are checked per host; Virfield has no execution queue."}, func(ctx context.Context, _ *mcp.CallToolRequest, a acquireArgs) (*mcp.CallToolResult, any, error) {
		body := map[string]any{"template": a.Template, "ttl_seconds": a.TTLSeconds, "ssh_public_key": a.SSHPublicKey}
		if a.DestinationLocation != "" {
			body["destination_location"] = a.DestinationLocation
		}
		return result(c.Do(ctx, "POST", "leases", body, a.Key))
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
	mcp.AddTool(s, &mcp.Tool{Name: "image_apple_auth", Description: "For an image job waiting with apple_auth_required, issue a 15-minute local link that opens Apple's Developer website in the native Virfield browser. Send the link privately to the human on the same Mac. Apple Account, password, 2FA and session never enter MCP. Successful sign-in automatically resumes the exact download job.", Annotations: &mcp.ToolAnnotations{IdempotentHint: false}}, func(ctx context.Context, _ *mcp.CallToolRequest, a idArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "POST", "jobs/"+url.PathEscape(a.ID)+"/apple-auth", empty{}, ""))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "virfield_events", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, Description: "Read ordered durable events, optionally filtered by lease_id. Typed diagnostic records retain expected/observed endpoints, drift, inventory failures and separate tunnel listener/stream close causes without credentials or payloads. Latest 32 per diagnostic kind/lease survive cleanup until normal retention; cursors may have gaps. Persist next_cursor after processing."}, func(ctx context.Context, _ *mcp.CallToolRequest, a eventsArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "GET", "events?after="+strconv.FormatInt(a.After, 10)+"&lease_id="+url.QueryEscape(a.LeaseID), nil, ""))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "image_catalog", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, Description: "List Apple silicon macOS restore versions and stable Xcode releases with minimum macOS and local_state: available, downloaded or installed. installed_images names verified ready golden templates. Metadata availability does not prove download authorization or VM acceptance."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "GET", "images/catalog", nil, ""))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "image_create", Annotations: &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: &additive}, Description: "Create and build a new Tart golden from an operator-approved digest-pinned base selected by exact macOS version/build/codename and optional stable Xcode version. Inspect image_catalog tart_bases first; public metadata alone does not establish buildability. Pins any requested Xcode archive metadata and persists the template across restart. Requires an available VM slot and CPU/RAM/disk budget. Select security default, sip-disabled or automation with a matching upstream SIP policy. No arbitrary host paths or overwriting VMs. Poll virfield_job; retry with the same idempotency key."}, func(ctx context.Context, _ *mcp.CallToolRequest, a createImageArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "POST", "images", map[string]string{"id": a.ID, "macos": a.MacOS, "xcode": a.Xcode, "location": a.Location, "security": a.Security, "toolchain": a.Toolchain}, a.Key))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "image_build", Annotations: &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: &additive}, Description: "Build an absent golden from a registered pinned Tart or node VM profile. Tart adopts an approved GHCR base through Packer, applies the recipe and qualifies the guest after reboot; node builds secure an operator-installed bootstrap golden. Exact OS/build, upstream SIP policy, credentials, native GUI/SSH keychain access and applicable workload tools must verify. Requires provider capacity and resource budget. Never overwrites an existing VM. Poll virfield_job and use the same idempotency key on retries."}, func(ctx context.Context, _ *mcp.CallToolRequest, a releaseArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "POST", "images/"+url.PathEscape(a.ID)+"/build", empty{}, a.Key))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "vm_tunnel", Description: "Open or return an ephemeral loopback TCP tunnel to this ready lease's SSH port, or with service desktop to a VM lease's Screen Sharing (returns a vnc:// URL). SSH authentication and pinned host key remain mandatory. Reopen after daemon restart.", Annotations: &mcp.ToolAnnotations{IdempotentHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, a tunnelArgs) (*mcp.CallToolResult, any, error) {
		var body any
		if a.Service != "" {
			body = map[string]string{"service": a.Service}
		}
		return result(c.Do(ctx, "POST", "leases/"+url.PathEscape(a.ID)+"/tunnel", body, ""))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "vm_tunnel_close", Description: "Close this lease's loopback tunnel for one service (ssh by default, or desktop) and all its connections", Annotations: &mcp.ToolAnnotations{IdempotentHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, a tunnelArgs) (*mcp.CallToolResult, any, error) {
		path := "leases/" + url.PathEscape(a.ID) + "/tunnel"
		if a.Service != "" {
			path += "?service=" + url.QueryEscape(a.Service)
		}
		return result(c.Do(ctx, "DELETE", path, nil, ""))
	})
	return s
}
