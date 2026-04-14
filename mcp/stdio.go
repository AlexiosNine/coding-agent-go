package mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// StdioTransport communicates with an MCP server via subprocess stdin/stdout.
type StdioTransport struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	mu      sync.Mutex
}

// NewStdioTransport starts a subprocess and returns a transport connected to it.
func NewStdioTransport(command string, args ...string) (*StdioTransport, error) {
	cmd := exec.Command(command, args...)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdio: create stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdio: create stdout pipe: %w", err)
	}

	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp stdio: start process %q: %w", command, err)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	return &StdioTransport{
		cmd:     cmd,
		stdin:   stdinPipe,
		scanner: scanner,
	}, nil
}

func (t *StdioTransport) Send(_ context.Context, data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Write JSON + newline delimiter
	msg := append(data, '\n')
	_, err := t.stdin.Write(msg)
	if err != nil {
		return fmt.Errorf("mcp stdio: write: %w", err)
	}
	return nil
}

func (t *StdioTransport) Receive(_ context.Context) ([]byte, error) {
	if !t.scanner.Scan() {
		if err := t.scanner.Err(); err != nil {
			return nil, fmt.Errorf("mcp stdio: read: %w", err)
		}
		return nil, fmt.Errorf("mcp stdio: server closed connection")
	}
	return append([]byte(nil), t.scanner.Bytes()...), nil
}

func (t *StdioTransport) Close() error {
	_ = t.stdin.Close()
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	return t.cmd.Wait()
}
