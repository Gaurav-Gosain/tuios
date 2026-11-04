package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestAPaneIsHeldToItsGrants gives the attached session's pane the read grant
// alone, from outside every pane, and then runs in that pane what a
// prompt-injected agent would: type into its own session. The daemon places
// the caller in the pane by its pid, refuses with forbidden and names the
// grant it needed, and the pane's own tuios pane-grants says what it holds.
// The same call from outside every pane, the test process, is still served.
//
// Negative control: with checkGrants in dispatchVerbLine cut, the pane's
// send-text exits 0 and GRANT_EXIT=1 never appears.
func TestAPaneIsHeldToItsGrants(t *testing.T) {
	term, base := attachClientBase(t)

	out, err := tuiosCLI(t, base, "list-windows", "-s", "e2e-ctrlp", "--json")
	if err != nil {
		t.Fatalf("list-windows failed: %v\n%s", err, out)
	}
	var listing struct {
		Windows []struct {
			WindowID string   `json:"window_id"`
			Grants   []string `json:"grants"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil || len(listing.Windows) != 1 {
		t.Fatalf("list-windows gave no single window: %v\n%s", err, out)
	}
	pane := listing.Windows[0].WindowID
	if listing.Windows[0].Grants != nil {
		t.Fatalf("a pane on the default already lists grants: %v", listing.Windows[0].Grants)
	}

	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", "e2e-ctrlp", "-w", pane, "--grants", "read"); err != nil {
		t.Fatalf("set-pane-grants from outside every pane failed: %v\n%s", err, out)
	}

	line := tuiosBin + " send-text -s e2e-ctrlp 'echo typed'; echo GRANT_EXIT=$?; " + tuiosBin + " pane-grants\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", line); err != nil {
		t.Fatalf("send-text from outside a pane failed: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "GRANT_EXIT=1") && strings.Contains(text, "write grant") &&
			strings.Contains(text, "holds read")
	}, uiTimeout); err != nil {
		t.Fatalf("the pane's send-text was not refused for its grants: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "pane-grants-refused")

	out, err = tuiosCLI(t, base, "list-windows", "-s", "e2e-ctrlp", "--json")
	if err != nil {
		t.Fatalf("list-windows failed: %v\n%s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil || len(listing.Windows) != 1 ||
		len(listing.Windows[0].Grants) != 1 || listing.Windows[0].Grants[0] != "read" {
		t.Fatalf("list-windows does not show the pane's grants: %v\n%s", err, out)
	}
	alive(t, term, "after a pane was held to its grants")
}

// TestStrictModeHoldsEveryPane turns on [agents.permissions] strict before the
// daemon starts. The pane then holds the default read, write and fan: opening
// a window needs admin and is refused, and pane-grants says where the grants
// came from. The test process, outside every pane, still opens one.
//
// Negative control: with the config's mode read as open, new-window from the
// pane exits 0 and STRICT_EXIT=1 never appears.
func TestStrictModeHoldsEveryPane(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	cfg := configPathIn(base)
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[agents.permissions]\nmode = \"strict\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new", "e2e-strict", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-strict"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}

	line := tuiosBin + " new-window -s e2e-strict sneaky; echo STRICT_EXIT=$?; " + tuiosBin + " pane-grants\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-strict", line); err != nil {
		t.Fatalf("send-text from outside a pane failed: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "STRICT_EXIT=1") && strings.Contains(text, "admin grant") &&
			strings.Contains(text, "mode strict")
	}, uiTimeout); err != nil {
		t.Fatalf("a strict pane was not held to the default grants: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "pane-grants-strict")

	if out, err := tuiosCLI(t, base, "new-window", "-s", "e2e-strict", "fromoutside", "--no-focus"); err != nil {
		t.Fatalf("new-window from outside every pane failed under strict: %v\n%s", err, out)
	}
	alive(t, term, "after a strict pane was refused")
}

// TestAPaneCannotTypeIntoASiblingThatHoldsMore is the escalation a narrowed
// pane under the default open mode used to have: type a command into a
// sibling shell, which holds admin, and have that shell run it. The first
// pane is given read and write from outside every pane, and its send-text
// into the sibling is refused. Once the sibling holds no more than it, the
// same send-text is served.
//
// Negative control: with holdTypingTarget cut from checkGrants, the first
// send-text exits 0 and SIB_EXIT=1 never appears.
func TestAPaneCannotTypeIntoASiblingThatHoldsMore(t *testing.T) {
	term, base := attachClientBase(t)

	if out, err := tuiosCLI(t, base, "new-window", "-s", "e2e-ctrlp", "sibling", "--no-focus"); err != nil {
		t.Fatalf("new-window failed: %v\n%s", err, out)
	}
	out, err := tuiosCLI(t, base, "list-windows", "-s", "e2e-ctrlp", "--json")
	if err != nil {
		t.Fatalf("list-windows failed: %v\n%s", err, out)
	}
	var listing struct {
		Windows []struct {
			WindowID string `json:"window_id"`
			Focused  bool   `json:"focused"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil || len(listing.Windows) != 2 {
		t.Fatalf("list-windows gave no two windows: %v\n%s", err, out)
	}
	var pane, sibling string
	for _, w := range listing.Windows {
		if w.Focused {
			pane = w.WindowID
		} else {
			sibling = w.WindowID
		}
	}
	if pane == "" || sibling == "" {
		t.Fatalf("could not tell the focused pane from its sibling: %s", out)
	}

	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", "e2e-ctrlp", "-w", pane, "--grants", "read,write"); err != nil {
		t.Fatalf("set-pane-grants failed: %v\n%s", err, out)
	}
	line := tuiosBin + " send-text -s e2e-ctrlp -w " + sibling + " 'echo widened'; echo SIB_EXIT=$?\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", "-w", pane, line); err != nil {
		t.Fatalf("send-text from outside a pane failed: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "SIB_EXIT=1") }, uiTimeout); err != nil {
		t.Fatalf("typing into a sibling that holds admin was not refused: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "pane-grants-sibling-refused")

	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", "e2e-ctrlp", "-w", sibling, "--grants", "read"); err != nil {
		t.Fatalf("set-pane-grants on the sibling failed: %v\n%s", err, out)
	}
	line = tuiosBin + " send-text -s e2e-ctrlp -w " + sibling + " 'echo fine'; echo SIB_EXIT=$?\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", "-w", pane, line); err != nil {
		t.Fatalf("send-text from outside a pane failed: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "SIB_EXIT=0") }, uiTimeout); err != nil {
		t.Fatalf("typing into a sibling that holds less was refused: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after a pane was held to its sibling's grants")
}

// TestAReadPaneRunsGetWindow: tuios get-window is a read. Under strict with
// the read grant alone, the pane's own get-window is served.
//
// Negative control: with get-window sent as the client protocol's GetWindow,
// the pane's call is refused as needing admin and GW_EXIT=0 never appears.
func TestAReadPaneRunsGetWindow(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	cfg := configPathIn(base)
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[agents.permissions]\nmode = \"strict\"\ngrants = [\"read\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new", "e2e-getwin", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-getwin"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}

	line := tuiosBin + " get-window --json >/dev/null; echo GW_EXIT=$?\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-getwin", line); err != nil {
		t.Fatalf("send-text from outside a pane failed: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "GW_EXIT=0") }, uiTimeout); err != nil {
		t.Fatalf("get-window from a read pane was not served: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after a read pane ran get-window")
}

// TestAChildAgentNeverRunsWiderThanItsParent runs a parent agent in a pane:
// a script named claude, started with --permission-mode plan, that calls
// tuios start-agent the way an agent's shell tool does. The daemon reads the
// parent from the caller's process ancestry. A child that asks for bypass
// mode is refused and nothing starts. A child that names no mode starts in
// plan mode, with the flag added. The same bypass child started by the test
// process, outside every pane, starts as asked.
//
// Negative control: with clampChildMode returning argv unchanged, the wide
// child starts (WIDE_EXIT=0) and the plain child's command has no
// --permission-mode plan.
func TestAChildAgentNeverRunsWiderThanItsParent(t *testing.T) {
	base, _ := fanFixture(t)
	if out, err := tuiosCLI(t, base, "new", "plain", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}
	outDir := t.TempDir()
	parentBin := filepath.Join(t.TempDir(), "parent-bin")
	if err := os.MkdirAll(parentBin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"'" + tuiosBin + "' start-agent -s plain 'claude --dangerously-skip-permissions' --name wide --ready-timeout 500 --json >'" + outDir + "/wide.txt' 2>&1\n" +
		"echo \"WIDE_EXIT=$?\" >>'" + outDir + "/wide.txt'\n" +
		"'" + tuiosBin + "' start-agent -s plain claude --name stepped --ready-timeout 500 --json >'" + outDir + "/step.txt' 2>&1\n" +
		"touch '" + outDir + "/done'\n" +
		"exec sleep 600\n"
	parent := filepath.Join(parentBin, "claude")
	if err := os.WriteFile(parent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", "plain", "parent", "--no-focus", "--", parent, "--permission-mode", "plan"); err != nil {
		t.Fatalf("open the parent agent's pane: %v: %s", err, out)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(outDir, "done")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			pane, _ := tuiosCLI(t, base, "capture-pane", "-s", "plain", "-w", "parent")
			t.Fatalf("the parent agent never finished its calls:\n%s", pane)
		}
		time.Sleep(200 * time.Millisecond)
	}
	wide, _ := os.ReadFile(filepath.Join(outDir, "wide.txt"))
	step, _ := os.ReadFile(filepath.Join(outDir, "step.txt"))
	if !strings.Contains(string(wide), "WIDE_EXIT=") || strings.Contains(string(wide), "WIDE_EXIT=0") ||
		!strings.Contains(string(wide), "permission mode") {
		t.Errorf("a child in bypass mode from a parent in plan mode was not refused:\n%s", wide)
	}
	if !strings.Contains(string(step), "--permission-mode plan") {
		t.Errorf("a child that names no mode did not start in the parent's plan mode:\n%s", step)
	}
	windows, err := tuiosCLI(t, base, "list-windows", "-s", "plain")
	if err != nil {
		t.Fatalf("list-windows: %v: %s", err, windows)
	}
	if strings.Contains(windows, "wide") {
		t.Errorf("the refused child has a window:\n%s", windows)
	}

	// The person is never held.
	out, err := tuiosCLI(t, base, "start-agent", "-s", "plain", "claude --dangerously-skip-permissions", "--name", "byperson", "--ready-timeout", "500", "--json")
	if !strings.Contains(out, "--dangerously-skip-permissions") || strings.Contains(out, "forbidden") {
		t.Errorf("the person's start-agent was held: %v\n%s", err, out)
	}
	// The calls and their answers, for a person to read back.
	if dir := os.Getenv("TUIOS_E2E_FRAMES"); dir != "" {
		record := "parent: claude --permission-mode plan\n\nwide child:\n" + string(wide) + "\nchild with no mode:\n" + string(step) + "\nthe person:\n" + out
		if err := os.WriteFile(filepath.Join(dir, "child-mode-clamp.txt"), []byte(record), 0o644); err != nil {
			t.Logf("could not save the record: %v", err)
		}
	}
}
