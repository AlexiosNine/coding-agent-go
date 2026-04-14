package mcp

import (
	"context"
	"encoding/json"
)

// MCPTool adapts an MCP server tool to the cc.Tool interface.
// It forwards Execute calls to the MCP server via the Client.
type MCPTool struct {
	client *Client
	info   ToolInfo
}

// NewMCPTool creates a Tool adapter for an MCP server tool.
func NewMCPTool(client *Client, info ToolInfo) *MCPTool {
	return &MCPTool{client: client, info: info}
}

func (t *MCPTool) Name() string                { return t.info.Name }
func (t *MCPTool) Description() string          { return t.info.Description }
func (t *MCPTool) InputSchema() json.RawMessage { return t.info.InputSchema }

func (t *MCPTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	return t.client.CallTool(ctx, t.info.Name, input)
}
