package cc

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Envelope is a message passed between agents on the bus.
type Envelope struct {
	From    string // sender agent name
	To      string // target agent name (for req/rep)
	Topic   string // topic name (for pub/sub)
	Payload string // message content
	ReplyTo chan Envelope // reply channel (for req/rep)
}

// MessageBus provides ZeroMQ-style messaging patterns for agent communication.
// All communication is in-process via Go channels — no network dependency.
type MessageBus struct {
	mu          sync.RWMutex
	subscribers map[string][]chan Envelope // topic → subscriber channels
	endpoints   map[string]chan Envelope   // agent name → request channel (req/rep)
	queues      map[string]chan Envelope   // queue name → task channel (push/pull)
}

// NewMessageBus creates a new message bus.
func NewMessageBus() *MessageBus {
	return &MessageBus{
		subscribers: make(map[string][]chan Envelope),
		endpoints:   make(map[string]chan Envelope),
		queues:      make(map[string]chan Envelope),
	}
}

// --- Pub/Sub ---

// Subscribe registers a channel to receive messages on a topic.
// Returns a channel that receives published messages.
func (b *MessageBus) Subscribe(topic string, bufSize int) <-chan Envelope {
	b.mu.Lock()
	defer b.mu.Unlock()

	if bufSize <= 0 {
		bufSize = 64
	}
	ch := make(chan Envelope, bufSize)
	b.subscribers[topic] = append(b.subscribers[topic], ch)
	return ch
}

// Publish sends a message to all subscribers of a topic.
// Non-blocking: if a subscriber's channel is full, the message is dropped for that subscriber.
func (b *MessageBus) Publish(topic string, from string, payload string) int {
	b.mu.RLock()
	subs := b.subscribers[topic]
	b.mu.RUnlock()

	env := Envelope{From: from, Topic: topic, Payload: payload}
	sent := 0
	for _, ch := range subs {
		select {
		case ch <- env:
			sent++
		default:
			// drop if subscriber is full
		}
	}
	return sent
}

// Unsubscribe removes a subscriber channel from a topic and closes it.
func (b *MessageBus) Unsubscribe(topic string, ch <-chan Envelope) {
	b.mu.Lock()
	defer b.mu.Unlock()

	subs := b.subscribers[topic]
	for i, sub := range subs {
		if sub == ch {
			close(sub)
			b.subscribers[topic] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
}

// --- Req/Rep ---

// Register registers an agent as a responder for request/reply.
// Returns a channel that receives incoming requests.
func (b *MessageBus) Register(name string, bufSize int) <-chan Envelope {
	b.mu.Lock()
	defer b.mu.Unlock()

	if bufSize <= 0 {
		bufSize = 16
	}
	ch := make(chan Envelope, bufSize)
	b.endpoints[name] = ch
	return ch
}

// Request sends a request to a named agent and waits for a reply.
// Returns the reply payload or error on timeout/context cancellation.
func (b *MessageBus) Request(ctx context.Context, from, to, payload string) (string, error) {
	b.mu.RLock()
	endpoint, ok := b.endpoints[to]
	b.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("bus: agent %q not registered", to)
	}

	replyCh := make(chan Envelope, 1)
	env := Envelope{From: from, To: to, Payload: payload, ReplyTo: replyCh}

	select {
	case endpoint <- env:
	case <-ctx.Done():
		return "", ctx.Err()
	}

	select {
	case reply := <-replyCh:
		return reply.Payload, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Reply sends a reply to a request envelope.
func Reply(env Envelope, payload string) {
	if env.ReplyTo != nil {
		env.ReplyTo <- Envelope{From: env.To, To: env.From, Payload: payload}
	}
}

// --- Push/Pull (Task Queue) ---

// CreateQueue creates a named task queue with the given buffer size.
func (b *MessageBus) CreateQueue(name string, bufSize int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if bufSize <= 0 {
		bufSize = 256
	}
	if _, exists := b.queues[name]; !exists {
		b.queues[name] = make(chan Envelope, bufSize)
	}
}

// Push adds a task to a named queue.
// Blocks if the queue is full.
func (b *MessageBus) Push(ctx context.Context, queue, from, payload string) error {
	b.mu.RLock()
	ch, ok := b.queues[queue]
	b.mu.RUnlock()

	if !ok {
		return fmt.Errorf("bus: queue %q not found", queue)
	}

	select {
	case ch <- Envelope{From: from, Topic: queue, Payload: payload}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Pull retrieves a task from a named queue.
// Blocks until a task is available or context is cancelled.
func (b *MessageBus) Pull(ctx context.Context, queue string) (Envelope, error) {
	b.mu.RLock()
	ch, ok := b.queues[queue]
	b.mu.RUnlock()

	if !ok {
		return Envelope{}, fmt.Errorf("bus: queue %q not found", queue)
	}

	select {
	case env := <-ch:
		return env, nil
	case <-ctx.Done():
		return Envelope{}, ctx.Err()
	}
}

// QueueLen returns the current number of tasks in a queue.
func (b *MessageBus) QueueLen(queue string) int {
	b.mu.RLock()
	ch, ok := b.queues[queue]
	b.mu.RUnlock()

	if !ok {
		return 0
	}
	return len(ch)
}

// --- Pipeline ---

// Pipeline creates a sequential processing pipeline.
// Each stage is a function that transforms the payload.
// The pipeline runs synchronously: input → stage1 → stage2 → ... → output.
type Pipeline struct {
	stages []PipelineStage
}

// PipelineStage processes a payload and returns the transformed result.
type PipelineStage struct {
	Name string
	Fn   func(ctx context.Context, input string) (string, error)
}

// NewPipeline creates a pipeline with the given stages.
func NewPipeline(stages ...PipelineStage) *Pipeline {
	return &Pipeline{stages: stages}
}

// Run executes the pipeline, passing the output of each stage as input to the next.
func (p *Pipeline) Run(ctx context.Context, input string) (string, error) {
	current := input
	for _, stage := range p.stages {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		result, err := stage.Fn(ctx, current)
		if err != nil {
			return "", fmt.Errorf("pipeline stage %q: %w", stage.Name, err)
		}
		current = result
	}
	return current, nil
}

// AgentStage creates a PipelineStage from an Agent.
// The agent receives the input as a user message and returns its output.
func AgentStage(name string, agent *Agent) PipelineStage {
	return PipelineStage{
		Name: name,
		Fn: func(ctx context.Context, input string) (string, error) {
			result, err := agent.Run(ctx, input)
			if err != nil {
				return "", err
			}
			return result.Output, nil
		},
	}
}

// --- Bus context propagation ---

type busKey struct{}

// WithBus attaches a MessageBus to a context.
func WithBus(ctx context.Context, bus *MessageBus) context.Context {
	return context.WithValue(ctx, busKey{}, bus)
}

// GetBus retrieves the MessageBus from a context.
func GetBus(ctx context.Context) *MessageBus {
	if v := ctx.Value(busKey{}); v != nil {
		return v.(*MessageBus)
	}
	return nil
}

// --- Helper: Fan-out / Fan-in ---

// FanOut runs the same input through multiple agents concurrently and returns all results.
func FanOut(ctx context.Context, input string, agents ...*Agent) ([]string, error) {
	results := make([]string, len(agents))
	errs := make([]error, len(agents))
	var wg sync.WaitGroup

	for i, a := range agents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := a.Run(ctx, input)
			if err != nil {
				errs[i] = err
				return
			}
			results[i] = result.Output
		}()
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

// Timeout wraps a context with a timeout duration.
func Timeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}
