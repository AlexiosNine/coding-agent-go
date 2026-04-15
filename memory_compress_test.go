package cc_test

import (
	"encoding/json"
	"strings"
	"testing"

	cc "github.com/alexioschen/cc-connect/goagent"
)

func TestCompressMemory_NoCompressionUnderThreshold(t *testing.T) {
	mem := cc.NewCompressMemory(5, 10)

	for i := range 8 {
		mem.Add(cc.NewUserMessage("msg " + string(rune('A'+i))))
	}

	if mem.Len() != 8 {
		t.Errorf("expected 8 messages (no compression), got %d", mem.Len())
	}
}

func TestCompressMemory_AutoCompress(t *testing.T) {
	mem := cc.NewCompressMemory(4, 10)

	// Add 11 messages to trigger compression
	for i := range 11 {
		if i%2 == 0 {
			mem.Add(cc.NewUserMessage("user turn " + string(rune('A'+i))))
		} else {
			mem.Add(cc.NewAssistantMessage("assistant turn " + string(rune('A'+i))))
		}
	}

	// Should have compressed: [first] + [summary] + [recent 4] = 6
	if mem.Len() > 10 {
		t.Errorf("expected compression to reduce messages, got %d", mem.Len())
	}
}

func TestCompressMemory_PreservesFirstMessage(t *testing.T) {
	mem := cc.NewCompressMemory(3, 8)

	mem.Add(cc.NewUserMessage("IMPORTANT FIRST MESSAGE"))
	for i := range 10 {
		mem.Add(cc.NewAssistantMessage("response " + string(rune('A'+i))))
	}

	msgs := mem.Messages()
	if msgs[0].Text() != "IMPORTANT FIRST MESSAGE" {
		t.Errorf("first message not preserved: got %q", msgs[0].Text())
	}
}

func TestCompressMemory_PreservesRecentMessages(t *testing.T) {
	mem := cc.NewCompressMemory(3, 8)

	mem.Add(cc.NewUserMessage("first"))
	for i := range 5 {
		mem.Add(cc.NewUserMessage("old " + string(rune('A'+i))))
	}
	mem.Add(cc.NewUserMessage("recent-1"))
	mem.Add(cc.NewUserMessage("recent-2"))
	mem.Add(cc.NewUserMessage("recent-3"))

	msgs := mem.Messages()
	n := len(msgs)

	// Last 3 should be the recent messages
	if msgs[n-1].Text() != "recent-3" {
		t.Errorf("expected last message 'recent-3', got %q", msgs[n-1].Text())
	}
	if msgs[n-2].Text() != "recent-2" {
		t.Errorf("expected second-to-last 'recent-2', got %q", msgs[n-2].Text())
	}
	if msgs[n-3].Text() != "recent-1" {
		t.Errorf("expected third-to-last 'recent-1', got %q", msgs[n-3].Text())
	}
}

func TestCompressMemory_SummaryContainsToolInfo(t *testing.T) {
	mem := cc.NewCompressMemory(2, 6)

	mem.Add(cc.NewUserMessage("first"))
	// Add assistant message with tool use
	mem.Add(cc.Message{
		Role: cc.RoleAssistant,
		Content: []cc.Content{
			cc.ToolUseContent{ID: "c1", Name: "shell", Input: json.RawMessage(`{"command":"ls"}`)},
		},
	})
	// Add tool result
	mem.Add(cc.NewToolResultMessage(cc.ToolResultContent{ToolUseID: "c1", Content: "file1.go\nfile2.go"}))
	mem.Add(cc.NewAssistantMessage("I found 2 files"))
	mem.Add(cc.NewUserMessage("recent-1"))
	mem.Add(cc.NewUserMessage("recent-2"))
	mem.Add(cc.NewUserMessage("trigger compression"))

	msgs := mem.Messages()

	// Second message should be the summary
	summary := msgs[1].Text()
	if !strings.Contains(summary, "summary") && !strings.Contains(summary, "Summary") &&
		!strings.Contains(summary, "compressed") {
		t.Errorf("expected summary message, got %q", summary)
	}
	if !strings.Contains(summary, "shell") {
		t.Errorf("expected summary to mention tool 'shell', got %q", summary)
	}
}

func TestCompressMemory_DropsToolResults(t *testing.T) {
	mem := cc.NewCompressMemory(2, 6)

	mem.Add(cc.NewUserMessage("first"))
	mem.Add(cc.NewToolResultMessage(cc.ToolResultContent{ToolUseID: "c1", Content: "LARGE TOOL OUTPUT THAT SHOULD BE DROPPED"}))
	mem.Add(cc.NewAssistantMessage("old response"))
	mem.Add(cc.NewUserMessage("old question"))
	mem.Add(cc.NewUserMessage("recent-1"))
	mem.Add(cc.NewUserMessage("recent-2"))
	mem.Add(cc.NewUserMessage("trigger"))

	msgs := mem.Messages()

	// The large tool output should not appear in any message
	for _, msg := range msgs {
		if strings.Contains(msg.Text(), "LARGE TOOL OUTPUT") {
			t.Error("tool result should have been dropped during compression")
		}
	}
}

func TestCompressMemory_Clear(t *testing.T) {
	mem := cc.NewCompressMemory(3, 10)
	mem.Add(cc.NewUserMessage("hello"))
	mem.Clear()

	if mem.Len() != 0 {
		t.Errorf("expected 0 after clear, got %d", mem.Len())
	}
}

func TestCompressMemory_RepeatedCompression(t *testing.T) {
	mem := cc.NewCompressMemory(3, 8)

	// First batch: trigger compression
	for i := range 10 {
		mem.Add(cc.NewUserMessage("batch1-" + string(rune('A'+i))))
	}
	afterFirst := mem.Len()

	// Second batch: trigger compression again
	for i := range 10 {
		mem.Add(cc.NewUserMessage("batch2-" + string(rune('A'+i))))
	}
	afterSecond := mem.Len()

	if afterSecond > 10 {
		t.Errorf("expected repeated compression to keep messages under threshold, got %d", afterSecond)
	}
	t.Logf("After first compression: %d, after second: %d", afterFirst, afterSecond)
}
