package tuie2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestArrangeVerb: the daemon runs a layout change itself (parity gap 1,
// step 3; issue #162). A client used to run the layout engine, read the tree
// and write the whole tree and layout state back, so two that changed a
// layout at once each wrote a state built on the one they read and the later
// write dropped the other's change. The arrange verb runs the engine in the
// daemon, one op per session at a time. With no client on a session of six
// tiled panes it switches the layout to master-stack, adds masters, two at
// once from two callers, rotates the split tree and turns tiling off, each
// read back with layout-tree. The transcript goes to arrange-verb.txt.
//
// How this could pass wrongly, written down first:
//   - Two calls at once could run one after the other by chance and land even
//     with no lock. Each round sends both at the same moment from two
//     goroutines, five rounds, and every round must add exactly two.
//   - An op could answer success and change nothing. Each check names a value
//     the step before it did not have.
func TestArrangeVerb(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "arrange"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	var log strings.Builder
	first := func() string {
		out, err := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
		if err != nil {
			t.Fatalf("list-windows: %v\n%s", err, out)
		}
		var l struct {
			Windows []struct {
				ID string `json:"window_id"`
			} `json:"windows"`
		}
		_ = json.Unmarshal([]byte(out), &l)
		return l.Windows[0].ID
	}()
	from := first
	for range 5 {
		from = splitSide(t, base, sess, from, "right", &log)
	}
	arrange := func(params map[string]any) error {
		params["session"] = sess
		b, _ := json.Marshal(params)
		out, err := tuiosCLI(t, base, "call", "arrange", string(b))
		fmt.Fprintf(&log, "arrange %s: err %v %s\n", b, err, strings.TrimSpace(out))
		return err
	}
	type tree struct {
		Mode   string `json:"mode"`
		Tiling bool   `json:"tiling"`
		Master struct {
			Count int `json:"count"`
		} `json:"master"`
		Tree json.RawMessage `json:"tree"`
	}
	read := func(what string) tree {
		t.Helper()
		out, err := tuiosCLI(t, base, "call", "layout-tree", `{"session":"`+sess+`"}`)
		if err != nil {
			t.Fatalf("layout-tree: %v\n%s", err, out)
		}
		var lt tree
		if err := json.Unmarshal([]byte(out), &lt); err != nil {
			t.Fatalf("read layout-tree: %v\n%s", err, out)
		}
		fmt.Fprintf(&log, "  %s: mode %q tiling %v masters %d tree %s\n", what, lt.Mode, lt.Tiling, lt.Master.Count, lt.Tree)
		return lt
	}

	read("six panes split")
	if err := arrange(map[string]any{"op": "mode", "mode": "master-stack"}); err != nil {
		t.Fatalf("arrange mode master-stack: %v", err)
	}
	lt := read("mode master-stack")
	if lt.Mode != "master-stack" || !lt.Tiling {
		t.Fatalf("after mode master-stack: mode %q tiling %v", lt.Mode, lt.Tiling)
	}
	// Four callers, each on a connection of its own opened beforehand, send
	// master.add at the same moment: a lost change shows as fewer masters.
	count := max(lt.Master.Count, 1)
	const callers = 4
	conns := make([]net.Conn, callers)
	for i := range conns {
		c, err := net.Dial("unix", hubSocket(base))
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer c.Close()
		conns[i] = c
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	replies := make([]string, callers)
	for i, c := range conns {
		wg.Go(func() {
			req := fmt.Sprintf(`{"id":%d,"verb":"arrange","params":{"session":%q,"op":"master.add"}}`+"\n", i+1, sess)
			<-start
			if _, err := c.Write([]byte(req)); err != nil {
				replies[i] = "write: " + err.Error()
				return
			}
			line, err := bufio.NewReader(c).ReadString('\n')
			if err != nil {
				replies[i] = "read: " + err.Error()
				return
			}
			replies[i] = strings.TrimSpace(line)
		})
	}
	close(start)
	wg.Wait()
	for i, r := range replies {
		fmt.Fprintf(&log, "caller %d: %s\n", i, r)
		if !strings.Contains(r, `"arranged"`) {
			t.Fatalf("caller %d: master.add did not answer arranged: %s", i, r)
		}
	}
	lt = read("four master.add at once")
	if lt.Master.Count != count+callers {
		t.Fatalf("four master.add at once took the masters from %d to %d, want %d: a change was lost", count, lt.Master.Count, count+callers)
	}

	if err := arrange(map[string]any{"op": "mode", "mode": "bsp"}); err != nil {
		t.Fatalf("arrange mode bsp: %v", err)
	}
	before := read("mode bsp")
	if before.Mode != "bsp" {
		t.Fatalf("after mode bsp: mode %q", before.Mode)
	}
	if err := arrange(map[string]any{"op": "rotate"}); err != nil {
		t.Fatalf("arrange rotate: %v", err)
	}
	after := read("rotate")
	if string(after.Tree) == string(before.Tree) {
		t.Fatalf("rotate left the tree as it was: %s", after.Tree)
	}
	if err := arrange(map[string]any{"op": "tiling"}); err != nil {
		t.Fatalf("arrange tiling: %v", err)
	}
	if lt = read("tiling off"); lt.Tiling {
		t.Fatalf("tiling is still on")
	}
	if err := arrange(map[string]any{"op": "no-such-op"}); err == nil {
		t.Fatalf("an unknown op succeeded")
	}

	if err := os.WriteFile(filepath.Join(artifactDir(t), "arrange-verb.txt"), []byte(log.String()), 0o600); err != nil {
		t.Logf("write the transcript: %v", err)
	}
	t.Log("\n" + log.String())
}
