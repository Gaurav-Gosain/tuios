package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitTalkedWith checks the Talked with line under the focus header.
// alpha mails beta, gamma, delta and eps through the real CLI, in that
// order, and beta replies last. zeta has no mail.
//
//   - At 160 columns alpha's header has a chip per agent, the newest
//     exchange first: beta, eps, delta, gamma, each with its count.
//   - At 80 columns the chips that do not fit fold into a "+N" chip, and a
//     click on it opens a menu of the rest.
//   - A click on beta's chip selects beta, and ctrl+o goes back to alpha.
//   - zeta, with no mail, has no Talked with line.
//
// The artifacts are the frames talked-with-wide, talked-with-narrow and
// talked-with-more.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - on a build without the line (origin/main), the test fails
//     at "no Talked with line".
//   - with the sort by the newest exchange cut from ckTalkedWith, the test
//     fails at "the chips are not newest first".
func TestCockpitTalkedWith(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "")
	cli := func(args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	names := []string{"beta", "gamma", "delta", "eps", "zeta"}
	cli("new", "api", "--detach")
	for _, n := range names {
		cli("new-window", "-s", "api", n, "--no-focus")
	}
	var ids map[string]string
	waitTextOf(t, "the panes", shellTimeout, func() string {
		out, _ := tuiosCLI(t, base, "list-windows", "-s", "api", "--json")
		return out
	}, func(out string) bool {
		ids = mailWindowIDsOf(out)
		return len(ids) == len(names)+1 && ids["zeta"] != ""
	})
	for name, id := range ids {
		if !strings.Contains(strings.Join(names, " "), name) {
			ids["alpha"] = id
		}
	}
	cli("set-window", "-s", "api", "-w", ids["alpha"], "--name", "alpha")
	alpha := ids["alpha"]
	for _, n := range []string{"beta", "gamma", "delta", "eps"} {
		cli("send-agent-message", "-s", "api", "-w", n, "--from", alpha, "hello "+n)
	}
	first := cli("send-agent-message", "-s", "api", "-w", "beta", "--from", alpha, "and again")
	cli("send-agent-message", "-s", "api", "-w", alpha, "--from", ids["beta"], "--reply-to", mailFirstID(t, first), "got both")
	for _, n := range append([]string{"alpha"}, names...) {
		state := "idle"
		if n == "alpha" {
			state = "working"
		}
		cli("set-agent-state", state, "-s", "api", "-w", n, "--harness", "claude-code")
	}

	open := func(cols int) *tuitest.Terminal {
		term := startIn(t, base, startOpts{args: []string{"attach", "api"}, cols: cols, rows: 40, shippedLooks: true,
			env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
		if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
			t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
		}
		windowManagementMode(t, term)
		if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
			t.Fatalf("open the agents view: %v", err)
		}
		waitText(t, term, "the agents view", "alpha")
		return term
	}

	term := open(160)
	err := term.WaitForText("Talked with", uiTimeout)
	_ = term.WaitStable(uiTimeout)
	// Saved first, so a build without the line leaves its frame too.
	saveCockpitFrames(t, term, "talked-with-wide")
	if err != nil {
		t.Fatalf("ASSERTION: no Talked with line in alpha's header: %v\n%s", err, term.Snapshot())
	}
	row, _ := findText(t, term, "Talked with")
	line := term.Screen().Line(row)
	at := func(s string) int { return strings.Index(line, s) }
	if at("beta 3") < 0 || at("eps 1") < 0 || at("delta 1") < 0 || at("gamma 1") < 0 {
		t.Fatalf("ASSERTION: the line does not have a chip with its count for each agent: %q\n%s", line, term.Snapshot())
	}
	if !(at("beta") < at("eps") && at("eps") < at("delta") && at("delta") < at("gamma")) {
		t.Fatalf("ASSERTION: the chips are not newest first: %q\n%s", line, term.Snapshot())
	}
	// A click on beta's chip selects beta; ctrl+o goes back.
	mouseClick(t, term, len([]rune(line[:at("beta 3")])), row, tuitest.MouseLeft, 0)
	waitTextFor(t, term, "beta's header", uiTimeout, "alpha 3")
	if err := term.SendKeys(tuitest.Ctrl('o')); err != nil {
		t.Fatal(err)
	}
	waitTextFor(t, term, "back on alpha", uiTimeout, "beta 3")
	term.Close()

	term = open(80)
	// At 80 columns the list and the focus share the screen; l opens the
	// focus when the screen is narrow.
	if !strings.Contains(term.Screen().Text(), "Talked with") {
		if err := term.SendKeys("l"); err != nil {
			t.Fatal(err)
		}
	}
	if err := term.WaitForText("Talked with", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: no Talked with line at 80 columns: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "talked-with-narrow")
	row, _ = findText(t, term, "Talked with")
	line = term.Screen().Line(row)
	plus := strings.Index(line, " +")
	if plus < 0 || strings.Contains(line, "gamma") {
		t.Fatalf("ASSERTION: at 80 columns the chips that do not fit are not folded into +N: %q\n%s", line, term.Snapshot())
	}
	mouseClick(t, term, len([]rune(line[:plus]))+2, row, tuitest.MouseLeft, 0)
	waitTextFor(t, term, "the menu of the rest", uiTimeout, "gamma")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "talked-with-more")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	term.Close()

	// zeta has no mail: no line.
	term = open(160)
	if err := term.SendKeys("/"); err != nil {
		t.Fatal(err)
	}
	if err := term.Type("zeta"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	waitTextFor(t, term, "zeta selected", uiTimeout, "zeta")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "alpha ") || strings.Contains(s.Text(), "zeta")
	}, uiTimeout); err != nil {
		t.Fatal(err)
	}
	_ = term.WaitStable(uiTimeout)
	if strings.Contains(term.Screen().Text(), "Talked with") {
		t.Fatalf("ASSERTION: zeta, with no mail, has a Talked with line\n%s", term.Snapshot())
	}
	alive(t, term, "after the Talked with line")
}
