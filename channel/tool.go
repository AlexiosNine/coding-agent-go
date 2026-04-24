package channel

import (
	"context"
	"fmt"

	cc "github.com/alexioschen/cc-connect/goagent"
)

// sendMessageInput is the typed input for the send_message tool.
type sendMessageInput struct {
	Message  string `json:"message" desc:"The message content to send"`
	TargetID string `json:"target_id,omitempty" desc:"Override the default recipient ID (optional)"`
}

// SendMessageTool returns a cc.Tool that lets the Agent proactively send
// messages to an IM platform. defaultTarget is used when target_id is empty.
func SendMessageTool(ch Channel, defaultTarget Target) cc.Tool {
	return cc.NewFuncTool(
		"send_message",
		fmt.Sprintf("Send a message to %s", ch.Name()),
		func(ctx context.Context, in sendMessageInput) (string, error) {
			target := defaultTarget
			if in.TargetID != "" {
				target.ID = in.TargetID
			}
			if err := ch.SendMessage(ctx, target, OutgoingMessage{Content: in.Message}); err != nil {
				return "", err
			}
			return "sent", nil
		},
	)
}
