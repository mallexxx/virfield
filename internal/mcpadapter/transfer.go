package mcpadapter

import (
	"context"
	"github.com/mallexxx/virfield/internal/client"
	"github.com/mallexxx/virfield/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addTransferTool(s *mcp.Server, c *client.Client) {
	type args struct {
		Template string `json:"template" jsonschema:"Verified managed source Tart template"`
		ID       string `json:"id" jsonschema:"New destination ID and VM name; never overwritten"`
		Location string `json:"location" jsonschema:"Different operator-configured local Tart storage"`
		Key      string `json:"idempotency_key"`
	}
	additive := false
	mcp.AddTool(s, &mcp.Tool{Name: "image_transfer", Annotations: &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: &additive}, Description: "Copy a verified managed local Tart golden to a new name in another configured storage. Requires an idle pool and full destination disk budget. Preserves source, recipe, qualification and private SSH identity; verifies content hashes before atomic registration. Does not upload to a registry. Partial or interrupted copies stay quarantined and are never blindly replayed. Poll virfield_job and use the SAME idempotency key."}, func(ctx context.Context, _ *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		return result(c.Do(ctx, "POST", "images/transfer", domain.ImageTransferRequest{Template: a.Template, ID: a.ID, Location: a.Location}, a.Key))
	})
}
