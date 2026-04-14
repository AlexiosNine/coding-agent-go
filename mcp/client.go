package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// ClientInfo identifies the MCP client to the server.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ServerInfo contains the server's identity from the initialize response.
type ServerInfo struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	ProtocolVersion string `json:"protocolVersion"`
}

// ToolInfo describes a tool discovered from the MCP server.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Client is an MCP client that communicates with an MCP server.
type Client struct {
	transport Transport
	nextID    atomic.Int64
	info      ClientInfo
}

// NewClient creates a new MCP client with the given transport.
func NewClient(transport Transport, info ClientInfo) *Client {
	return &Client{
		transport: transport,
		info:      info,
	}
}

// call sends a JSON-RPC request and waits for the response.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	var rawParams json.RawMessage
	if params != nil {
		var err error
		rawParams, err = json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("mcp: marshal params: %w", err)
		}
	}

	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  rawParams,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}

	if err := c.transport.Send(ctx, data); err != nil {
		return nil, err
	}

	// Read response (skip notifications)
	for {
		respData, err := c.transport.Receive(ctx)
		if err != nil {
			return nil, err
		}

		var resp Response
		if err := json.Unmarshal(respData, &resp); err != nil {
			continue // skip malformed messages
		}

		// Skip notifications (id == 0 and no result/error)
		if resp.ID == 0 && resp.Result == nil && resp.Error == nil {
			continue
		}

		if resp.ID != id {
			continue // skip responses for other requests
		}

		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// notify sends a JSON-RPC notification (no response expected).
func (c *Client) notify(ctx context.Context, method string) error {
	notif := Notification{
		JSONRPC: "2.0",
		Method:  method,
	}
	data, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("mcp: marshal notification: %w", err)
	}
	return c.transport.Send(ctx, data)
}

// Initialize performs the MCP initialization handshake.
func (c *Client) Initialize(ctx context.Context) (*ServerInfo, error) {
	params := map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]string{
			"name":    c.info.Name,
			"version": c.info.Version,
		},
	}

	result, err := c.call(ctx, "initialize", params)
	if err != nil {
		return nil, fmt.Errorf("mcp: initialize: %w", err)
	}

	var info ServerInfo
	if err := json.Unmarshal(result, &info); err != nil {
		return nil, fmt.Errorf("mcp: unmarshal server info: %w", err)
	}

	// Send initialized notification
	if err := c.notify(ctx, "notifications/initialized"); err != nil {
		return nil, fmt.Errorf("mcp: send initialized notification: %w", err)
	}

	return &info, nil
}

// ListTools discovers available tools from the MCP server.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	result, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: list tools: %w", err)
	}

	var resp struct {
		Tools []ToolInfo `json:"tools"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("mcp: unmarshal tools: %w", err)
	}
	return resp.Tools, nil
}

// CallTool invokes a tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	params := map[string]any{
		"name":      name,
		"arguments": json.RawMessage(args),
	}

	result, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return "", fmt.Errorf("mcp: call tool %q: %w", name, err)
	}

	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return "", fmt.Errorf("mcp: unmarshal tool result: %w", err)
	}

	var text string
	for _, c := range resp.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}

	if resp.IsError {
		return "", fmt.Errorf("mcp: tool %q error: %s", name, text)
	}
	return text, nil
}

// Close shuts down the transport.
func (c *Client) Close() error {
	return c.transport.Close()
}
