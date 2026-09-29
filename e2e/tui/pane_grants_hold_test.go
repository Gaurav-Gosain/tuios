package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// needPython skips a test whose pane dials a unix socket with python3.
func needPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is needed to dial a socket from a pane")
	}
}

// dialScript writes a python3 script that sends one line to the unix socket
// at sock and prints the reply after marker.
func dialScript(t *testing.T, dir, name, sock, line, marker string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	body := "import socket,sys\n" +
		"s=socket.socket(socket.AF_UNIX)\n" +
		"s.connect(" + pyString(sock) + ")\n" +
		"s.sendall(" + pyString(line+"\n") + ".encode())\n" +
		"print(" + pyString(marker) + ", s.recv(4096).decode().strip()[:160])\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// pyString quotes s as a python string literal.
func pyString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// grantWindowIDs lists a session's windows, the focused one first.
func grantWindowIDs(t *testing.T, base, sess string) []string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
	if err != nil {
		t.Fatalf("list-windows failed: %v\n%s", err, out)
	}
	var listing struct {
		Windows []struct {
			WindowID string `json:"window_id"`
			Focused  bool   `json:"focused"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var ids []string
	for _, w := range listing.Windows {
		if w.Focused {
			ids = append([]string{w.WindowID}, ids...)
		} else {
			ids = append(ids, w.WindowID)
		}
	}
	return ids
}

// TestAStrictPaneCannotTypeThroughTheLinkSocket: a pane under strict with the
// read grant dials the daemon's .link socket itself and asks for a new window.
// The link socket is for the link proxy, so the call is refused and no window
// opens.
//
// Negative control: with the linkFromPane check cut from checkLinkVerb, the
// reply is a result and a second window opens.
func TestAStrictPaneCannotTypeThroughTheLinkSocket(t *testing.T) {
	needPython(t)
	base := t.TempDir()
	killDaemon(t, base)
	cfg := configPathIn(base)
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[agents.permissions]\nmode = \"strict\"\ngrants = [\"read\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new", "e2e-link", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-link"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}

	sock := filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock.link")
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("the daemon has no link socket: %v", err)
	}
	script := dialScript(t, base, "link.py", sock, `{"id":1,"verb":"new-window","params":{"session":"e2e-link","name":"sneaky"}}`, "LINK_REPLY")
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-link", "python3 "+script+"\n"); err != nil {
		t.Fatalf("send-text from outside a pane failed: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "LINK_REPLY") && strings.Contains(text, "forbidden")
	}, uiTimeout); err != nil {
		t.Fatalf("a strict pane's call on the link socket was not refused: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "link-socket-refused")
	if ids := grantWindowIDs(t, base, "e2e-link"); len(ids) != 1 {
		t.Fatalf("a window opened through the link socket: %v", ids)
	}
	alive(t, term, "after a pane was refused on the link socket")
}

// TestAnOpenPaneCannotApproveAnotherPanesPrompt: under the default open mode
// every pane holds admin. A pane that sends "1 Enter" to a sibling waiting on
// a prompt would approve it, so the call is refused and names the respond
// grant. The sibling receives nothing.
//
// Negative control: with the admin early return back in front of the prompt
// check in checkGrants, SK_EXIT=0 appears and the sibling shows the 1.
func TestAnOpenPaneCannotApproveAnotherPanesPrompt(t *testing.T) {
	term, base := attachClientBase(t)
	if out, err := tuiosCLI(t, base, "new-window", "-s", "e2e-ctrlp", "prompted", "--no-focus"); err != nil {
		t.Fatalf("new-window failed: %v\n%s", err, out)
	}
	ids := grantWindowIDs(t, base, "e2e-ctrlp")
	if len(ids) != 2 {
		t.Fatalf("want two windows, got %v", ids)
	}
	sibling := ids[1]
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-ctrlp", "-w", sibling, "needs_input", "--kind", "approval", "--message", "approve Bash: rm -rf build"); err != nil {
		t.Fatalf("set-agent-state from outside a pane failed: %v\n%s", err, out)
	}

	line := tuiosBin + " send-keys -s e2e-ctrlp -w " + sibling + " '1 Enter'; echo SK_EXIT=$?\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", "-w", ids[0], line); err != nil {
		t.Fatalf("send-text from outside a pane failed: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "SK_EXIT=1") && strings.Contains(text, "respond")
	}, uiTimeout); err != nil {
		t.Fatalf("an open pane approved a sibling's prompt: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "open-pane-prompt-refused")

	// The person is not held by it.
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", "-w", sibling, "echo PERSON_TYPED\n"); err != nil {
		t.Fatalf("the person's send-text into a prompt failed: %v\n%s", err, out)
	}
	cap, err := tuiosCLI(t, base, "capture-pane", "-s", "e2e-ctrlp", "-w", sibling)
	deadline := time.Now().Add(uiTimeout)
	for err == nil && !strings.Contains(cap, "PERSON_TYPED") && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		cap, err = tuiosCLI(t, base, "capture-pane", "-s", "e2e-ctrlp", "-w", sibling)
	}
	if err != nil || !strings.Contains(cap, "PERSON_TYPED") {
		t.Fatalf("the person's text never reached the sibling: %v\n%s", err, cap)
	}
	alive(t, term, "after an open pane was refused on a prompt")
}

// TestTheShimHolderChecksTheCallersGrants: a pane opened through the tmux
// shim has a holder that respawns it on request. A pane without admin that
// dials the holder's socket itself, rather than through tuios tmux, is
// refused by the holder, which asks the daemon about the caller's pid.
//
// Negative control: with the authorize call cut from acceptRespawns, the
// reply is ok and the marker file appears.
func TestTheShimHolderChecksTheCallersGrants(t *testing.T) {
	needPython(t)
	term, base := attachClientBase(t)

	out := filepath.Join(base, "shim-out")
	script := filepath.Join(base, "split.sh")
	body := strings.Join([]string{
		"set -e",
		"P=$(tmux split-window -d -h -P -F '#{pane_id}' -- cat)",
		`D=$(dirname "${TMUX%%,*}")`,
		`echo "$D/p/${P#%}.sock" > ` + out,
		"echo SPLIT_DONE",
	}, "\n") + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	line := tuiosBin + " tmux-shim -- sh " + script + ` || echo SPLIT_"FAILED"` + "\n"
	if o, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, o)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "SPLIT_DONE") || strings.Contains(s.Text(), "SPLIT_FAILED")
	}, shellTimeout); err != nil || strings.Contains(term.Screen().Text(), "SPLIT_FAILED") {
		t.Fatalf("split-window through the shim failed: %v\n%s", err, term.Snapshot())
	}
	waitWindowCount(t, term, 2, "after split-window through the shim")
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	sock := strings.TrimSpace(string(raw))
	ids := grantWindowIDs(t, base, "e2e-ctrlp")
	if len(ids) != 2 {
		t.Fatalf("want two windows, got %v", ids)
	}
	caller, held := ids[0], ids[1]

	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", "e2e-ctrlp", "-w", caller, "--grants", "read,write"); err != nil {
		t.Fatalf("set-pane-grants failed: %v\n%s", err, out)
	}
	marker := filepath.Join(base, "respawned")
	req, _ := json.Marshal(map[string]any{"window": held, "command": []string{"touch " + marker}})
	dial := dialScript(t, base, "holder.py", sock, string(req), "HOLDER_REPLY")
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", "-w", caller, "python3 "+dial+"\n"); err != nil {
		t.Fatalf("send-text from outside a pane failed: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "HOLDER_REPLY") && strings.Contains(text, `"ok":false`)
	}, shellTimeout); err != nil {
		t.Fatalf("the holder respawned for a pane without admin: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "shim-holder-refused")
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the refused respawn ran")
	}
	alive(t, term, "after the holder refused a respawn")
}

// TestAnOpenPaneCannotReachAPromptThroughTheClient: send-keys with no window
// used to go through the attached client, where PREFIX moves focus to the
// next window. A pane could move focus to a sibling on a prompt and answer it
// in one call. From a pane without respond the keys go to the focused pane's
// terminal, and PREFIX is refused.
//
// Negative control: with paneTypesRaw back to holding only panes without
// admin, NX_EXIT=0 appears and the sibling receives the 1.
func TestAnOpenPaneCannotReachAPromptThroughTheClient(t *testing.T) {
	term, base := attachClientBase(t)
	if out, err := tuiosCLI(t, base, "new-window", "-s", "e2e-ctrlp", "prompted", "--no-focus"); err != nil {
		t.Fatalf("new-window failed: %v\n%s", err, out)
	}
	ids := grantWindowIDs(t, base, "e2e-ctrlp")
	if len(ids) != 2 {
		t.Fatalf("want two windows, got %v", ids)
	}
	sibling := ids[1]
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-ctrlp", "-w", sibling, "needs_input", "--kind", "approval", "--message", "approve Bash: rm -rf build"); err != nil {
		t.Fatalf("set-agent-state failed: %v\n%s", err, out)
	}
	line := tuiosBin + " send-keys -s e2e-ctrlp 'PREFIX n 1 Enter'; echo NX_EXIT=$?\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", "-w", ids[0], line); err != nil {
		t.Fatalf("send-text failed: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "NX_EXIT=1") }, uiTimeout); err != nil {
		t.Fatalf("a pane's PREFIX sequence was not refused: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "open-pane-prefix-refused")
	if focused := grantWindowIDs(t, base, "e2e-ctrlp")[0]; focused != ids[0] {
		t.Errorf("focus moved to %s", focused)
	}
	alive(t, term, "after a pane's PREFIX was refused")
}
