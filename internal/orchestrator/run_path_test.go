package orchestrator

import (
	"testing"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

func TestAppendRouteTargetPreservesRepeatedNodes(t *testing.T) {
	path := []core.NodeType{core.NodeTypeReason, core.NodeTypeToolExecute}
	path = appendRouteTarget(path, core.NodeTypeReason)
	if len(path) != 3 || path[2] != core.NodeTypeReason {
		t.Fatalf("route target should preserve a legitimate loop: %#v", path)
	}
	path = appendRouteTarget(path, core.NodeTypeReason)
	if len(path) != 3 {
		t.Fatalf("duplicate target should not be appended twice: %#v", path)
	}
}

func TestAppendRouteTargetIgnoresEmptyTarget(t *testing.T) {
	path := []core.NodeType{core.NodeTypeReason}
	got := appendRouteTarget(path, "")
	if len(got) != 1 {
		t.Fatalf("empty route target changed path: %#v", got)
	}
}
