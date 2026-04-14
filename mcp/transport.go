package mcp

import "context"

// Transport is the interface for MCP communication channels.
type Transport interface {
	// Send writes a JSON-RPC message to the server.
	Send(ctx context.Context, data []byte) error
	// Receive reads the next JSON-RPC message from the server.
	Receive(ctx context.Context) ([]byte, error)
	// Close shuts down the transport.
	Close() error
}
