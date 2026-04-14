package anthropic

import (
	"encoding/json"
	"strconv"
	"time"

	cc "github.com/alexioschen/cc-connect/goagent"
)

// apiRequest is the Anthropic Messages API request body.
type apiRequest struct {
	Model     string       `json:"model"`
	System    string       `json:"system,omitempty"`
	Messages  []apiMessage `json:"messages"`
	MaxTokens int          `json:"max_tokens"`
	Tools     []apiTool    `json:"tools,omitempty"`
}

type apiMessage struct {
	Role    string           `json:"role"`
	Content json.RawMessage  `json:"content"`
}

type apiContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result fields
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type apiTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type apiResponse struct {
	Content    []apiContentBlock `json:"content"`
	StopReason string            `json:"stop_reason"`
	Usage      apiUsage          `json:"usage"`
}

type apiUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// toAPIMessages converts internal messages to Anthropic API format.
func toAPIMessages(msgs []cc.Message) []apiMessage {
	out := make([]apiMessage, 0, len(msgs))
	for _, m := range msgs {
		blocks := make([]apiContentBlock, 0, len(m.Content))
		for _, c := range m.Content {
			switch v := c.(type) {
			case cc.TextContent:
				blocks = append(blocks, apiContentBlock{Type: "text", Text: v.Text})
			case cc.ToolUseContent:
				blocks = append(blocks, apiContentBlock{Type: "tool_use", ID: v.ID, Name: v.Name, Input: v.Input})
			case cc.ToolResultContent:
				blocks = append(blocks, apiContentBlock{Type: "tool_result", ToolUseID: v.ToolUseID, Content: v.Content, IsError: v.IsError})
			}
		}
		data, _ := json.Marshal(blocks)
		out = append(out, apiMessage{Role: string(m.Role), Content: data})
	}
	return out
}

// toAPITools converts internal tool definitions to Anthropic API format.
func toAPITools(defs []cc.ToolDef) []apiTool {
	out := make([]apiTool, len(defs))
	for i, d := range defs {
		out[i] = apiTool{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema}
	}
	return out
}

// fromAPIResponse converts an Anthropic API response to internal format.
func fromAPIResponse(resp apiResponse) *cc.ChatResponse {
	content := make([]cc.Content, 0, len(resp.Content))
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			content = append(content, cc.TextContent{Text: b.Text})
		case "tool_use":
			content = append(content, cc.ToolUseContent{ID: b.ID, Name: b.Name, Input: b.Input})
		}
	}
	return &cc.ChatResponse{
		Content:    content,
		StopReason: resp.StopReason,
		Usage:      cc.Usage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens},
	}
}

// parseRetryAfter parses the Retry-After header value.
func parseRetryAfter(val string) time.Duration {
	if val == "" {
		return 0
	}
	if secs, err := strconv.Atoi(val); err == nil {
		return time.Duration(secs) * time.Second
	}
	return 0
}