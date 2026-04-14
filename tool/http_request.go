package tool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	cc "github.com/alexioschen/cc-connect/goagent"
)

type httpInput struct {
	URL     string `json:"url" desc:"The URL to request"`
	Method  string `json:"method" desc:"HTTP method: GET or POST (default GET)"`
	Body    string `json:"body" desc:"Request body for POST requests"`
	Timeout int    `json:"timeout" desc:"Timeout in seconds (default 30)"`
}

// HTTPRequest returns a tool that makes HTTP requests.
func HTTPRequest() cc.Tool {
	return cc.NewFuncTool("http_request", "Make an HTTP GET or POST request", func(ctx context.Context, in httpInput) (string, error) {
		method := strings.ToUpper(in.Method)
		if method == "" {
			method = http.MethodGet
		}

		timeout := 30
		if in.Timeout > 0 {
			timeout = in.Timeout
		}

		ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()

		var bodyReader io.Reader
		if in.Body != "" {
			bodyReader = strings.NewReader(in.Body)
		}

		req, err := http.NewRequestWithContext(ctx, method, in.URL, bodyReader)
		if err != nil {
			return "", fmt.Errorf("create request: %w", err)
		}
		if in.Body != "" {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("send request: %w", err)
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("read response: %w", err)
		}

		return fmt.Sprintf("Status: %d\n%s", resp.StatusCode, string(data)), nil
	})
}
