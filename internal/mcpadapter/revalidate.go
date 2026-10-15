package mcpadapter

import (
	"context"
	"net/url"

	"github.com/mallexxx/virfield/internal/client"
	"github.com/mallexxx/virfield/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type revalidateArgs struct {
	ID          string `json:"id"`
	VMName      string `json:"vm_name" jsonschema:"Exact existing VM name; revalidation never clones or deletes it"`
	CallerProof string `json:"caller_proof" jsonschema:"Public proof generated locally with virfield -key SAME_KEY revalidate-proof ID VM_NAME EXISTING_IDENTITY_DIRECTORY using the lease's original Ed25519 key. Never submit a private key."`
	Key         string `json:"idempotency_key"`
}

func addRevalidationTool(s *mcp.Server, c *client.Client) {
	additive := false
	mcp.AddTool(s, &mcp.Tool{Name: "vm_revalidate", Annotations: &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: &additive}, Description: "Revalidate an owned unexpired VM lease in needs_attention/vm_drift without deleting/recloning it or changing keys, account, workspace or TTL. Requires its existing verified runtime identity, current verified running endpoint, original image provenance and original caller-key proof. Independently checks pinned SSH, management authentication and caller-key acceptance. Save the request/key before dispatch; poll virfield_job and vm_lease before reconnecting. Read workflows first; unsupported legacy baselines fail without repair."}, func(ctx context.Context, _ *mcp.CallToolRequest, a revalidateArgs) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "POST", "leases/"+url.PathEscape(a.ID)+"/revalidate", domain.RevalidateRequest{VMName: a.VMName, CallerProof: a.CallerProof}, a.Key))
	})
}
