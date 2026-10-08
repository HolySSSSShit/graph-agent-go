package orchestrator

import (
	"testing"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

func TestCompactThroughCompleteTurn(t *testing.T) {
	messages := []core.Message{
		{Role: "user", Content: "m1"},
		{Role: "assistant", Content: "r1"},
		{Role: "user", Content: "m2"},
		{Role: "assistant", Content: "r2"},
		{Role: "user", Content: "m3"},
	}
	if through := compactThroughCompleteTurn(messages, 4); through != 0 {
		t.Fatalf("through = %d, want 0 when only a user message exceeds the window", through)
	}
	if through := compactThroughCompleteTurn(messages, 2); through != 2 {
		t.Fatalf("through = %d, want complete first turn", through)
	}
}
