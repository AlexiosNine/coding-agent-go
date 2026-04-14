package cc

// Memory is the interface for conversation history storage.
type Memory interface {
	// Add appends a message to the history.
	Add(msg Message)
	// Messages returns a copy of all stored messages.
	Messages() []Message
	// Clear removes all stored messages.
	Clear()
}

// BufferMemory is an unbounded memory that stores all messages.
type BufferMemory struct {
	messages []Message
}

// NewBufferMemory creates a new buffer memory.
func NewBufferMemory() *BufferMemory {
	return &BufferMemory{}
}

func (b *BufferMemory) Add(msg Message) {
	b.messages = append(b.messages, msg)
}

func (b *BufferMemory) Messages() []Message {
	out := make([]Message, len(b.messages))
	copy(out, b.messages)
	return out
}

func (b *BufferMemory) Clear() {
	b.messages = nil
}

// WindowMemory keeps only the last N messages.
type WindowMemory struct {
	messages []Message
	size     int
}

// NewWindowMemory creates a sliding window memory that keeps the last n messages.
func NewWindowMemory(n int) *WindowMemory {
	return &WindowMemory{size: n}
}

func (w *WindowMemory) Add(msg Message) {
	w.messages = append(w.messages, msg)
	if len(w.messages) > w.size {
		w.messages = w.messages[len(w.messages)-w.size:]
	}
}

func (w *WindowMemory) Messages() []Message {
	out := make([]Message, len(w.messages))
	copy(out, w.messages)
	return out
}

func (w *WindowMemory) Clear() {
	w.messages = nil
}
