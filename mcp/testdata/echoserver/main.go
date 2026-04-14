// Package main implements a minimal MCP server for integration testing.
// It reads JSON-RPC messages from stdin and writes responses to stdout.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		resp := handle(req)
		if resp == nil {
			continue // notification, no response
		}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(os.Stdout, string(data))
	}
}

func handle(req request) *response {
	switch req.Method {
	case "notifications/initialized":
		return nil // notification, no response

	case "initialize":
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2025-11-25",
				"name":            "echo-server",
				"version":         "1.0.0",
				"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			},
		}

	case "tools/list":
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": []interface{}{
					map[string]interface{}{
						"name":        "echo",
						"description": "Echoes the input text back",
						"inputSchema": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"text": map[string]interface{}{"type": "string", "description": "text to echo"},
							},
							"required": []string{"text"},
						},
					},
					map[string]interface{}{
						"name":        "add",
						"description": "Adds two numbers",
						"inputSchema": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"a": map[string]interface{}{"type": "integer", "description": "first number"},
								"b": map[string]interface{}{"type": "integer", "description": "second number"},
							},
							"required": []string{"a", "b"},
						},
					},
				},
			},
		}

	case "tools/call":
		return handleToolCall(req)

	default:
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "method not found: " + req.Method},
		}
	}
}

func handleToolCall(req request) *response {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &response{
			JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32602, Message: "invalid params: " + err.Error()},
		}
	}

	var resultText string
	var isError bool

	switch params.Name {
	case "echo":
		var args struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			resultText = "invalid arguments: " + err.Error()
			isError = true
		} else {
			resultText = args.Text
		}

	case "add":
		var args struct {
			A json.Number `json:"a"`
			B json.Number `json:"b"`
		}
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			resultText = "invalid arguments: " + err.Error()
			isError = true
		} else {
			a, _ := strconv.Atoi(args.A.String())
			b, _ := strconv.Atoi(args.B.String())
			resultText = strconv.Itoa(a + b)
		}

	default:
		resultText = "unknown tool: " + params.Name
		isError = true
	}

	return &response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": resultText},
			},
			"isError": isError,
		},
	}
}
