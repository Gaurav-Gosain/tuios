package app

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The package's TestMain already points every XDG base at a throwaway tree (see
// internal/testutil.RunIsolated), so the crash logs these tests write land there
// and not in the developer's state directory. Nothing here redirects again: a
// second redirect inside a single test is what trips RunIsolated's own guard.

// These tests panic on purpose, through the real barriers, and read the real
// frame that comes out of View. That is the only proof worth having here: a
// crash overlay that is asserted through a flag says nothing about whether a
// user would ever see it, because the whole failure mode it replaces was a
// program that recovered correctly and drew nothing.

// crashTestOS is a model with enough in it to compose a frame.
func crashTestOS(t testing.TB) *OS {
	t.Helper()
	return &OS{
		Settings:       config.Global,
		Windows:        []*terminal.Window{newTestWindow(t, "crash-a", 80, 24)},
		FocusedWindow:  0,
		WorkspaceFocus: map[int]int{},
		NumWorkspaces:  9,
		Width:          120,
		Height:         40,
	}
}

// TestCrashReportLeavesPrivateThingsOut is the privacy rule, asserted rather
// than described.
//
// Pane contents, the working directory, window titles, the session name and the
// environment can each carry a hostname, a token, a client's name or a path
// that says who someone works for. None of them helps place a panic in a stack
// trace, so none of them is in the report. The rule is in crashFacts' comment;
// this is what stops it from drifting.
func TestCrashReportLeavesPrivateThingsOut(t *testing.T) {
	m := crashTestOS(t)
	m.SessionName = "acme-prod-migration"
	m.Windows[0].SetTitle("ssh deploy@bastion.acme.internal")
	m.Windows[0].CustomName = "acme bastion"
	m.Windows[0].Cwd = "/home/dana/clients/acme/secrets"

	m.NoteCrash("handling an event", "boom", []byte("goroutine 1 [running]:\nmain.main()\n"))
	report := m.Crash()
	if report == nil {
		t.Fatal("no report")
	}

	// Everything the report can reach a person through: the overlay, the
	// clipboard, the issue body and the file on disk.
	surfaces := map[string]string{
		"overlay":   RenderCrashScreen(report, "", 120, 40),
		"clipboard": report.Markdown(0),
		"issue URL": report.IssueURL(),
		"log file":  readFileOrEmpty(t, report.LogPath),
	}
	leaks := []string{
		"acme-prod-migration",
		"bastion.acme.internal",
		"acme bastion",
		"/home/dana/clients",
	}
	for name, text := range surfaces {
		for _, leak := range leaks {
			if strings.Contains(text, leak) {
				t.Errorf("the %s carries %q, which is the user's and not the bug's", name, leak)
			}
		}
	}
}

func readFileOrEmpty(t *testing.T, path string) string {
	t.Helper()
	if path == "" {
		t.Fatal("the crash log was not written, so it cannot be checked")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read crash log: %v", err)
	}
	return string(b)
}

// TestIssueURLStaysShortEnoughToOpen pins the length rule.
//
// A prefilled issue is a GET, so the report travels in the query string, and
// something between the desktop's argv limit and GitHub's front end refuses a
// long one with no message a user can act on. The URL must fit whatever the
// trace is, by dropping trace lines rather than by giving up on the prefill.
func TestIssueURLStaysShortEnoughToOpen(t *testing.T) {
	var huge strings.Builder
	for i := range 4000 {
		huge.WriteString("github.com/Gaurav-Gosain/tuios/internal/app.(*OS).frame")
		huge.WriteString("\n\t/home/x/tuios/internal/app/render.go:")
		huge.WriteString(strings.Repeat("9", 4))
		huge.WriteString(" +0x1c4\n")
		_ = i
	}
	report := NewCrashReport("drawing the screen", "index out of range [7] with length 3",
		[]byte(huge.String()), baseFacts("drawing the screen"))

	// The untrimmed body is far over the limit, so an implementation that does
	// not trim cannot pass by luck.
	if untrimmed := len(report.Markdown(0)); untrimmed <= issueURLLimit {
		t.Fatalf("the fixture is too small to test the cap: %d bytes", untrimmed)
	}
	got := report.IssueURL()
	if len(got) > issueURLLimit {
		t.Fatalf("the issue URL is %d bytes, over the %d limit", len(got), issueURLLimit)
	}
	if !strings.HasPrefix(got, "https://github.com/Gaurav-Gosain/tuios/issues/new?") {
		t.Fatalf("not a new-issue address: %.80s", got)
	}
	for _, want := range []string{"title=", "body=", "labels=bug"} {
		if !strings.Contains(got, want) {
			t.Errorf("the issue URL has no %s", want)
		}
	}
	// The head of the trace is the part that names the bug, so it is the part
	// that must survive the trim.
	if !strings.Contains(got, "index+out+of+range") {
		t.Error("the issue title lost the panic value")
	}

	// A short report keeps its whole trace.
	small := NewCrashReport("handling an event", "boom",
		[]byte("goroutine 1 [running]:\nmain.main()\n"), baseFacts("handling an event"))
	if !strings.Contains(small.IssueURL(), "main.main") {
		t.Error("a short report lost its trace from the issue body")
	}
}

// TestUpdatePanicPutsTheCrashOverlayOnScreen is the Update-path proof.
//
// The panic is real and it happens inside handleMsg, on the registered input
// handler, which is the extension point every keystroke in the running program
// goes through. Nothing here asserts that a function was called: it panics,
// the barrier catches it, and then the test reads the string View produced and
// checks the words a user would see.
//
// Before this change the same panic was caught, logged and left invisible: the
// frame did not update and LogError draws nothing.
func TestUpdatePanicPutsTheCrashOverlayOnScreen(t *testing.T) {
	m := crashTestOS(t)

	SetInputHandler(func(_ tea.Msg, _ *OS) (tea.Model, tea.Cmd) {
		panic("the pane index was -1, which cannot happen")
	})
	t.Cleanup(func() { SetInputHandler(nil) })

	model, _ := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if model != tea.Model(m) {
		t.Fatal("Update did not return the model unchanged after a recovered panic")
	}
	if !m.CrashActive() {
		t.Fatal("a panic in Update did not put the crash overlay on screen")
	}

	frame := m.View().Content
	for _, want := range []string{
		"tuios hit a bug",
		"the pane index was -1",
		"Your panes and your session are still running",
		"copy report",
		"open an issue",
	} {
		if !strings.Contains(frame, want) {
			t.Fatalf("the crash frame never says %q:\n%s", want, frame)
		}
	}
}

// TestRenderPanicPutsTheCrashOverlayOnScreen is the render-path proof, and it
// is the gap this change closed.
//
// A nil entry in Windows is a real impossible state: every producer appends a
// live window, so a nil is a bug somewhere else that only shows up here, in the
// compositor, which dereferences it. Before safeComposeFrame that panic escaped
// View into bubbletea, which restores the terminal, prints a Go traceback to
// stderr and stops. Locally the user is left at a shell looking at a traceback;
// over SSH the session ends; in a browser the tab's program ends.
func TestRenderPanicPutsTheCrashOverlayOnScreen(t *testing.T) {
	m := crashTestOS(t)
	m.Windows = append(m.Windows, nil)

	frame := m.View().Content

	if !m.CrashActive() {
		t.Fatalf("a panic while drawing did not put the crash overlay on screen:\n%s", frame)
	}
	for _, want := range []string{"tuios hit a bug", "drawing the screen", "copy report"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("the crash frame never says %q:\n%s", want, frame)
		}
	}
	// The frame returned on the panicking pass, not on some later one. A crash
	// screen that only appears on the next tick is a blank screen for however
	// long the next tick takes, and on the render path there may not be one.
	if strings.TrimSpace(frame) == "" {
		t.Fatal("View returned an empty frame instead of the crash overlay")
	}
}
