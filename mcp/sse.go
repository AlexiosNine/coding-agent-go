package mcp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// SSETransport communicates with an MCP server via HTTP SSE.
// The client GETs the SSE endpoint to receive messages, and POSTs to the
// session URL (received via the "endpoint" SSE event) to send messages.
type SSETransport struct {
	baseURL    string
	client     *http.Client
	sessionURL string
	messages   chan []byte
	done       chan struct{}
	closeOnce  sync.Once
	resp       *http.Response
}

// NewSSETransport connects to an MCP server's SSE endpoint.
// baseURL should be the server's base URL (e.g., "http://localhost:8080").
func NewSSETransport(ctx context.Context, baseURL string) (*SSETransport, error) {
	t := &SSETransport{
		baseURL:  strings.TrimRight(baseURL, "/"),
		client:   &http.Client{},
		messages: make(chan []byte, 64),
		done:     make(chan struct{}),
	}

	// Connect to SSE endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+"/sse", nil)
	if err != nil {
		return nil, fmt.Errorf("mcp sse: create request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp sse: connect: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("mcp sse: unexpected status %d", resp.StatusCode)
	}
	t.resp = resp

	// Read the first event to get the session endpoint URL
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: endpoint") {
			if scanner.Scan() {
				data := strings.TrimPrefix(scanner.Text(), "data: ")
				if strings.HasPrefix(data, "/") {
					t.sessionURL = t.baseURL + data
				} else {
					t.sessionURL = data
				}
				break
			}
		}
	}
	if t.sessionURL == "" {
		resp.Body.Close()
		return nil, fmt.Errorf("mcp sse: no endpoint event received")
	}

	// Start background goroutine to read SSE messages
	go t.readLoop(resp.Body, scanner)

	return t, nil
}

func (t *SSETransport) readLoop(body io.Reader, scanner *bufio.Scanner) {
	defer close(t.messages)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: message") {
			if scanner.Scan() {
				data := strings.TrimPrefix(scanner.Text(), "data: ")
				select {
				case t.messages <- []byte(data):
				case <-t.done:
					return
				}
			}
		}
	}
}

func (t *SSETransport) Send(ctx context.Context, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.sessionURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("mcp sse: create post request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("mcp sse: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mcp sse: send failed status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (t *SSETransport) Receive(_ context.Context) ([]byte, error) {
	msg, ok := <-t.messages
	if !ok {
		return nil, fmt.Errorf("mcp sse: connection closed")
	}
	return msg, nil
}

func (t *SSETransport) Close() error {
	t.closeOnce.Do(func() {
		close(t.done)
		if t.resp != nil {
			t.resp.Body.Close()
		}
	})
	return nil
}
