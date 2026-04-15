package cc

import (
	"fmt"
	"hash/fnv"
)

// MessageDeduplicator tracks and replaces duplicate tool result content.
type MessageDeduplicator struct {
	seen map[uint64]int
}

// NewMessageDeduplicator creates a new MessageDeduplicator.
func NewMessageDeduplicator() *MessageDeduplicator {
	return &MessageDeduplicator{seen: make(map[uint64]int)}
}

// Process scans messages and replaces duplicate tool results with references.
// Returns a new slice with duplicates replaced.
func (d *MessageDeduplicator) Process(messages []Message) []Message {
	out := make([]Message, len(messages))
	copy(out, messages)
	for i, msg := range out {
		for j, content := range msg.Content {
			tr, ok := content.(ToolResultContent)
			if !ok {
				continue
			}
			h := hashString(tr.Content)
			if firstIdx, exists := d.seen[h]; exists {
				tr.Content = fmt.Sprintf("[Same result as earlier tool call near message #%d, omitted to save tokens]", firstIdx)
				out[i].Content[j] = tr
				continue
			}
			d.seen[h] = i
		}
	}
	return out
}

func hashString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}
