package tuie2e

import (
	"strings"
	"testing"
)

// TestGUIBridgeRunsTheActionsTheTableMarks: the bridge's action command runs
// the actions the action table marks GUI. Its hand-written list named the
// resize_width_ and resize_height_ families, but it took one digit after the
// prefix and those actions end in 10 to 90, so the bridge refused them as
// "only a screen can show". It also named nav_left and three more, which no
// action is, and passed them on to a runner that then failed.
//
// How this could pass wrongly, written down first:
//   - The call could succeed and change nothing. With two tiled panes of 50
//     columns, the test waits for a pane at least 60 wide after
//     resize_width_70.
//   - The width could come from somewhere else. Nothing else runs between the
//     two state reads, and the test checks no pane is 60 wide before.
//   - nav_left could be refused for an unrelated reason. The test wants the
//     bridge's own "not an action the bridge runs".
func TestGUIBridgeRunsTheActionsTheTableMarks(t *testing.T) {
	b := startBridge(t, t.TempDir(), "table-actions", 100, 30)
	b.mustCall(map[string]any{"cmd": "action", "name": "new_window"})
	b.mustCall(map[string]any{"cmd": "action", "name": "new_window"})
	st := b.waitState(func(s *wireState) bool { return len(s.Windows) == 2 }, "two panes")
	for _, w := range st.Windows {
		if w.W >= 60 {
			t.Fatalf("a pane is %d wide before any resize; the test needs two halves", w.W)
		}
	}

	r := b.call(map[string]any{"cmd": "action", "name": "resize_width_70"})
	if !r.OK {
		t.Fatalf("ASSERTION: the bridge refused resize_width_70: %s", r.Error)
	}
	b.waitState(func(s *wireState) bool {
		for _, w := range s.Windows {
			if w.W >= 60 {
				return true
			}
		}
		return false
	}, "a pane 60 or more wide after resize_width_70")

	r = b.call(map[string]any{"cmd": "action", "name": "nav_left"})
	if r.OK {
		t.Fatalf("ASSERTION: the bridge ran nav_left, which is no action")
	}
	if !strings.Contains(r.Error, "not an action the bridge runs") {
		t.Errorf("ASSERTION: the bridge refused nav_left with %q, want its own refusal", r.Error)
	}
	b.saveLog("table-actions")
}
