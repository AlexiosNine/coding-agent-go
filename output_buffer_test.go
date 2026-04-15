package cc_test

import (
	"strings"
	"testing"

	cc "github.com/alexioschen/cc-connect/goagent"
)

func TestOutputBuffer_StoreAndPaginate(t *testing.T) {
	buf := cc.NewOutputBuffer(1 << 20)
	content := strings.Join([]string{"a", "b", "c", "d", "e"}, "\n")
	buf.Store("tool-1", content)

	page, total, hasMore := buf.GetPage("tool-1", 0, 2)
	if total != 5 {
		t.Fatalf("expected total 5, got %d", total)
	}
	if !hasMore {
		t.Fatal("expected hasMore=true for first page")
	}
	if page != "a\nb" {
		t.Fatalf("expected first page 'a\\nb', got %q", page)
	}

	page, total, hasMore = buf.GetPage("tool-1", 2, 2)
	if total != 5 {
		t.Fatalf("expected total 5, got %d", total)
	}
	if !hasMore {
		t.Fatal("expected hasMore=true for middle page")
	}
	if page != "c\nd" {
		t.Fatalf("expected middle page 'c\\nd', got %q", page)
	}
}

func TestOutputBuffer_EvictsWhenOverSize(t *testing.T) {
	buf := cc.NewOutputBuffer(20)
	buf.Store("tool-1", "1234567890")
	buf.Store("tool-2", "abcdefghij")
	buf.Store("tool-3", "zzzzzzzzzz")

	if _, _, ok := buf.TryGetPage("tool-1", 0, 10); ok {
		t.Fatal("expected oldest entry to be evicted")
	}
}
