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
		if len(body) != 3 || body["ssh_public_key"] != "test-public-key" || body["template"] != "golden" {
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
	if len(list.Tools) != 17 {
		t.Fatal(len(list.Tools))
	}
	for _, tool := range list.Tools {
		if strings.Contains(tool.Name, "exec") || (strings.Contains(tool.Name, "resolve") && tool.Name != "registry_resolve") {
			t.Fatal("unsafe tool exposed", tool.Name)
		}
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "vm_acquire", Arguments: map[string]any{"template": "golden", "ttl_seconds": 3600, "idempotency_key": "request-one", "ssh_public_key": "test-public-key"}})
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

func TestMCPVersionSelectionReachesAPI(t *testing.T) {
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/images" || r.Method != "POST" || r.Header.Get("Idempotency-Key") != "monterey-xcode-request" {
			t.Error(r.Method, r.URL, r.Header.Get("Idempotency-Key"))
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["macos"] != "monterey" || body["xcode"] != "13.4.1" || body["id"] != "monterey-xcode" || body["security"] != "automation" {
			t.Error(body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		fmt.Fprint(w, `{"job":{"id":"durable-job"}}`)
	}))
	defer api.Close()
	c, err := client.New(api.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := New(c).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "image_create", Arguments: map[string]any{"id": "monterey-xcode", "macos": "monterey", "xcode": "13.4.1", "security": "automation", "idempotency_key": "monterey-xcode-request"}})
	if err != nil || res.IsError || calls != 1 {
		t.Fatal(res, err, calls)
	}
}

func TestMCPRegistryRequestsReachTypedAPI(t *testing.T) {
	for _, tc := range []struct {
		name, path, key string
		args            map[string]any
	}{
		{"registry_resolve", "registry/resolve", "", map[string]any{"source": "team", "repository": "vm", "tag": "v1"}},
		{"image_pull", "images/pull", "import-unique-key", map[string]any{"id": "new-golden", "source": "team", "repository": "vm", "tag": "v1", "macos": "12.6", "security": "automation", "idempotency_key": "import-unique-key"}},
		{"image_publish", "images/publish", "publish-unique-key", map[string]any{"template": "golden", "source": "team", "repository": "vm", "tag": "v2", "idempotency_key": "publish-unique-key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/api/v1/"+tc.path || r.Header.Get("Idempotency-Key") != tc.key {
					t.Error(r.Method, r.URL)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				for k, v := range tc.args {
					if k != "idempotency_key" && body[k] != v {
						t.Errorf("missing %s", k)
					}
				}
				if body["idempotency_key"] != nil {
					t.Error("key should be header")
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"ok":true}`)
			}))
			defer api.Close()
			c, err := client.New(api.URL, "secret")
			if err != nil {
				t.Fatal(err)
			}
			st, ct := mcp.NewInMemoryTransports()
			ctx := context.Background()
			ss, err := New(c).Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "registry-test", Version: "1"}, nil).Connect(ctx, ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
			if err != nil || result.IsError || calls != 1 {
				t.Fatal(result, err, calls)
			}
		})
	}
}
