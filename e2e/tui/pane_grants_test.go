package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
