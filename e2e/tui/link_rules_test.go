package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// Link rules turn plain text into a link whose target is built from the
// match ([[links.rules]], issue #1583). These tests print a ticket ID in a
// pane and drive the click the way the outer terminal reports it, then read
// what tuios did: the argument the recording opener was started with, and the
// OSC 8 sequences in the frame sent to the outer terminal.

const (
	ruleTicket  = "PROJ-1234"
	ruleTarget  = "https://jira.example.com/browse/PROJ-1234"
	ruleInURL   = "https://example.com/PROJ-77/page"
	ruleSkipped = "SKIP-5"
)

// linkRulesConfig has the ticket rule, and a rule whose target a click would
// refuse, which must never become a link.
const linkRulesConfig = `
[[links.rules]]
regex = '\b(PROJ-[1-9][0-9]*)\b'
url = "https://jira.example.com/browse/$1"

[[links.rules]]
regex = '\bSKIP-[0-9]+\b'
url = "javascript:alert('$0')"
`

// linkRulesSetup starts tuios with the recording opener and the rules, prints
// the fixture, and goes back to window management.
func linkRulesSetup(t *testing.T) (*tuitest.Terminal, *lockedBuffer, string) {
	t.Helper()
	base := t.TempDir()
	record := filepath.Join(base, "opened.txt")
	opener := filepath.Join(base, "opener.sh")
	if err := os.WriteFile(opener, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> '"+record+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The fixture is printed from a file, so the IDs are on screen once, in
	// the output, and never in a typed command line.
	script := filepath.Join(base, "tickets.sh")
	body := "#!/bin/sh\n" +
		fmt.Sprintf("printf 'T: ticket %s done\\n'\n", ruleTicket) +
		fmt.Sprintf("printf 'U: see %s now\\n'\n", ruleInURL) +
		fmt.Sprintf("printf 'S: refused %s here\\n'\n", ruleSkipped) +
		"printf 'TICKETS''DONE\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, fmt.Sprintf("[appearance]\nlink_opener = %q\n%s", opener, linkRulesConfig))

	out := &lockedBuffer{}
	term := startIn(t, base, startOpts{out: out, env: []string{"SSH_CONNECTION=", "SSH_CLIENT=", "SSH_TTY="}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell for the tickets")
	enterTerminalMode(t, term)
	runInShell(t, term, "sh "+script, "TICKETSDONE", uiTimeout)
	leaveTerminalMode(t, term)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v\n%s", err, term.Snapshot())
	}
	return term, out, record
}

// TestCtrlClickOpensATicketIDThroughALinkRule: a ticket ID opens the address
// its rule builds, a URL that holds an ID opens as the URL, and a rule whose
// target a click would refuse makes no link.
//
// Negative controls: with appendBareSpans and bareLinkAt passed nil rules,
// the first wait fails with the opener never started and the OSC 8 assertion
// for the ticket fails; with the URL overlap check in hints.Links removed,
// the second click opens the rule's target for PROJ-77 instead of the URL.
func TestCtrlClickOpensATicketIDThroughALinkRule(t *testing.T) {
	term, out, record := linkRulesSetup(t)

	col, row := mustFind(t, term, ruleTicket)
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, tuitest.ModCtrl)
	waitOpened(t, record, []string{ruleTarget}, "ctrl+click on a ticket ID")

	// A real URL wins over a rule that matches inside it.
	col, row = mustFind(t, term, "PROJ-77")
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, tuitest.ModCtrl)
	waitOpened(t, record, []string{ruleTarget, ruleInURL}, "ctrl+click on an ID inside a URL")

	// A rule whose target is not openable makes no link. The ticket clicked
	// after it is the barrier that proves this click was handled first.
	col, row = mustFind(t, term, ruleSkipped)
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, tuitest.ModCtrl)
	col, row = mustFind(t, term, ruleTicket)
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, tuitest.ModCtrl)
	waitOpened(t, record, []string{ruleTarget, ruleInURL, ruleTarget}, "a refused rule, then the ticket again")
	if n := countWindows(term.Screen()); n != 1 {
		t.Errorf("a link opened a pane; there are %d panes, want 1:\n%s", n, term.Snapshot())
	}

	// The frame carries the ticket as OSC 8 with the built target, so the
	// outer terminal's own link handling opens the same address, and the
	// refused rule is not in it.
	stream := out.String()
	ticket := regexp.MustCompile(`\x1b\]8;id=tuios-[0-9a-z]+;` + regexp.QuoteMeta(ruleTarget) + `(\x07|\x1b\\)` + ruleTicket)
	if !ticket.MatchString(stream) {
		t.Errorf("the frame sent to the outer terminal has no OSC 8 for the ticket ID")
	}
	if regexp.MustCompile(`\x1b\]8;[^;\x07\x1b]*;javascript:`).MatchString(stream) {
		t.Errorf("the frame sent to the outer terminal carries the refused rule's javascript: target")
	}
	alive(t, term, "after opening ticket links")
}
