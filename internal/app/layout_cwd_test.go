package app

import (
	"os"
	"strings"
	"testing"

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

// Loading a layout into an existing pane at its prompt types a cd. The path
// must be quoted for a POSIX shell, so nothing in it runs.
func TestLayoutLoadQuotesTheCdForTheShell(t *testing.T) {
	w, typed := layoutWindow(t, "load-win")
	w.Workspace = 1
	m := layoutOS(w)

	ApplyLayoutTemplate(LayoutTemplate{Windows: []LayoutWindow{{Width: 40, Height: 10, WorkingDir: spoofedCwd}}}, m)

	want := "cd " + shellQuote(spoofedCwd) + " && clear\n"
	if got := typed.String(); got != want {
		t.Fatalf("typed %q, want %q", got, want)
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
