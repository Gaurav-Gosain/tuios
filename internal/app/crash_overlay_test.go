package app

import (
	"fmt"
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

// TestCrashOverlaySurvivesADismissOnABrokenModel pins the dismiss contract.
//
// Dismissing a render-path crash lets View try to compose again, and the frame
// still cannot be drawn, so the overlay must come straight back rather than
// leave the user with a blank screen or take the program down.
func TestCrashOverlaySurvivesADismissOnABrokenModel(t *testing.T) {
	m := crashTestOS(t)
	m.Windows = append(m.Windows, nil)

	_ = m.View()
	if !m.CrashActive() {
		t.Fatal("no crash overlay after the first frame")
	}
	m.DismissCrash()
	if m.CrashActive() {
		t.Fatal("DismissCrash left the overlay up")
	}

	frame := m.View().Content
	if !m.CrashActive() {
		t.Fatalf("the overlay did not come back for a model that still cannot draw:\n%s", frame)
	}
	if !strings.Contains(frame, "tuios hit a bug") {
		t.Fatalf("the second frame is not the crash overlay:\n%s", frame)
	}
}

// TestCrashOverlayKeepsTheFirstReport pins which of a repeating panic's reports
// is shown.
//
// The overlay is armed by one panic and the model keeps running underneath it:
// ticks still arrive, PTY output still arrives, and any of those handlers can
// panic again for the same reason the first one did. Keeping the first report
// matters because it is the one whose facts describe the state that broke; a
// later one describes the state the overlay itself left behind.
//
// Key presses cannot reach a second panic, because the overlay consumes them.
// Everything else can, which is why this drives it through NoteCrash rather
// than through a key.
func TestCrashOverlayKeepsTheFirstReport(t *testing.T) {
	m := crashTestOS(t)

	m.NoteCrash("handling an event", "the first panic", []byte("goroutine 1:\n"))
	first := m.Crash()
	if first == nil {
		t.Fatal("the first panic did not arm the overlay")
	}

	for i := range 5 {
		m.NoteCrash("handling an event", fmt.Sprintf("a later panic %d", i), []byte("goroutine 2:\n"))
	}
	if m.Crash() != first {
		t.Fatalf("a later panic replaced the first report: now %q", m.Crash().Panic)
	}
	if !strings.Contains(m.View().Content, "the first panic") {
		t.Fatal("the frame does not show the first panic")
	}

	// Dismissing clears the way for a genuinely new one, which is what makes
	// keeping the first a de-duplication rather than a one-crash-per-session
	// limit.
	m.DismissCrash()
	m.NoteCrash("handling an event", "a new panic after the dismiss", []byte("goroutine 3:\n"))
	if got := m.Crash(); got == nil || got.Panic != "a new panic after the dismiss" {
		t.Fatalf("a panic after a dismiss did not arm a fresh overlay: %v", got)
	}
}

// TestCrashOverlayOwnsTheKeyboard checks that a key pressed while the overlay is
// up cannot reach a pane the user cannot see.
func TestCrashOverlayOwnsTheKeyboard(t *testing.T) {
	m := crashTestOS(t)

	var reached int
	SetInputHandler(func(_ tea.Msg, o *OS) (tea.Model, tea.Cmd) {
		reached++
		panic("boom")
	})
	t.Cleanup(func() { SetInputHandler(nil) })

	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if reached != 1 || !m.CrashActive() {
		t.Fatalf("setup: handler ran %d times, overlay %v", reached, m.CrashActive())
	}

	// n creates a window in window-management mode. With the overlay up it must
	// do nothing at all.
	windows := len(m.Windows)
	m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if reached != 1 {
		t.Fatal("a key reached the input handler while the crash overlay was up")
	}
	if len(m.Windows) != windows {
		t.Fatal("a key changed the model while the crash overlay was up")
	}

	// esc leaves.
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.CrashActive() {
		t.Fatal("esc did not dismiss the crash overlay")
	}
}

// TestCrashReportCarriesTheContextAReportNeeds checks every fact the overlay
// promises, on a model configured like each of the three client kinds.
func TestCrashReportCarriesTheContextAReportNeeds(t *testing.T) {
	SetBuildStamp("v9.9.9", "abc1234")
	t.Cleanup(func() { SetBuildStamp("", "") })

	cases := []struct {
		name  string
		setup func(*OS)
		want  string
	}{
		{"local", func(*OS) {}, "local"},
		{"ssh", func(m *OS) { m.IsSSHMode = true }, "ssh"},
		{"web", func(m *OS) { m.BrowserClient = true }, "web"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := crashTestOS(t)
			tc.setup(m)
			m.NoteAction("split_vertical")
			m.NoteAction("focus_next")

			facts := m.crashFacts("handling an event")
			got := map[string]string{}
			for _, f := range facts {
				got[f.Label] = f.Value
			}

			if got["Client"] != tc.want {
				t.Errorf("Client = %q, want %q", got["Client"], tc.want)
			}
			if got["tuios"] != "v9.9.9" {
				t.Errorf("tuios = %q, want the build stamp", got["tuios"])
			}
			if got["Commit"] != "abc1234" {
				t.Errorf("Commit = %q", got["Commit"])
			}
			if got["Screen"] != "120x40" {
				t.Errorf("Screen = %q", got["Screen"])
			}
			if got["Layout"] == "" {
				t.Error("no Layout fact")
			}
			if !strings.Contains(got["Panes"], "in session") {
				t.Errorf("Panes = %q", got["Panes"])
			}
			if got["Last actions"] != "split_vertical → focus_next" {
				t.Errorf("Last actions = %q", got["Last actions"])
			}
			if got["Tape"] != "not recording" {
				t.Errorf("Tape = %q", got["Tape"])
			}
			for _, need := range []string{"Caught while", "Emulator", "Go", "System", "Terminal", "Mode", "Daemon"} {
				if _, ok := got[need]; !ok {
					t.Errorf("no %q fact", need)
				}
			}
		})
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

// TestCrashOverlayDrawsOnABrokenModel is the overlay's real operating
// condition: it is asked to draw after something has already gone wrong.
//
// None of these may panic, and none may return an empty frame, because an empty
// frame is exactly the failure the overlay exists to replace.
func TestCrashOverlayDrawsOnABrokenModel(t *testing.T) {
	report := NewCrashReport("handling an event", "boom",
		[]byte("goroutine 1 [running]:\nmain.main()\n\t/x/y.go:1 +0x1\n"), baseFacts("handling an event"))

	sizes := []struct{ w, h int }{
		{120, 40}, {80, 24}, {40, 12}, {20, 6}, {8, 3}, {1, 1}, {0, 0}, {-5, -5},
	}
	for _, s := range sizes {
		frame := RenderCrashScreen(report, "", s.w, s.h)
		if frame == "" {
			t.Errorf("%dx%d drew nothing", s.w, s.h)
		}
	}

	// A report that does not exist. Not reachable through NoteCrash, but the
	// renderer is public and takes a pointer.
	if got := RenderCrashScreen(nil, "", 80, 24); !strings.Contains(got, "No report") {
		t.Errorf("a nil report did not say so:\n%s", got)
	}

	// A model that is barely a model. crashFacts reads it from inside a
	// recover, and the answer must be a report with the base facts rather than
	// a second panic.
	var zero OS
	facts := zero.crashFacts("handling an event")
	if len(facts) == 0 {
		t.Error("a zero model produced no facts at all")
	}

	// And no model.
	var nilOS *OS
	nilOS.NoteCrash("handling an event", "boom", nil)
	nilOS.DismissCrash()
	if nilOS.CrashActive() {
		t.Error("a nil model reported a crash overlay")
	}
	if got := nilOS.RecentActions(); got != nil {
		t.Errorf("a nil model returned actions: %v", got)
	}
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

// TestRecentActionsIsARingOfNames pins the ring's size and its collapsing of a
// held key, and that it holds names and nothing else.
func TestRecentActionsIsARingOfNames(t *testing.T) {
	m := &OS{}
	for _, a := range []string{"a", "a", "a", "b", "c", "d", "e", "f", "g"} {
		m.NoteAction(a)
	}
	got := strings.Join(m.RecentActions(), ",")
	if got != "c,d,e,f,g" {
		t.Fatalf("ring = %q, want the last five distinct actions", got)
	}
	m.NoteAction("")
	if len(m.RecentActions()) != maxRecentActions {
		t.Fatal("an empty action name entered the ring")
	}
}

// TestCrashOverlaySaysWhatTheKeyDid pins the overlay's own feedback line.
//
// This is not a nicety. A crash caught while drawing the screen is the
// compositor failing, so the dock is not drawn and ShowNotification has nowhere
// to appear: without a line on the overlay itself, pressing c copies the report
// and changes nothing on screen, which reads as a key that does not work.
func TestCrashOverlaySaysWhatTheKeyDid(t *testing.T) {
	m := crashTestOS(t)
	m.Windows = append(m.Windows, nil)
	_ = m.View() // panics in the compositor, arms the overlay

	if got := m.CrashNotice(); got != "" {
		t.Fatalf("a fresh overlay already had a notice: %q", got)
	}

	if cmd := m.CopyCrashReport(); cmd == nil {
		t.Fatal("c produced no clipboard command")
	}
	if !strings.Contains(m.CrashNotice(), "Copied the report") {
		t.Fatalf("c left no notice: %q", m.CrashNotice())
	}
	frame := m.View().Content
	if !strings.Contains(frame, "Copied the report") {
		t.Fatalf("the frame does not say the report was copied:\n%s", frame)
	}

	m.DismissCrash()
	if m.CrashNotice() != "" {
		t.Fatalf("the notice outlived the overlay: %q", m.CrashNotice())
	}
}

// TestCrashIssueBehavesPerClientKind pins the one thing that differs between a
// local, an SSH and a web client.
//
// A URL has to be opened by a browser on the machine the person is sitting at,
// and there is no escape sequence for "open this". So a local client hands it
// to the desktop and a remote one puts it on the clipboard and says why. That
// split is OpenLink's and is not reimplemented here; this checks the crash
// overlay actually goes through it and that a remote user is told what happened.
func TestCrashIssueBehavesPerClientKind(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setup      func(*OS)
		wantRemote bool
	}{
		{"ssh", func(m *OS) { m.IsSSHMode = true; m.RemoteClient = true }, true},
		{"web", func(m *OS) { m.BrowserClient = true; m.RemoteClient = true }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := crashTestOS(t)
			tc.setup(m)
			m.NoteCrash("handling an event", "boom", []byte("goroutine 1 [running]:\n"))

			cmd := m.OpenCrashIssue()
			if cmd == nil {
				t.Fatal("g produced no command; a remote client must still copy the address")
			}
			notice := m.CrashNotice()
			if !strings.Contains(notice, "Copied") {
				t.Fatalf("a remote client was not told the address was copied: %q", notice)
			}
			if !strings.Contains(notice, "remote client can not open it") {
				t.Fatalf("a remote client was not told why: %q", notice)
			}
		})
	}
}

// TestCrashOverlaySaysWhenItLeftDetailsOut pins the cut being visible.
//
// A block that silently ends at a different fact on every terminal size reads
// as a report whose contents depend on the screen, and the one thing a user has
// to be able to trust here is that c sends the whole of it.
func TestCrashOverlaySaysWhenItLeftDetailsOut(t *testing.T) {
	m := crashTestOS(t)
	m.NoteCrash("handling an event", "boom", []byte("goroutine 1 [running]:\n"))
	report := m.Crash()

	tall := RenderCrashScreen(report, "", 120, 44)
	if strings.Contains(tall, "more details") {
		t.Errorf("a tall screen claimed it left details out:\n%s", tall)
	}
	for _, f := range report.Facts {
		if !strings.Contains(tall, f.Label) {
			t.Errorf("a tall screen dropped the %q row", f.Label)
		}
	}

	short := RenderCrashScreen(report, "", 80, 20)
	if !strings.Contains(short, "more details") {
		t.Errorf("a short screen cut the details and did not say so:\n%s", short)
	}
	// And it still offers the way out, which is the point of cutting the facts
	// rather than the footer.
	for _, want := range []string{"copy report", "open an issue"} {
		if !strings.Contains(short, want) {
			t.Errorf("a short screen lost %q from the footer:\n%s", want, short)
		}
	}
}

// TestCrashOverlayNamesTheTapeWhenOneIsRunning pins the one honest answer tuios
// has to "how do I reproduce this".
//
// A tape is a replayable script and it exists only when the user chose to
// record. When one is running the report says so, because that recording is
// worth more than the rest of the report together. When one is not, it says
// "not recording" rather than implying tuios could have replayed this.
func TestCrashOverlayNamesTheTapeWhenOneIsRunning(t *testing.T) {
	m := crashTestOS(t)
	if got := m.crashTapeState(); got != "not recording" {
		t.Fatalf("with no recorder the tape fact is %q", got)
	}

	m.InitTapeManager()
	m.TapeManagerStartRecording()
	m.TapeManagerConfirmRecording()
	if m.TapeRecorder == nil || !m.TapeRecorder.IsRecording() {
		t.Fatal("the recorder did not start, so the fact cannot be checked")
	}
	if got := m.crashTapeState(); !strings.Contains(got, "recording") {
		t.Fatalf("with a recorder running the tape fact is %q", got)
	}
}
