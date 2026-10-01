package mcpadapter

import (
	"context"
	"github.com/mallexxx/virfield/internal/client"
	"github.com/mallexxx/virfield/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type pullImageArgs struct {
	ID         string `json:"id"`
	Source     string `json:"source" jsonschema:"Registry source ID from registry_sources"`
	Repository string `json:"repository"`
	Tag        string `json:"tag" jsonschema:"Explicit registry tag; manifest digest is pinned at acceptance"`
	MacOS      string `json:"macos" jsonschema:"Expected macOS version or build, checked after import"`
	Xcode      string `json:"xcode,omitempty"`
	Security   string `json:"security,omitempty"`
	Location   string `json:"location,omitempty"`
	Key        string `json:"idempotency_key"`
}
type publishImageArgs struct {
	Template   string `json:"template"`
	Source     string `json:"source"`
	Repository string `json:"repository"`
	Tag        string `json:"tag" jsonschema:"New explicit tag; existing tags are refused"`
	Key        string `json:"idempotency_key"`
}

func addRegistryTools(s *mcp.Server, c *client.Client) {
	mcp.AddTool(s, &mcp.Tool{Name: "registry_sources", Description: "List operator-approved GHCR namespaces and whether publication is enabled. No credentials are returned.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "GET", "registry/sources", nil, ""))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "registry_resolve", Description: "Read a GHCR tag and verify its manifest digest and layer descriptors. Does not download or boot a VM.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, a domain.RegistryResolveRequest) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "POST", "registry/resolve", a, ""))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "image_pull", Description: "Import a GHCR VM into a new golden, pinning the resolved digest before download. Requires an idle pool. Refuses existing names and changed tags. Expected macOS/Xcode, unencrypted disks, isolated SSH and selected security policy must verify before publication. Poll virfield_job. Same idempotency key on retries.", Annotations: &mcp.ToolAnnotations{IdempotentHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, a pullImageArgs) (*mcp.CallToolResult, any, error) {
		r := domain.ImagePullRequest{ImageCreateRequest: domain.ImageCreateRequest{ID: a.ID, MacOS: a.MacOS, Xcode: a.Xcode, Security: a.Security, Location: a.Location}, Source: a.Source, Repository: a.Repository, Tag: a.Tag}
		return result(c.Do(ctx, "POST", "images/pull", r, a.Key))
	})
	mcp.AddTool(s, &mcp.Tool{Name: "image_publish", Description: "Rebuild a portable image from a verified template recipe and publish it to an operator-enabled GHCR namespace/new tag. Requires explicit authorization to upload and an idle pool. Does NOT upload the source VM disk or its credentials/workload changes. Uses a fresh build with public bootstrap credentials, verifies and sanitizes it, uploads, verifies the registry digest, then deletes that temporary build. Original golden is preserved. Poll virfield_job; same idempotency key on retries.", Annotations: &mcp.ToolAnnotations{IdempotentHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, a publishImageArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "POST", "images/publish", domain.ImagePublishRequest{Template: a.Template, Source: a.Source, Repository: a.Repository, Tag: a.Tag}, a.Key))
	})
}
