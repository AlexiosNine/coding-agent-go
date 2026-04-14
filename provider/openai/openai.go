// Package openai implements the cc.Provider interface for OpenAI-compatible APIs.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	cc "github.com/alexioschen/cc-connect/goagent"
)

const defaultBaseURL = "https://api.openai.com/v1"

// Provider implements cc.Provider for the OpenAI Chat Completions API.
// It also works with any OpenAI-compatible API (e.g., DeepSeek, Groq, Together).
type Provider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// Option configures an OpenAI Provider.
type Option func(*Provider)

// WithBaseURL overrides the default OpenAI API base URL.
// Use this for OpenAI-compatible providers.
func WithBaseURL(url string) Option {
	return func(p *Provider) { p.baseURL = url }
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.client = c }
}

// New creates a new OpenAI provider with the given API key.
func New(apiKey string, opts ...Option) *Provider {
	p := &Provider{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		client:  http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Chat sends messages to the OpenAI API and returns a complete response.
func (p *Provider) Chat(ctx context.Context, params cc.ChatParams) (*cc.ChatResponse, error) {
	reqBody := apiRequest{
		Model:     params.Model,
		Messages:  toAPIMessages(params.System, params.Messages),
		MaxTokens: params.MaxTokens,
	}
	if len(params.Tools) > 0 {
		reqBody.Tools = toAPITools(params.Tools)
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d: %s", cc.ErrProviderRequest, resp.StatusCode, string(respBody))
	}

	var apiResp apiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("openai: unmarshal response: %w", err)
	}

	return fromAPIResponse(apiResp), nil
}
