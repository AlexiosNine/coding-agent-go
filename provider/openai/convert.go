package openai

import (
	"encoding/json"
	"strconv"
	"time"

	cc "github.com/alexioschen/cc-connect/goagent"
)

// apiRequest is the OpenAI Chat Completions API request body.
type apiRequest struct {
	Model    string         `json:"model"`
	Messages []apiMessage   `json:"messages"`
	Tools    []apiTool      `json:"tools,omitempty"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

type apiMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCalls  []apiToolCall   `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type apiToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function apiFunction     `json:"function"`
}

type apiFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type apiTool struct {
	Type     string          `json:"type"`
	Function apiFunctionDef  `json:"function"`
}

type apiFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type apiResponse struct {
	Choices []apiChoice `json:"choices"`
	Usage   apiUsage    `json:"usage"`
}

type apiChoice struct {
	Message      apiMessage `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

type apiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// toAPIMessages converts internal messages to OpenAI API format.
func toAPIMessages(system string, msgs []cc.Message) []apiMessage {
	var out []apiMessage

	// System message
	if system != "" {
		out = append(out, apiMessage{Role: "system", Content: system})
	}

	for _, m := range msgs {
		toolUses := m.ToolUses()
		if len(toolUses) > 0 {
			// Assistant message with tool calls
			calls := make([]apiToolCall, len(toolUses))
			for i, tu := range toolUses {
				calls[i] = apiToolCall{
					ID:   tu.ID,
					Type: "function",
					Function: apiFunction{
						Name:      tu.Name,
						Arguments: string(tu.Input),
					},
				}
			}
			msg := apiMessage{Role: "assistant", Content: m.Text(), ToolCalls: calls}
			out = append(out, msg)
			continue
		}

		// Check for tool results
		hasToolResult := false
		for _, c := range m.Content {
			if tr, ok := c.(cc.ToolResultContent); ok {
				hasToolResult = true
				out = append(out, apiMessage{
					Role:       "tool",
					Content:    tr.Content,
					ToolCallID: tr.ToolUseID,
				})
			}
		}
		if hasToolResult {
			continue
		}

		// Regular text message
		out = append(out, apiMessage{Role: string(m.Role), Content: m.Text()})
	}
	return out
}

// toAPITools converts internal tool definitions to OpenAI API format.
func toAPITools(defs []cc.ToolDef) []apiTool {
	out := make([]apiTool, len(defs))
	for i, d := range defs {
		out[i] = apiTool{
			Type: "function",
			Function: apiFunctionDef{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  d.InputSchema,
			},
		}
	}
	return out
}

// fromAPIResponse converts an OpenAI API response to internal format.
func fromAPIResponse(resp apiResponse) *cc.ChatResponse {
	if len(resp.Choices) == 0 {
		return &cc.ChatResponse{}
	}

	choice := resp.Choices[0]
	var content []cc.Content

	if choice.Message.Content != "" {
		content = append(content, cc.TextContent{Text: choice.Message.Content})
	}

	for _, tc := range choice.Message.ToolCalls {
		content = append(content, cc.ToolUseContent{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: json.RawMessage(tc.Function.Arguments),
		})
	}

	stopReason := choice.FinishReason
	if stopReason == "stop" {
		stopReason = "end_turn"
	}

	return &cc.ChatResponse{
		Content:    content,
		StopReason: stopReason,
		Usage: cc.Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
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