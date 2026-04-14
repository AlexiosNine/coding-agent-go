package cc

import (
	"io"
	"sync"
)

// StreamEvent represents a single event in a streaming response.
type StreamEvent struct {
	Type    string          // "text_delta", "tool_use", "message_stop", "error"
	Text    string          // for text_delta events
	ToolUse *ToolUseContent // for tool_use events (complete, after accumulation)
	Usage   Usage           // for message_stop events
	Error   error           // for error events
}

// StreamReader reads streaming events using an iterator pattern.
type StreamReader struct {
	ch     chan StreamEvent
	cancel func()
	once   sync.Once
}

// NewStreamReader creates a StreamReader. The producer writes events to the
// returned channel and closes it when done. Cancel is called on Close().
func NewStreamReader(bufSize int, cancel func()) (*StreamReader, chan<- StreamEvent) {
	ch := make(chan StreamEvent, bufSize)
	return &StreamReader{ch: ch, cancel: cancel}, ch
}

// Next returns the next event. Returns io.EOF when the stream is complete.
func (r *StreamReader) Next() (StreamEvent, error) {
	ev, ok := <-r.ch
	if !ok {
		return StreamEvent{}, io.EOF
	}
	if ev.Error != nil {
		return ev, ev.Error
	}
	return ev, nil
}

// Close cancels the stream and drains remaining events.
func (r *StreamReader) Close() error {
	r.once.Do(func() {
		if r.cancel != nil {
			r.cancel()
		}
		for range r.ch {
		}
	})
	return nil
}

// Collect reads all events and assembles them into a ChatResponse.
func (r *StreamReader) Collect() (*ChatResponse, error) {
	var contents []Content
	var usage Usage
	var hasToolUse bool

	for {
		ev, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch ev.Type {
		case "text_delta":
			if len(contents) == 0 {
				contents = append(contents, TextContent{})
			}
			if tc, ok := contents[len(contents)-1].(TextContent); ok {
				contents[len(contents)-1] = TextContent{Text: tc.Text + ev.Text}
			} else {
				contents = append(contents, TextContent{Text: ev.Text})
			}
		case "tool_use":
			if ev.ToolUse != nil {
				hasToolUse = true
				contents = append(contents, *ev.ToolUse)
			}
		case "message_stop":
			usage = ev.Usage
		}
	}

	stopReason := "end_turn"
	if hasToolUse {
		stopReason = "tool_use"
	}

	return &ChatResponse{
		Content:    contents,
		StopReason: stopReason,
		Usage:      usage,
	}, nil
}
