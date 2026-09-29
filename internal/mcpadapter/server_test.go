package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPProxiesToAPIAndPreservesIdempotency(t *testing.T) {
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing bearer token")
		}
		if r.URL.Path != "/api/v1/leases" || r.Method != "POST" || r.Header.Get("Idempotency-Key") != "request-one" {
			t.Error(r.Method, r.URL, r.Header.Get("Idempotency-Key"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body) != 2 || body["template"] != "golden" {
			t.Error(body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(409)
		fmt.Fprint(w, `{"error":{"code":"capacity_exhausted","message":"2/2 slots occupied","retryable":false}}`)
	}))
	defer api.Close()
	c, err := client.New(api.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	server := New(c)
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 8 {
		t.Fatal(len(list.Tools))
	}
	for _, tool := range list.Tools {
		if strings.Contains(tool.Name, "exec") || strings.Contains(tool.Name, "resolve") {
			t.Fatal("unsafe tool exposed", tool.Name)
		}
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "vm_acquire", Arguments: map[string]any{"template": "golden", "ttl_seconds": 3600, "idempotency_key": "request-one"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || len(res.Content) == 0 {
		t.Fatal(res)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, "capacity_exhausted") {
		t.Fatal(res)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
