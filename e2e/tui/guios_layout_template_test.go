package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	guios "github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// TestGuiosSavesAndLoadsALayout runs the guios client (internal/guios/client),
// with no window, against a scratch daemon, through the methods the Layout
// menu's Save layout… and Load layout… call. The client saves the two-pane
// workspace, closes the second pane, loads the layout, lists it, and deletes
// it. The daemon then holds the second pane again, with its name and its
// folder, and the layout file is gone.
//
// How this could pass wrongly, written down first:
//   - The second pane could be the old one. Its window id is checked to be
//     new, and the load to have opened one pane.
//   - The list could come from a file the test wrote. Only the client's save
//     writes one, and the list is checked for its two panes.
//
// Negative control: with the client's layout methods taken out of its
// dispatch, the child fails at once (unknown method "layout.save").
func TestGuiosSavesAndLoadsALayout(t *testing.T) {
	if os.Getenv("TUIOS_E2E_GUIOS_LAYOUT_CHILD") != "" {
		guiosLayoutChild(os.Getenv("TUIOS_E2E_GUIOS_LAYOUT_CHILD"))
		return
	}
	base := t.TempDir()
	const session = "e2e-guios-lay"
	right := filepath.Join(base, "work-right")
	if err := os.MkdirAll(right, 0o755); err != nil {
		t.Fatal(err)
	}
	if r, err := filepath.EvalSymlinks(right); err == nil {
		right = r
	}
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "split-window", "right", "-s", session, "--name", "right", "--cwd", right, "--no-focus"); err != nil {
		t.Fatalf("split-window: %v\n%s", err, out)
	}
	before := xpanesRowsIn(t, base, session)
	if len(before) != 2 {
		t.Fatalf("the session has %d panes, want 2", len(before))
	}
	oldRight := ""
	for _, r := range before {
		if r.Name == "right" {
			oldRight = r.ID
		}
	}

	logPath := filepath.Join(artifactDir(t), "guios-layout-client.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create %s: %v", logPath, err)
	}
	defer logFile.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestGuiosSavesAndLoadsALayout$", "-test.v")
	child.Dir = workDirIn(t, base)
	child.Env = append(os.Environ(), "TUIOS_E2E_GUIOS_LAYOUT_CHILD="+session+" "+oldRight)
	for _, key := range xdgKeys {
		child.Env = append(child.Env, key+"="+xdgDir(base, key))
	}
	child.Stdout, child.Stderr = logFile, logFile
	done := make(chan error, 1)
	if err := child.Start(); err != nil {
		t.Fatalf("start the guios client: %v", err)
	}
	go func() { done <- child.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			b, _ := os.ReadFile(logPath)
			t.Fatalf("ASSERTION: the guios client failed: %v\n%s", err, b)
		}
	case <-time.After(60 * time.Second):
		_ = child.Process.Kill()
		b, _ := os.ReadFile(logPath)
		t.Fatalf("the guios client never finished:\n%s", b)
	}
	b, _ := os.ReadFile(logPath)
	t.Logf("client log:\n%s", b)

	after := xpanesRowsIn(t, base, session)
	var got *xpanesRow
	for i := range after {
		if after[i].Name == "right" {
			got = &after[i]
		}
	}
	if len(after) != 2 || got == nil {
		t.Fatalf("ASSERTION: after the load the session should have its two panes, one named right: %+v", after)
	}
	if got.ID == oldRight {
		t.Fatalf("ASSERTION: the right pane is the old one, so the load opened nothing")
	}
	out, err := tuiosCLI(t, base, "list-windows", "-s", session, "--json")
	if err != nil || !strings.Contains(out, `"cwd": "`+right+`"`) && !strings.Contains(out, `"cwd":"`+right+`"`) {
		t.Errorf("ASSERTION: the loaded right pane is not in its folder %s: %v\n%s", right, err, out)
	}
	if _, err := os.Stat(filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "layouts", "gpair.json")); err == nil {
		t.Errorf("ASSERTION: layout.delete left the layout file")
	}
}

// guiosLayoutChild is the client of TestGuiosSavesAndLoadsALayout.
func guiosLayoutChild(args string) {
	fail := func(f string, a ...any) {
		fmt.Fprintf(os.Stderr, f+"\n", a...)
		os.Exit(1)
	}
	parts := strings.Fields(args)
	if len(parts) != 2 {
		fail("want session and pane, got %q", args)
	}
	name, right := parts[0], parts[1]
	c, err := guios.Connect(true)
	if err != nil {
		fail("connect: %v", err)
	}
	if err := c.Call("subscribe", nil, nil); err != nil {
		fail("subscribe: %v", err)
	}
	sid := ""
	for range 100 {
		var st proto.State
		if c.Call("state", nil, &st) == nil {
			for _, s := range st.Sessions {
				if s.Name == name {
					sid = s.ID
				}
			}
		}
		if sid != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if sid == "" {
		fail("the session %s never showed in the state", name)
	}
	p := proto.LayoutTemplateParams{Session: sid, Name: "gpair"}
	if err := c.Call("layout.save", p, nil); err != nil {
		fail("layout.save: %v", err)
	}
	fmt.Fprintln(os.Stderr, "saved gpair")
	if _, err := c.Verb("close-window", map[string]any{"session": name, "window": right}, false); err != nil {
		fail("close-window: %v", err)
	}
	if err := c.Call("layout.load", p, nil); err != nil {
		fail("layout.load: %v", err)
	}
	fmt.Fprintln(os.Stderr, "loaded gpair")
	var ls []proto.SavedLayout
	if err := c.Call("layout.list", proto.LayoutTemplateParams{Session: sid}, &ls); err != nil {
		fail("layout.list: %v", err)
	}
	b, _ := json.Marshal(ls)
	fmt.Fprintln(os.Stderr, "list", string(b))
	if len(ls) != 1 || ls[0].Name != "gpair" || ls[0].Panes != 2 || ls[0].SavedAt == 0 {
		fail("the list should hold gpair with two panes and its time: %s", b)
	}
	if err := c.Call("layout.delete", p, nil); err != nil {
		fail("layout.delete: %v", err)
	}
	fmt.Fprintln(os.Stderr, "deleted gpair")
}
