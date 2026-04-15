package cc

import (
	"context"
	"strings"
	"sync"
	"time"
)

// BufferEntry holds the output of a single tool execution.
type BufferEntry struct {
	Lines     []string
	CreatedAt time.Time
	Size      int64
}

// OutputBuffer is a session-scoped cache for tool outputs.
// It stores full outputs and supports pagination without re-execution.
// Uses LRU eviction when size exceeds maxSize.
type OutputBuffer struct {
	mu      sync.RWMutex
	entries map[string]*BufferEntry
	order   []string
	maxSize int64
	size    int64
}

// NewOutputBuffer creates a new OutputBuffer with the given max size.
// If maxSize <= 0, defaults to 50MB.
func NewOutputBuffer(maxSize int64) *OutputBuffer {
	if maxSize <= 0 {
		maxSize = 50 << 20
	}
	return &OutputBuffer{
		entries: make(map[string]*BufferEntry),
		maxSize: maxSize,
	}
}

// Store saves tool output in the buffer.
// If the entry already exists, it's updated and moved to the end of LRU order.
// Evicts oldest entries if total size exceeds maxSize.
func (b *OutputBuffer) Store(id string, content string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	lines := strings.Split(content, "\n")
	size := int64(len(content))

	// Remove old entry size if updating
	if old, ok := b.entries[id]; ok {
		b.size -= old.Size
	}

	b.entries[id] = &BufferEntry{Lines: lines, CreatedAt: time.Now(), Size: size}
	b.order = append(b.order, id)
	b.size += size

	// Evict oldest entries until within budget
	for b.size > b.maxSize && len(b.order) > 0 {
		oldest := b.order[0]
		b.order = b.order[1:]
		if entry, ok := b.entries[oldest]; ok {
			b.size -= entry.Size
			delete(b.entries, oldest)
		}
	}
}

// GetPage returns a page of lines from the stored output.
// Returns (page content, total lines, hasMore).
// Returns ("", 0, false) if the entry does not exist.
func (b *OutputBuffer) GetPage(id string, offset, limit int) (string, int, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	entry := b.entries[id]
	if entry == nil {
		return "", 0, false
	}
	return paginate(entry.Lines, offset, limit)
}

// TryGetPage returns a page of lines, with the third return value indicating
// whether the entry exists (true) or not (false).
// Unlike GetPage, hasMore=true means the entry was found (not that there are more lines).
func (b *OutputBuffer) TryGetPage(id string, offset, limit int) (string, int, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	entry := b.entries[id]
	if entry == nil {
		return "", 0, false
	}
	page, total, _ := paginate(entry.Lines, offset, limit)
	return page, total, true
}

// paginate slices lines and returns (content, total, hasMore).
func paginate(lines []string, offset, limit int) (string, int, bool) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 200
	}
	total := len(lines)
	if offset >= total {
		return "", total, false
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return strings.Join(lines[offset:end], "\n"), total, end < total
}

type outputBufferKey struct{}

// WithOutputBuffer attaches an OutputBuffer to the context.
func WithOutputBuffer(ctx context.Context, buf *OutputBuffer) context.Context {
	return context.WithValue(ctx, outputBufferKey{}, buf)
}

// GetOutputBuffer retrieves the OutputBuffer from the context, or nil if not set.
func GetOutputBuffer(ctx context.Context) *OutputBuffer {
	if v := ctx.Value(outputBufferKey{}); v != nil {
		return v.(*OutputBuffer)
	}
	return nil
}
