package mcpadapter

import (
	"context"
	"fmt"

	"github.com/mallexxx/virfield/docs"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const instructions = "Start with virfield_help, then virfield_status. Read verification before claiming OS/Xcode support. Image and lease mutations are asynchronous: poll virfield_job and check vm_lease readiness. Reuse the same idempotency key on retries. Capacity is shared: at most two macOS VMs, no execution queue. Execute over pinned SSH, export artifacts, then release only with user authorization. For needs_attention or unknown outcomes, inspect the documented recovery procedure; never bypass the journal."

type helpArgs struct {
	Topic string `json:"topic,omitempty" jsonschema:"Documentation topic: workflows (default), images, operations, or verification"`
}

var helpDocuments = []struct{ topic, file, description string }{
	{"workflows", "AGENT-GUIDE.md", "Complete agent workflows, tool arguments, SSH, artifact export and error handling"},
	{"images", "IMAGE-PIPELINE.md", "Version selection, guest security, image dependencies and operator recovery"},
	{"operations", "OPERATIONS.md", "Installation, Codex/Claude registration, services, configuration and backups"},
	{"verification", "VERIFICATION.md", "Acceptance evidence, tested combinations and remaining release blockers"},
}

func addHelp(s *mcp.Server) {
	for _, document := range helpDocuments {
		uri := "virfield://docs/" + document.topic
		s.AddResource(&mcp.Resource{URI: uri, Name: document.topic, MIMEType: "text/markdown", Description: document.description}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			content, err := docs.Files.ReadFile(document.file)
			if err != nil {
				return nil, err
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "text/markdown", Text: string(content)}}}, nil
		})
	}
	mcp.AddTool(s, &mcp.Tool{Name: "virfield_help", Description: "Read the complete embedded agent runbook BEFORE using Virfield: workflows (default), images, operations or verification. Includes arguments, examples, pinned SSH execution, cleanup, recovery and tested scope. No source checkout or Obsidian needed. Read-only; available without contacting the daemon.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, func(_ context.Context, _ *mcp.CallToolRequest, a helpArgs) (*mcp.CallToolResult, any, error) {
		if a.Topic == "" {
			a.Topic = "workflows"
		}
		for _, document := range helpDocuments {
			if document.topic == a.Topic {
				content, err := docs.Files.ReadFile(document.file)
				if err != nil {
					return nil, nil, err
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(content)}}}, nil, nil
			}
		}
		return nil, nil, fmt.Errorf("unknown documentation topic; use workflows, images, operations or verification")
	})
}
