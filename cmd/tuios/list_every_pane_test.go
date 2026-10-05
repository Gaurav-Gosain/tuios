package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// A pane on another machine names itself, and its screen is that machine's
// to write, so list-windows --all-hosts prints neither as it came: an OSC 52
// in a name would set this terminal's clipboard, and a CSI in the text would
// clear or move this terminal's screen. Both are laundered, and the host's
// text is fenced as untrusted, as capture-pane fences it. A pane on this
// machine is laundered too: its title is what its program set.
func TestListWindowsAllHostsPrintsHostFieldsPlain(t *testing.T) {
	const osc52 = "\x1b]52;c;cm0gLXJmIH4=\x07"
	const csi = "\x1b[2J\x1b[H"
	panes := []listedPane{
		{Host: "build", Untrusted: true, Session: "api" + csi, Workspace: 1, WindowID: "w1",
			Name: "evil" + osc52, Command: "vim" + csi, Cwd: "/srv" + osc52,
			Text: []string{"line one" + csi, "line two" + osc52}},
		{Session: "home", Workspace: 1, WindowID: "w2", Name: "local" + osc52, Text: []string{"here" + csi}},
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	perr := printListedPanes(panes, []listError{{Host: "far" + osc52, Error: "down" + csi}}, 5, false)
	_ = w.Close()
	os.Stdout = stdout
	out, _ := io.ReadAll(r)
	if perr != nil {
		t.Fatal(perr)
	}
	got := string(out)
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("an escape reached the terminal:\n%q", got)
	}
	for _, want := range []string{"evil", "line one", "line two", "here", strings.SplitN(session.UntrustedFence("x", "y"), "x", 2)[0]} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q is missing from the listing:\n%s", want, got)
		}
	}
}
