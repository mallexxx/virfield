package mcpadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/docs"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHelpDiscoveryWithoutDaemonAndDocumentationCoverage(t *testing.T) {
	// No API client: embedded help/resources must work even when the backend
	// is unavailable. This also prevents the help tool from making mutations.
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := New(nil).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if !strings.Contains(cs.InitializeResult().Instructions, "virfield_help") {
		t.Fatal("missing startup workflow guidance")
	}
	resources, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources.Resources) != len(helpDocuments) {
		t.Fatal("incomplete documentation discovery")
	}
	for _, document := range helpDocuments {
		t.Run(document.topic, func(t *testing.T) {
			want, err := docs.Files.ReadFile(document.file)
			if err != nil {
				t.Fatal(err)
			}
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "virfield_help", Arguments: map[string]any{"topic": document.topic}})
			if err != nil {
				t.Fatal(err)
			}
			if res.IsError || len(res.Content) != 1 {
				t.Fatal(res)
			}
			content, ok := res.Content[0].(*mcp.TextContent)
			if !ok || content.Text != string(want) {
				t.Fatal("help diverged from maintained documentation")
			}
			uri := "virfield://docs/" + document.topic
			resource, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
			if err != nil {
				t.Fatal(err)
			}
			if len(resource.Contents) != 1 || resource.Contents[0].Text != string(want) || resource.Contents[0].URI != uri || resource.Contents[0].MIMEType != "text/markdown" {
				t.Fatal("resource diverged from help")
			}
		})
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "virfield_help", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatal(res, err)
	}
	guide := res.Content[0].(*mcp.TextContent).Text
	if !strings.HasPrefix(guide, "# Agent guide") {
		t.Fatal("default topic is not agent guide")
	}
	section := strings.Split(strings.Split(guide, "## Tool reference\n")[1], "## Build a golden")[0]
	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "| `") {
			name, _, ok := strings.Cut(strings.TrimPrefix(line, "| `"), "` |")
			if ok {
				documented[name] = true
			}
		}
	}
	if len(documented) != len(listed.Tools) {
		t.Fatalf("guide documents %d tools, server exposes %d", len(documented), len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		if !documented[tool.Name] {
			t.Errorf("undocumented tool: %s", tool.Name)
		}
		if tool.Name == "virfield_help" && (tool.Annotations == nil || !tool.Annotations.ReadOnlyHint) {
			t.Error("help not marked read-only")
		}
	}
	for _, topic := range []string{"../OPERATIONS.md", "/etc/passwd", "unknown"} {
		bad, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "virfield_help", Arguments: map[string]any{"topic": topic}})
		if err == nil && !bad.IsError {
			t.Errorf("accepted non-allowlisted topic %q", topic)
		}
	}
	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "virfield://docs/unknown"}); err == nil {
		t.Fatal("unknown resource accepted")
	}
}
