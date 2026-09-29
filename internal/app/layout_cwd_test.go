package app

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// spoofedCwd is a directory a pane can announce over OSC 7. Inside shell
// double quotes, which Go's %q produces, the $(...) runs.
const spoofedCwd = `/tmp/x"$(touch /tmp/tuios-layout-pwned)"`

// layoutWindow is an existing pane whose typed input is recorded.
func layoutWindow(t *testing.T, id string) (*terminal.Window, *strings.Builder) {
	t.Helper()
	w := terminal.NewDaemonWindow(id, "t", 0, 0, 40, 10, 0, "pty-"+id, make(chan struct{}, 1), 100)
	t.Cleanup(w.Close)
	var typed strings.Builder
	w.DaemonWriteFunc = func(b []byte) error { typed.Write(b); return nil }
	return w, &typed
}

func layoutOS(wins ...*terminal.Window) *OS {
	return &OS{
		Settings:         config.DefaultSettings(),
		Windows:          wins,
		CurrentWorkspace: 1,
		Width:            120,
		Height:           40,
	}
}

// Saving a layout records a directory for each pane. The pane's OSC 7 claim is
// kept only when the kernel agrees with it. When it does not, the kernel's
// directory is recorded instead.
func TestLayoutSaveRecordsTheKernelCwdOverASpoofedOne(t *testing.T) {
	useTempConfig(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	w, _ := layoutWindow(t, "save-win")
	w.Workspace = 1
	w.Cwd = spoofedCwd
	// This test process stands in for the pane's shell: its cwd is what the
	// kernel reports for the pane.
	w.ShellPgid = os.Getpid()
	m := layoutOS(w)

	if err := SaveLayoutTemplate("spoof", m); err != nil {
		t.Fatal(err)
	}
	tmpls, err := LoadLayoutTemplates()
	if err != nil || len(tmpls) != 1 || len(tmpls[0].Windows) != 1 {
		t.Fatalf("templates = %+v, err %v", tmpls, err)
	}
	if got := tmpls[0].Windows[0].WorkingDir; got != wd {
		t.Fatalf("saved working_dir = %q, want the kernel's %q", got, wd)
	}
}

// With no shell pid there is no kernel to check the OSC 7 claim against, and
// the claim is not recorded.
func TestLayoutSaveDropsACwdItCannotCheck(t *testing.T) {
	useTempConfig(t)
	w, _ := layoutWindow(t, "save-unchecked")
	w.Workspace = 1
	w.Cwd = spoofedCwd
	m := layoutOS(w)

	if err := SaveLayoutTemplate("unchecked", m); err != nil {
		t.Fatal(err)
	}
	tmpls, err := LoadLayoutTemplates()
	if err != nil || len(tmpls) != 1 || len(tmpls[0].Windows) != 1 {
		t.Fatalf("templates = %+v, err %v", tmpls, err)
	}
	if got := tmpls[0].Windows[0].WorkingDir; got != "" {
		t.Fatalf("saved working_dir = %q, want nothing for an unchecked claim", got)
	}
}

// A pane tuios cannot see into (a daemon pane has no local PTY) may be running
// anything, so loading a layout types nothing into it.
func TestLayoutLoadTypesNothingIntoAPaneItCannotSee(t *testing.T) {
	w, typed := layoutWindow(t, "load-win")
	w.Workspace = 1
	m := layoutOS(w)

	ApplyLayoutTemplate(LayoutTemplate{Windows: []LayoutWindow{{Width: 40, Height: 10, WorkingDir: "/tmp"}}}, m)

	if got := typed.String(); got != "" {
		t.Fatalf("typed %q into a pane whose shell tuios cannot see", got)
	}
}

// A shell tuios spawned itself, at its prompt, is moved with a typed cd.
func TestLayoutLoadMovesALocalShellAtItsPrompt(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	w, err := terminal.NewWindow("local-win", "t", 0, 0, 40, 10, 0, make(chan string, 4), make(chan struct{}, 1), 100)
	if err != nil {
		t.Skipf("cannot spawn a shell: %v", err)
	}
	t.Cleanup(w.Close)
	w.Workspace = 1
	m := layoutOS(w)
	dir := t.TempDir()

	deadline := time.Now().Add(5 * time.Second)
	for !w.ShellAtPrompt() {
		if time.Now().After(deadline) {
			t.Skip("the shell never took the terminal")
		}
		time.Sleep(20 * time.Millisecond)
	}
	ApplyLayoutTemplate(LayoutTemplate{Windows: []LayoutWindow{{Width: 40, Height: 10, WorkingDir: dir}}}, m)

	for time.Now().Before(deadline) {
		if got, ok := terminal.ShellCWD(w.ShellPgid); ok && sameDir(got, dir) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := terminal.ShellCWD(w.ShellPgid)
	t.Fatalf("the shell is in %q, want %q", got, dir)
}

// No quoting of a quote or a backslash is right for every shell (fish reads
// \' inside single quotes as an escaped quote), so such a folder gets no cd,
// and the dock says so.
func TestLayoutLoadRefusesAQuoteInTheFolder(t *testing.T) {
	for _, dir := range []string{`/tmp/a\'';echo INJECTED;#`, `/tmp/back\slash`} {
		w, typed := layoutWindow(t, "quote-win")
		w.Workspace = 1
		m := layoutOS(w)

		ApplyLayoutTemplate(LayoutTemplate{Windows: []LayoutWindow{{Width: 40, Height: 10, WorkingDir: dir}}}, m)

		if got := typed.String(); got != "" {
			t.Fatalf("typed %q for folder %q", got, dir)
		}
		if lastMessage(m) != cdRefusedMessage {
			t.Fatalf("dock = %q, want the refusal for folder %q", lastMessage(m), dir)
		}
	}
}

// The sidebar's "cd here" refuses the same folders.
func TestSidebarCdRefusesAQuoteInTheFolder(t *testing.T) {
	w, typed := layoutWindow(t, "sidebar-win")
	m := layoutOS(w)
	m.filesView.Origin = w.ID

	m.sendCdToOrigin(`/tmp/a\'';echo INJECTED;#`)

	if got := typed.String(); got != "" {
		t.Fatalf("typed %q", got)
	}
	if lastMessage(m) != cdRefusedMessage {
		t.Fatalf("dock = %q, want the refusal", lastMessage(m))
	}
	// A plain folder is not typed either: a daemon pane has no local PTY, so
	// tuios cannot see its shell at a prompt, whatever it reports.
	m.sendCdToOrigin("/tmp/plain dir")
	if got := typed.String(); got != "" {
		t.Fatalf("typed %q into a pane tuios cannot see", got)
	}
	if lastMessage(m) != cdUnseenMessage {
		t.Fatalf("dock = %q, want the unseen-pane message", lastMessage(m))
	}
}

// A shell tuios spawned, at its prompt, gets the sidebar's cd.
func TestSidebarCdMovesALocalShellAtItsPrompt(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	w, err := terminal.NewWindow("local-cd", "t", 0, 0, 40, 10, 0, make(chan string, 4), make(chan struct{}, 1), 100)
	if err != nil {
		t.Skipf("cannot spawn a shell: %v", err)
	}
	t.Cleanup(w.Close)
	m := layoutOS(w)
	m.filesView.Origin = w.ID
	dir := t.TempDir()

	deadline := time.Now().Add(5 * time.Second)
	for !w.ShellAtPrompt() {
		if time.Now().After(deadline) {
			t.Skip("the shell never took the terminal")
		}
		time.Sleep(20 * time.Millisecond)
	}
	m.sendCdToOrigin(dir)
	for time.Now().Before(deadline) {
		if got, ok := terminal.ShellCWD(w.ShellPgid); ok && sameDir(got, dir) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := terminal.ShellCWD(w.ShellPgid)
	t.Fatalf("the shell is in %q, want %q", got, dir)
}

// The tape export writes no cd for such a folder either.
func TestLayoutTapeExportRefusesAQuoteInTheFolder(t *testing.T) {
	script := GenerateTapeScript(LayoutTemplate{Windows: []LayoutWindow{{WorkingDir: `/tmp/a\'';echo INJECTED;#`}}})
	cmds, _ := tape.ParseFile(script)
	for _, c := range cmds {
		if c.Type == tape.CommandTypeType {
			t.Fatalf("tape types %q for a folder holding a quote:\n%s", c.Args, script)
		}
	}
}

// A pane running something other than its shell gets nothing typed into it:
// an agent would read the cd as a prompt.
func TestLayoutLoadDoesNotTypeIntoAProgram(t *testing.T) {
	w, typed := layoutWindow(t, "busy-win")
	w.Workspace = 1
	w.ForegroundCmd = "claude"
	m := layoutOS(w)

	ApplyLayoutTemplate(LayoutTemplate{Windows: []LayoutWindow{{Width: 40, Height: 10, WorkingDir: "/tmp"}}}, m)

	if got := typed.String(); got != "" {
		t.Fatalf("typed %q into a pane running a program", got)
	}
}

// The tape export types the cd too, and must quote it the same way. The Type
// line is read back through the tape parser, so this checks what a replay
// would type.
func TestLayoutTapeExportQuotesTheCd(t *testing.T) {
	script := GenerateTapeScript(LayoutTemplate{Windows: []LayoutWindow{{WorkingDir: spoofedCwd}}})
	cmds, errs := tape.ParseFile(script)
	if len(errs) > 0 {
		t.Fatalf("tape does not parse: %v\n%s", errs, script)
	}
	want := "cd " + shellQuote(spoofedCwd)
	for _, c := range cmds {
		if c.Type == tape.CommandTypeType {
			if got := strings.Join(c.Args, " "); got != want {
				t.Fatalf("tape types %q, want %q", got, want)
			}
			return
		}
	}
	t.Fatalf("tape types no cd:\n%s", script)
}
