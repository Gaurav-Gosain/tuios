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

// TestGuiosRailPaneIsTheTUIs runs the guios client (internal/guios/client),
// with no window, against a scratch daemon, through the method the rail's
// pane section calls. The client asks for the session's rail pane running
// a command line, and the pane shows in its state as the session's
// RailPane, keyed by that line. A TUI's rail asks the daemon for the same
// line through rail-pane: it gets the same window, not a second one, and
// the window runs the command.
//
// How this could pass wrongly, written down first:
//   - The state could name a pane the client made some other way. Only
//     rail.pane makes one; the session starts with its shell alone.
//   - The TUI's call could make a second pane with the same name. Its answer
//     must say created false, with the client's window id.
//   - The pane could be made and never run the line. Its screen must show
//     what the command printed.
//
// Negative control: with the client's rail.pane case taken out of its
// dispatch, the child fails at once (unknown method "rail.pane").
func TestGuiosRailPaneIsTheTUIs(t *testing.T) {
	if os.Getenv("TUIOS_E2E_GUIOS_RAILPANE_CHILD") != "" {
		guiosRailPaneChild(os.Getenv("TUIOS_E2E_GUIOS_RAILPANE_CHILD"))
		return
	}
	base := t.TempDir()
	const session = "e2e-guios-rail"
	const line = "printf RAILPANE; sleep 60"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	killDaemon(t, base)

	logPath := filepath.Join(artifactDir(t), "guios-rail-pane-client.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create %s: %v", logPath, err)
	}
	defer logFile.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestGuiosRailPaneIsTheTUIs$", "-test.v")
	child.Dir = workDirIn(t, base)
	child.Env = append(os.Environ(), "TUIOS_E2E_GUIOS_RAILPANE_CHILD="+session)
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
	var railID string
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "railpane "); ok {
			railID = strings.TrimSpace(v)
		}
	}
	if railID == "" {
		t.Fatalf("ASSERTION: the client never saw its rail pane in the state")
	}

	params, _ := json.Marshal(map[string]any{"session": session, "key": line, "command": []string{"env", "sh", "-c", line}, "width": 30, "height": 8})
	out, err := tuiosCLI(t, base, "call", "rail-pane", string(params))
	if err != nil {
		t.Fatalf("rail-pane: %v\n%s", err, out)
	}
	var res struct {
		WindowID string `json:"window_id"`
		Created  bool   `json:"created"`
	}
	_ = json.Unmarshal([]byte(out[strings.IndexByte(out, '{'):]), &res)
	if res.Created || res.WindowID != railID {
		t.Fatalf("ASSERTION: the TUI's rail-pane should get the client's pane %s, got %+v", railID, res)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, _ = tuiosCLI(t, base, "capture-pane", "-s", session, "-w", railID)
		if strings.Contains(out, "RAILPANE") || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(out, "RAILPANE") {
		t.Fatalf("ASSERTION: the rail pane does not show what its command printed:\n%s", out)
	}
}

// guiosRailPaneChild is the client of TestGuiosRailPaneIsTheTUIs.
func guiosRailPaneChild(name string) {
	fail := func(f string, a ...any) {
		fmt.Fprintf(os.Stderr, f+"\n", a...)
		os.Exit(1)
	}
	const line = "printf RAILPANE; sleep 60"
	c, err := guios.Connect(true)
	if err != nil {
		fail("connect: %v", err)
	}
	if err := c.Call("subscribe", nil, nil); err != nil {
		fail("subscribe: %v", err)
	}
	find := func() *proto.Session {
		var st proto.State
		if c.Call("state", nil, &st) != nil {
			return nil
		}
		for i := range st.Sessions {
			if st.Sessions[i].Name == name {
				return &st.Sessions[i]
			}
		}
		return nil
	}
	var s *proto.Session
	for range 100 {
		if s = find(); s != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if s == nil {
		fail("the session %s never showed in the state", name)
	}
	if s.RailPane != nil {
		fail("the session has a rail pane before rail.pane: %+v", s.RailPane)
	}
	if err := c.Call("rail.pane", proto.RailPaneParams{Session: s.ID, Command: line, Cols: 30, Rows: 8}, nil); err != nil {
		fail("rail.pane: %v", err)
	}
	for range 100 {
		if s = find(); s != nil && s.RailPane != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if s == nil || s.RailPane == nil || s.RailKey != line {
		fail("the state never showed the rail pane keyed %q: %+v", line, s)
	}
	fmt.Fprintln(os.Stderr, "railpane", s.RailPane.ID)
}
