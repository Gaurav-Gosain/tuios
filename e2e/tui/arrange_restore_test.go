package tuie2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestArrangeRestoreByPaneAndOrder drives the arrange verb's restore, which
// guios sends from its rail's minimized rows and its restore keys. Three panes:
// gamma, then alpha are minimized.
//
//   - restore with Delta 1 restores gamma, the pane minimized first: the
//     order the TUI dock lists and its restore digits count in.
//   - restore with Pane restores that pane and focuses it, whatever its place.
//   - restore with Pane set to a pane that is not minimized (a client sends
//     the focused pane on every op) and no Delta restores every one, as
//     before.
//
// Negative control: on guios/land before this change Delta 1 restores alpha
// (window order), and Pane is ignored.
func TestArrangeRestoreByPaneAndOrder(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "arrest"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", sess, "-w", "0", "--name", "alpha"); err != nil {
		t.Fatalf("name: %v\n%s", err, out)
	}
	for _, n := range []string{"beta", "gamma"} {
		if out, err := tuiosCLI(t, base, "new-window", "-s", sess, n, "--no-focus"); err != nil {
			t.Fatalf("new-window %s: %v\n%s", n, err, out)
		}
	}
	type row struct {
		ID        string `json:"window_id"`
		Name      string `json:"display_name"`
		Minimized bool   `json:"minimized"`
	}
	windows := func() map[string]row {
		t.Helper()
		out, err := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
		if err != nil {
			t.Fatalf("list-windows: %v\n%s", err, out)
		}
		var lw struct {
			Windows []row `json:"windows"`
		}
		if err := json.Unmarshal([]byte(out[strings.IndexByte(out, '{'):]), &lw); err != nil {
			t.Fatalf("list-windows json: %v\n%s", err, out)
		}
		m := map[string]row{}
		for _, w := range lw.Windows {
			m[w.Name] = w
		}
		return m
	}
	minimize := func(n string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "set-window", "-s", sess, "-w", n, "--minimize"); err != nil {
			t.Fatalf("minimize %s: %v\n%s", n, err, out)
		}
	}
	// arrange returns the pane the client is to focus, as guios applies it.
	arrange := func(params string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, "call", "arrange", params)
		if err != nil {
			t.Fatalf("arrange %s: %v\n%s", params, err, out)
		}
		var res struct {
			Focus string `json:"focus"`
		}
		if i := strings.IndexByte(out, '{'); i >= 0 {
			_ = json.Unmarshal([]byte(out[i:]), &res)
		}
		return res.Focus
	}
	minimized := func() string {
		var out []string
		for _, n := range []string{"alpha", "beta", "gamma"} {
			if windows()[n].Minimized {
				out = append(out, n)
			}
		}
		return strings.Join(out, ",")
	}

	minimize("gamma")
	minimize("alpha")
	arrange(fmt.Sprintf(`{"session":%q,"op":"restore","delta":1}`, sess))
	if got := minimized(); got != "alpha" {
		t.Fatalf("ASSERTION: restore Delta 1 should restore gamma, minimized first; minimized now %q", got)
	}

	minimize("gamma")
	gamma := windows()["gamma"].ID
	focus := arrange(fmt.Sprintf(`{"session":%q,"op":"restore","pane":%q}`, sess, gamma))
	if got := minimized(); got != "alpha" {
		t.Fatalf("ASSERTION: restore Pane gamma should restore gamma alone; minimized now %q", got)
	}
	if focus != gamma {
		t.Fatalf("ASSERTION: restore Pane gamma should hand the focus to gamma, got %q", focus)
	}

	minimize("gamma")
	beta := windows()["beta"].ID
	arrange(fmt.Sprintf(`{"session":%q,"op":"restore","pane":%q}`, sess, beta))
	if got := minimized(); got != "" {
		t.Fatalf("ASSERTION: restore with a pane that is not minimized should restore every one; minimized now %q", got)
	}
}
