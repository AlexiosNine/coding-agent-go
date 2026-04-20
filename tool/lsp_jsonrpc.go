package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// executeJSONRPC runs a language server via JSON-RPC over stdio.
func executeJSONRPC(ctx context.Context, cfg *ServerConfig, root, operation, file string, line, col int, query string) (string, error) {
	// Spawn language server process
	args := cfg.Args
	cmd := exec.CommandContext(ctx, cfg.Binary, args...)
	cmd.Dir = root

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start %s: %w", cfg.Binary, err)
	}
	defer func() {
		stdin.Close()
		cmd.Process.Kill()
	}()

	rpc := newJSONRPCConn(stdin, stdout)

	// Initialize handshake
	initResult, err := rpc.call(ctx, "initialize", map[string]interface{}{
		"processId": os.Getpid(),
		"rootUri":   "file://" + root,
		"capabilities": map[string]interface{}{
			"textDocument": map[string]interface{}{
				"definition":    map[string]interface{}{"linkSupport": true},
				"references":    map[string]interface{}{"contextSupport": true},
				"hover":         map[string]interface{}{"contentFormat": []string{"plaintext"}},
				"documentSymbol": map[string]interface{}{},
			},
			"workspace": map[string]interface{}{
				"symbol": map[string]interface{}{},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("initialize failed: %w", err)
	}
	_ = initResult // ignore for now

	// Send initialized notification
	if err := rpc.notify(ctx, "initialized", map[string]interface{}{}); err != nil {
		return "", fmt.Errorf("initialized notification failed: %w", err)
	}

	// Open the file
	content, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	if err := rpc.notify(ctx, "textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        "file://" + file,
			"languageId": cfg.Language,
			"version":    1,
			"text":       string(content),
		},
	}); err != nil {
		return "", fmt.Errorf("didOpen failed: %w", err)
	}

	// Execute the operation
	var result json.RawMessage
	switch operation {
	case "definition":
		if line <= 0 || col <= 0 {
			return "", fmt.Errorf("line and column are required for definition")
		}
		result, err = rpc.call(ctx, "textDocument/definition", map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": "file://" + file},
			"position":     map[string]interface{}{"line": line - 1, "character": col - 1},
		})

	case "references":
		if line <= 0 || col <= 0 {
			return "", fmt.Errorf("line and column are required for references")
		}
		result, err = rpc.call(ctx, "textDocument/references", map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": "file://" + file},
			"position":     map[string]interface{}{"line": line - 1, "character": col - 1},
			"context":      map[string]interface{}{"includeDeclaration": true},
		})

	case "hover":
		if line <= 0 || col <= 0 {
			return "", fmt.Errorf("line and column are required for hover")
		}
		result, err = rpc.call(ctx, "textDocument/hover", map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": "file://" + file},
			"position":     map[string]interface{}{"line": line - 1, "character": col - 1},
		})

	case "symbols":
		result, err = rpc.call(ctx, "textDocument/documentSymbol", map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": "file://" + file},
		})

	case "workspace_symbol":
		if query == "" {
			return "", fmt.Errorf("query is required for workspace_symbol")
		}
		result, err = rpc.call(ctx, "workspace/symbol", map[string]interface{}{
			"query": query,
		})

	default:
		return "", fmt.Errorf("unsupported operation: %s", operation)
	}

	if err != nil {
		return "", fmt.Errorf("%s failed: %w", operation, err)
	}

	// Shutdown
	rpc.call(ctx, "shutdown", nil)
	rpc.notify(ctx, "exit", nil)

	return formatJSONRPCResult(operation, result)
}

// jsonrpcConn handles JSON-RPC 2.0 communication over stdio.
type jsonrpcConn struct {
	stdin  io.WriteCloser
	stdout io.Reader
	nextID int
}

func newJSONRPCConn(stdin io.WriteCloser, stdout io.Reader) *jsonrpcConn {
	return &jsonrpcConn{stdin: stdin, stdout: stdout, nextID: 1}
}

func (c *jsonrpcConn) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := c.nextID
	c.nextID++

	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	if err := c.send(req); err != nil {
		return nil, err
	}

	return c.receive(ctx, id)
}

func (c *jsonrpcConn) notify(ctx context.Context, method string, params interface{}) error {
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	return c.send(req)
}

func (c *jsonrpcConn) send(msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := c.stdin.Write([]byte(header)); err != nil {
		return err
	}
	if _, err := c.stdin.Write(data); err != nil {
		return err
	}
	return nil
}

func (c *jsonrpcConn) receive(ctx context.Context, expectedID int) (json.RawMessage, error) {
	reader := bufio.NewReader(c.stdout)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Read headers
		var contentLength int
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return nil, err
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "Content-Length:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					contentLength, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
				}
			}
		}

		if contentLength == 0 {
			continue
		}

		// Read body
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(reader, body); err != nil {
			return nil, err
		}

		var resp struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}

		if err := json.Unmarshal(body, &resp); err != nil {
			continue // might be a notification, skip
		}

		if resp.ID == expectedID {
			if resp.Error != nil {
				return nil, fmt.Errorf("RPC error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			return resp.Result, nil
		}
	}
}

func formatJSONRPCResult(operation string, result json.RawMessage) (string, error) {
	if len(result) == 0 || string(result) == "null" {
		return fmt.Sprintf("No results for %s", operation), nil
	}

	switch operation {
	case "definition":
		var locations []struct {
			URI   string `json:"uri"`
			Range struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
			} `json:"range"`
		}
		if err := json.Unmarshal(result, &locations); err != nil {
			return "", err
		}
		if len(locations) == 0 {
			return "No definition found", nil
		}
		loc := locations[0]
		uri := strings.TrimPrefix(loc.URI, "file://")
		return fmt.Sprintf("%s:%d:%d", uri, loc.Range.Start.Line+1, loc.Range.Start.Character+1), nil

	case "references":
		var locations []struct {
			URI   string `json:"uri"`
			Range struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
			} `json:"range"`
		}
		if err := json.Unmarshal(result, &locations); err != nil {
			return "", err
		}
		if len(locations) == 0 {
			return "No references found", nil
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("References (%d found):\n", len(locations)))
		for i, loc := range locations {
			uri := strings.TrimPrefix(loc.URI, "file://")
			b.WriteString(fmt.Sprintf("  %d. %s:%d:%d\n", i+1, uri, loc.Range.Start.Line+1, loc.Range.Start.Character+1))
		}
		return b.String(), nil

	case "hover":
		var hover struct {
			Contents interface{} `json:"contents"`
		}
		if err := json.Unmarshal(result, &hover); err != nil {
			return "", err
		}
		// Extract text from hover contents (can be string or MarkupContent)
		switch v := hover.Contents.(type) {
		case string:
			return v, nil
		case map[string]interface{}:
			if value, ok := v["value"].(string); ok {
				return value, nil
			}
		}
		return string(result), nil

	case "symbols":
		var symbols []struct {
			Name string `json:"name"`
			Kind int    `json:"kind"`
		}
		if err := json.Unmarshal(result, &symbols); err != nil {
			return "", err
		}
		if len(symbols) == 0 {
			return "No symbols found", nil
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Symbols (%d):\n", len(symbols)))
		for _, sym := range symbols {
			b.WriteString(fmt.Sprintf("  %s (kind: %d)\n", sym.Name, sym.Kind))
		}
		return b.String(), nil

	case "workspace_symbol":
		var symbols []struct {
			Name     string `json:"name"`
			Kind     int    `json:"kind"`
			Location struct {
				URI string `json:"uri"`
			} `json:"location"`
		}
		if err := json.Unmarshal(result, &symbols); err != nil {
			return "", err
		}
		if len(symbols) == 0 {
			return "No symbols found", nil
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Workspace symbols (%d):\n", len(symbols)))
		for _, sym := range symbols {
			uri := strings.TrimPrefix(sym.Location.URI, "file://")
			b.WriteString(fmt.Sprintf("  %s (kind: %d) in %s\n", sym.Name, sym.Kind, uri))
		}
		return b.String(), nil

	default:
		return string(result), nil
	}
}
