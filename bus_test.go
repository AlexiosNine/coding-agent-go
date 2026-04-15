package cc_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	cc "github.com/alexioschen/cc-connect/goagent"
)

func TestBus_PubSub(t *testing.T) {
	bus := cc.NewMessageBus()

	sub1 := bus.Subscribe("events", 10)
	sub2 := bus.Subscribe("events", 10)

	sent := bus.Publish("events", "agent-a", "hello world")
	if sent != 2 {
		t.Errorf("expected 2 subscribers notified, got %d", sent)
	}

	msg1 := <-sub1
	msg2 := <-sub2

	if msg1.Payload != "hello world" {
		t.Errorf("sub1 expected 'hello world', got %q", msg1.Payload)
	}
	if msg2.From != "agent-a" {
		t.Errorf("sub2 expected from 'agent-a', got %q", msg2.From)
	}

	// Unsubscribe
	bus.Unsubscribe("events", sub1)
	sent = bus.Publish("events", "agent-a", "second")
	if sent != 1 {
		t.Errorf("expected 1 subscriber after unsubscribe, got %d", sent)
	}
}

func TestBus_PubSub_NoSubscribers(t *testing.T) {
	bus := cc.NewMessageBus()
	sent := bus.Publish("empty-topic", "agent-a", "nobody listening")
	if sent != 0 {
		t.Errorf("expected 0 sent, got %d", sent)
	}
}

func TestBus_ReqRep(t *testing.T) {
	bus := cc.NewMessageBus()
	ctx := context.Background()

	// Register responder
	inbox := bus.Register("calculator", 10)

	// Start responder goroutine
	go func() {
		req := <-inbox
		if req.Payload != "2+3" {
			t.Errorf("expected '2+3', got %q", req.Payload)
		}
		cc.Reply(req, "5")
	}()

	// Send request
	reply, err := bus.Request(ctx, "user", "calculator", "2+3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "5" {
		t.Errorf("expected '5', got %q", reply)
	}
}

func TestBus_ReqRep_NotRegistered(t *testing.T) {
	bus := cc.NewMessageBus()
	_, err := bus.Request(context.Background(), "a", "nonexistent", "hello")
	if err == nil {
		t.Error("expected error for unregistered agent")
	}
}

func TestBus_ReqRep_Timeout(t *testing.T) {
	bus := cc.NewMessageBus()
	bus.Register("slow", 10) // registered but nobody replies

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := bus.Request(ctx, "a", "slow", "hello")
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestBus_PushPull(t *testing.T) {
	bus := cc.NewMessageBus()
	ctx := context.Background()

	bus.CreateQueue("tasks", 10)

	// Push 3 tasks
	for i := range 3 {
		err := bus.Push(ctx, "tasks", "producer", string(rune('A'+i)))
		if err != nil {
			t.Fatalf("push error: %v", err)
		}
	}

	if bus.QueueLen("tasks") != 3 {
		t.Errorf("expected queue length 3, got %d", bus.QueueLen("tasks"))
	}

	// Pull tasks — should come in order
	for i := range 3 {
		env, err := bus.Pull(ctx, "tasks")
		if err != nil {
			t.Fatalf("pull error: %v", err)
		}
		expected := string(rune('A' + i))
		if env.Payload != expected {
			t.Errorf("expected %q, got %q", expected, env.Payload)
		}
	}
}

func TestBus_PushPull_MultipleWorkers(t *testing.T) {
	bus := cc.NewMessageBus()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	bus.CreateQueue("work", 100)

	// Push 10 tasks
	for i := range 10 {
		_ = bus.Push(ctx, "work", "producer", fmt.Sprintf("task-%d", i))
	}

	// 3 workers pull concurrently
	var mu sync.Mutex
	var results []string
	var wg sync.WaitGroup

	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				env, err := bus.Pull(ctx, "work")
				if err != nil {
					return
				}
				mu.Lock()
				results = append(results, env.Payload)
				done := len(results) >= 10
				mu.Unlock()
				if done {
					cancel() // signal all workers to stop
					return
				}
			}
		}()
	}

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(results) != 10 {
		t.Errorf("expected 10 results, got %d", len(results))
	}
}

func TestPipeline(t *testing.T) {
	ctx := context.Background()

	pipeline := cc.NewPipeline(
		cc.PipelineStage{Name: "upper", Fn: func(_ context.Context, in string) (string, error) {
			return strings.ToUpper(in), nil
		}},
		cc.PipelineStage{Name: "prefix", Fn: func(_ context.Context, in string) (string, error) {
			return "RESULT: " + in, nil
		}},
	)

	out, err := pipeline.Run(ctx, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "RESULT: HELLO" {
		t.Errorf("expected 'RESULT: HELLO', got %q", out)
	}
}

func TestPipeline_WithAgents(t *testing.T) {
	ctx := context.Background()

	agent1 := cc.New(cc.WithProvider(&mockProvider{
		responses: []*cc.ChatResponse{
			{Content: []cc.Content{cc.TextContent{Text: "researched: Go concurrency"}}, StopReason: "end_turn"},
		},
	}), cc.WithModel("test"))

	agent2 := cc.New(cc.WithProvider(&mockProvider{
		responses: []*cc.ChatResponse{
			{Content: []cc.Content{cc.TextContent{Text: "code: func main() {}"}}, StopReason: "end_turn"},
		},
	}), cc.WithModel("test"))

	pipeline := cc.NewPipeline(
		cc.AgentStage("researcher", agent1),
		cc.AgentStage("coder", agent2),
	)

	out, err := pipeline.Run(ctx, "Build a Go app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "code: func main() {}" {
		t.Errorf("expected pipeline output, got %q", out)
	}
}

func TestFanOut(t *testing.T) {
	ctx := context.Background()

	agents := make([]*cc.Agent, 3)
	for i := range 3 {
		agents[i] = cc.New(cc.WithProvider(&mockProvider{
			responses: []*cc.ChatResponse{
				{Content: []cc.Content{cc.TextContent{Text: fmt.Sprintf("result-%d", i)}}, StopReason: "end_turn"},
			},
		}), cc.WithModel("test"))
	}

	results, err := cc.FanOut(ctx, "analyze this", agents...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for i, r := range results {
		expected := fmt.Sprintf("result-%d", i)
		if r != expected {
			t.Errorf("result[%d]: expected %q, got %q", i, expected, r)
		}
	}
}

func TestBus_ContextPropagation(t *testing.T) {
	bus := cc.NewMessageBus()
	ctx := cc.WithBus(context.Background(), bus)

	retrieved := cc.GetBus(ctx)
	if retrieved == nil {
		t.Fatal("expected bus from context")
	}

	// Verify it's the same bus
	retrieved.CreateQueue("test", 1)
	if bus.QueueLen("test") != 0 {
		t.Error("expected same bus instance")
	}
}
