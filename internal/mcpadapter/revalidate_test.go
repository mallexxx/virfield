package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mallexxx/virfield/internal/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMCPRevalidationForwardsExactOwnedNamePublicProofAndStableKey(t *testing.T) {
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/leases/lease-existing/revalidate" || r.Method != "POST" || r.Header.Get("Idempotency-Key") != "revalidation-request" {
			t.Error("Incorrect revalidation route/key")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body) != 2 || body["vm_name"] != "vf-existing" || body["caller_proof"] != "public-bound-proof" {
			t.Error("Proof request changed or added private input")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"lease":{"id":"lease-existing","state":"revalidating"},"job":{"id":"job-revalidation","kind":"lease_revalidate","state":"queued"}}`)
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
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "vm_revalidate", Arguments: map[string]any{"id": "lease-existing", "vm_name": "vf-existing", "caller_proof": "public-bound-proof", "idempotency_key": "revalidation-request"}})
	if err != nil || result.IsError || calls != 1 {
		t.Fatal("Revalidation MCP request failed", err, calls)
	}
}
