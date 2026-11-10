package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestArrangePlace: the daemon puts a pane its split tree lacks into the
// tree (the arrange verb's place op, parity gap 1, step 3). guios used to
// work the place out in its state build and write the whole tree back,
// which dropped a peer's change made meanwhile. With no client, a session
// of two split panes gets a third from new-window, which the tree does not
// hold; place with the split asked for (below the first pane) puts it there.
// The transcript goes to arrange-place.txt.
//
// How this could pass wrongly, written down first:
//   - The tree could already hold the new pane, from the daemon's own insert,
//     and place do nothing. The test reads the leaves before place and
//     requires the pane missing there.
//   - place could put the pane in, but anywhere. The check is the side asked
//     for: the new pane's split with the first pane is top and bottom, with
//     the first pane on top.
func TestArrangePlace(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "place"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	var log strings.Builder
	type lt struct {
		Leaves map[string]string `json:"leaves"`
		Tree   json.RawMessage   `json:"tree"`
	}
	read := func(what string) lt {
		t.Helper()
		out, err := tuiosCLI(t, base, "call", "layout-tree", `{"session":"`+sess+`"}`)
		if err != nil {
			t.Fatalf("layout-tree: %v\n%s", err, out)
		}
		var l lt
		if err := json.Unmarshal([]byte(out), &l); err != nil {
			t.Fatalf("read layout-tree: %v\n%s", err, out)
		}
		fmt.Fprintf(&log, "  %s: leaves %v tree %s\n", what, l.Leaves, l.Tree)
		return l
	}
	holds := func(l lt, id string) bool {
		for _, v := range l.Leaves {
			if v == id {
				return true
			}
		}
		return false
	}
	out, err := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var lw struct {
		Windows []struct {
			ID string `json:"window_id"`
		} `json:"windows"`
	}
	_ = json.Unmarshal([]byte(out), &lw)
	first := lw.Windows[0].ID
	second := splitSide(t, base, sess, first, "right", &log)
	read("two panes split")

	out, err = tuiosCLI(t, base, "new-window", "-s", sess, "third", "--json")
	if err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	var nw struct {
		ID string `json:"window_id"`
	}
	if err := json.Unmarshal([]byte(out), &nw); err != nil || nw.ID == "" {
		t.Fatalf("new-window gave no id: %v\n%s", err, out)
	}
	before := read("after new-window")
	if holds(before, nw.ID) {
		t.Skipf("the daemon put the new pane in the tree itself; place has nothing to do here: %s", before.Tree)
	}

	params, _ := json.Marshal(map[string]any{"session": sess, "op": "place", "asked": true, "target": first, "mode": "down"})
	out, err = tuiosCLI(t, base, "call", "arrange", string(params))
	fmt.Fprintf(&log, "arrange %s: err %v %s\n", params, err, strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("arrange place: %v\n%s", err, out)
	}
	after := read("place below the first")
	if !holds(after, nw.ID) || !holds(after, first) || !holds(after, second) {
		t.Fatalf("after place the tree lacks a pane: %v", after.Leaves)
	}
	// The first pane and the new one under one top and bottom split, the
	// first on top.
	var root struct {
		Root node `json:"root"`
	}
	if err := json.Unmarshal(after.Tree, &root); err != nil {
		t.Fatalf("read the tree: %v", err)
	}
	num := func(id string) int {
		for k, v := range after.Leaves {
			if v == id {
				var n int
				fmt.Sscan(k, &n)
				return n
			}
		}
		return -1
	}
	if p := root.Root.parentOf(num(first), num(nw.ID)); p == nil || p.SplitType != 2 || p.Left == nil || p.Left.WindowID != num(first) {
		t.Fatalf("the new pane is not below the first pane: %s", after.Tree)
	}

	if err := os.WriteFile(filepath.Join(artifactDir(t), "arrange-place.txt"), []byte(log.String()), 0o600); err != nil {
		t.Logf("write the transcript: %v", err)
	}
}

// node is a split tree node as layout-tree gives it.
type node struct {
	WindowID  int   `json:"window_id"`
	SplitType int   `json:"split_type"`
	Left      *node `json:"left"`
	Right     *node `json:"right"`
}

// parentOf is the split whose two children are the leaves a and b.
func (n *node) parentOf(a, b int) *node {
	if n == nil || n.Left == nil || n.Right == nil {
		return nil
	}
	leaf := func(c *node, id int) bool { return c.Left == nil && c.Right == nil && c.WindowID == id }
	if leaf(n.Left, a) && leaf(n.Right, b) || leaf(n.Left, b) && leaf(n.Right, a) {
		return n
	}
	if p := n.Left.parentOf(a, b); p != nil {
		return p
	}
	return n.Right.parentOf(a, b)
}
