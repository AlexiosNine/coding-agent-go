package cc_test

import (
	"testing"

	cc "github.com/alexioschen/cc-connect/goagent"
)

func TestMessageDeduplicator_ReplacesDuplicateToolResults(t *testing.T) {
	d := cc.NewMessageDeduplicator()
	messages := []cc.Message{
		cc.NewUserMessage("find errors"),
		cc.NewToolResultMessage(cc.ToolResultContent{ToolUseID: "a1", Content: "same output"}),
		cc.NewToolResultMessage(cc.ToolResultContent{ToolUseID: "a2", Content: "same output"}),
	}
	out := d.Process(messages)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out))
	}
	if out[2].Text() == "same output" {
		t.Fatal("expected duplicate tool result to be replaced")
	}
}

func TestMessageDeduplicator_DoesNotTouchUserMessages(t *testing.T) {
	d := cc.NewMessageDeduplicator()
	messages := []cc.Message{
		cc.NewUserMessage("same"),
		cc.NewUserMessage("same"),
	}
	out := d.Process(messages)
	if out[0].Text() != "same" || out[1].Text() != "same" {
		t.Fatal("expected user messages to be unchanged")
	}
}
