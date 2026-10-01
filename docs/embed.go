// Package docs embeds the maintained operator and agent runbooks into releases.
package docs

import "embed"

// Files is the source of truth for both MCP resources and the help tool.
//
//go:embed AGENT-GUIDE.md IMAGE-PIPELINE.md OPERATIONS.md VERIFICATION.md
var Files embed.FS
