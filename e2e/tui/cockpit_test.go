package tuie2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// The cockpit: ctrl+b i opens one full-screen surface with every agent,
// grouped by what each needs from the person. These tests build a fleet of
// twelve fake agents in four sessions with the real CLI and the real hook,
// open the cockpit, and read it the way a person does.

// cockpitAgent is one fake agent of the fleet.
type cockpitAgent struct {
	session, name, harness, state, message string
	meta                                   []string
}

// cockpitFleet is the fleet of DESIGN.md frame (a): one approval waits, three
// turns are ready for review, six agents work, two rest.
var cockpitFleet = []cockpitAgent{
	{"docs", "docs-cli", "codex", "done", "", []string{"model=gpt-5", "diff=+88/-12"}},
	{"docs", "vt-sixel", "claude-code", "done", "", []string{"model=opus-4.5", "diff=+412/-160"}},
	{"docs", "rail-tokens", "opencode", "done", "", []string{"diff=+36/-4"}},
	{"cache", "cache-1", "gemini", "working", "Bash go test ./internal/cache/...", []string{"diff=+142/-20"}},
	{"cache", "cache-2", "claude-code", "working", "Edit internal/cache/lru.go", []string{"diff=+61/-9"}},
	{"cache", "cache-3", "codex", "working", "Read internal/cache/evict.go", []string{"diff=+97/-31"}},
	{"cache", "bench-idle", "claude-code", "working", "Bash go test -bench Idle -count 5", []string{"context=31%", "diff=+18/-2"}},
	{"api", "ws-reconnect", "codex", "working", "Edit internal/federation/link.go", []string{"diff=+54/-11"}},
	{"api", "migrate-cfg", "claude-code", "working", "Task 2 subagents", []string{"diff=+230/-198"}},
	{"misc", "scratch", "claude-code", "idle", "", nil},
	{"misc", "release-notes", "goose", "idle", "", nil},
}

// cockpitApproval is the hook payload of the one held approval.
const cockpitApproval = `{"hook_event_name":"PermissionRequest","session_id":"e2e-cockpit","tool_name":"Bash","tool_input":{"command":"go test ./internal/auth/... -run Token -count=1"}}`

// startCockpitFleet starts a daemon with the fleet, a client attached to an
// empty session at cols x rows, and the hook holding api-auth's approval. It
// returns the client and the hook's output, which holds the answer once one
// is given.
func startCockpitFleet(t *testing.T, cols, rows int, env []string) (*tuitest.Terminal, string, *bytes.Buffer, chan error) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 300\n")
	for _, s := range []string{"home", "api", "docs", "cache", "misc"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	if out, err := tuiosCLI(t, base, "new-window", "api-auth", "-s", "api", "--", "/bin/sh", "-c", "sleep 900"); err != nil {
		t.Fatalf("open api-auth: %v\n%s", err, out)
	}
	for _, a := range cockpitFleet {
		if out, err := tuiosCLI(t, base, "new-window", a.name, "-s", a.session, "--", "/bin/sh", "-c", "sleep 900"); err != nil {
			t.Fatalf("open %s: %v\n%s", a.name, err, out)
		}
		args := []string{"set-agent-state", a.state, "-s", a.session, "-w", a.name, "--harness", a.harness}
		if a.message != "" {
			args = append(args, "-m", a.message)
		}
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("report %s: %v\n%s", a.name, err, out)
		}
		if len(a.meta) > 0 {
			if out, err := tuiosCLI(t, base, append([]string{"set-agent-meta", "-s", a.session, "-w", a.name}, a.meta...)...); err != nil {
				t.Fatalf("meta of %s: %v\n%s", a.name, err, out)
			}
		}
	}
	if out, err := tuiosCLI(t, base, "set-agent-meta", "-s", "api", "-w", "api-auth", "model=opus-4.5", "context=62%", "diff=+214/-37"); err != nil {
		t.Fatalf("meta of api-auth: %v\n%s", err, out)
	}

	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", "api-auth")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = strings.NewReader(cockpitApproval)
	var stdout bytes.Buffer
	hook.Stdout = &stdout
	if err := hook.Start(); err != nil {
		t.Fatalf("start the hook: %v", err)
	}
	exited := make(chan error, 1)
	t.Cleanup(func() { _ = hook.Process.Kill() })
	go func() { exited <- hook.Wait() }()
	waitForAttention(t, base, nil, "go test ./internal/auth/...")

	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: cols, rows: rows, shippedLooks: true, env: env})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the cockpit: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Needs you", "Ready for review", "Working", "Idle", "release-not")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the cockpit never showed the four groups: %v\n%s", err, term.Snapshot())
	}
	return term, base, &stdout, exited
}

// TestCockpitGroupsTheFleet opens the cockpit on twelve agents. The groups
// come in the order of what each needs from the person, the approval's row
// says the verb and the command, a click selects a row and leaves the cockpit
// open, a answers the approval from the card, and a double-click goes to the
// pane.
//
// Negative controls, each recorded in NEGATIVE_CONTROLS.md: with the groups
// drawn in the rail's state order the order check fails; with a click going
// to the pane (the agents view's old rule) the click check fails.
func TestCockpitGroupsTheFleet(t *testing.T) {
	term, _, hookOut, exited := startCockpitFleet(t, 200, 55, nil)
	s := term.Screen()
	order := []string{"Needs you", "Ready for review", "Working", "Idle"}
	last := -1
	for _, g := range order {
		row := rowWith(s, g)
		if row <= last {
			t.Fatalf("ASSERTION: %q is on row %d, not below the group before it (row %d)\n%s", g, row, last, term.Snapshot())
		}
		last = row
	}
	// The approval is the Needs you group's one row: its verb and command.
	need := fleetRowWith(s, "api-auth")
	if need < 0 || need <= rowWith(s, "Needs you") || need >= rowWith(s, "Ready for review") {
		t.Fatalf("ASSERTION: api-auth is not in the Needs you group\n%s", term.Snapshot())
	}
	if !strings.Contains(s.Line(need+1), "Allow Bash") || !strings.Contains(s.Line(need+1), "go test ./internal/auth/...") {
		t.Fatalf("ASSERTION: the approval's second line does not say what it asks: %q\n%s", s.Line(need+1), term.Snapshot())
	}
	// The card answers it in place.
	if !containsAll(s.Text(), "Allow once", "Reject") {
		t.Fatalf("ASSERTION: the focus has no approval card\n%s", term.Snapshot())
	}
	_ = term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Reading...") }, uiTimeout)
	saveCockpitFrames(t, term, "cockpit-200x55")

	// A click on a working agent selects it: the focus shows it, and the
	// cockpit stays open.
	row, col := findText(t, term, "bench-idle")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		head := []rune(s.Line(rowWith(s, "Needs you")))
		return len(head) > 60 && strings.Contains(string(head[56:]), "bench-idle")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a click did not put bench-idle in the focus: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "Ready for review") {
		t.Fatalf("ASSERTION: a click left the cockpit\n%s", term.Snapshot())
	}
	saveCockpitFrames(t, term, "cockpit-200x55-working")

	// Back on the approval, a allows it once.
	row, col = findText(t, term, "api-auth")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	time.Sleep(1500 * time.Millisecond)
	if err := term.SendKeys("a"); err != nil {
		t.Fatalf("press a: %v", err)
	}
	select {
	case <-exited:
	case <-time.After(uiTimeout):
		t.Fatalf("ASSERTION: a never answered the held approval\n%s", term.Snapshot())
	}
	if !strings.Contains(hookOut.String(), `"behavior":"allow"`) {
		t.Fatalf("ASSERTION: the hook did not get an allow: %q", hookOut.String())
	}

	// A double-click goes to the pane and closes the cockpit.
	row, col = findText(t, term, "docs-cli")
	clickAt(t, term, col, row, 2)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Ready for review")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a double-click did not go to the pane: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after the cockpit round trip")
}

// TestCockpitNarrowFoldsRows opens the cockpit at 100x30: the fleet's rows are
// one line and the focus is beside them. At 16 colours the cursor row is
// reverse video.
func TestCockpitNarrowFoldsRows(t *testing.T) {
	for _, d := range []chromeDepth{{name: "truecolor", env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}}, chromeDepths[0]} {
		t.Run(d.name, func(t *testing.T) {
			term, _, _, _ := startCockpitFleet(t, 100, 30, d.env)
			s := term.Screen()
			need := fleetRowWith(s, "api-auth")
			if need < 0 || strings.Contains(string([]rune(s.Line(need + 1))[:38]), "Allow Bash") {
				t.Fatalf("ASSERTION: the narrow fleet drew a two-line row\n%s", term.Snapshot())
			}
			saveCockpitFrames(t, term, "cockpit-100x30-"+d.name)
		})
	}
}

// fleetRowWith is the first row whose fleet column, the first 56 cells,
// holds want.
func fleetRowWith(s tuitest.Screen, want string) int {
	_, rows := s.Size()
	for r := range rows {
		line := []rune(s.Line(r))
		if strings.Contains(string(line[:min(len(line), 56)]), want) {
			return r
		}
	}
	return -1
}

// saveCockpitFrames saves the screen as text and as a PNG drawn by
// tuios.dev/shot, in the font named by TUIOS_E2E_FONT_DIR when it is set.
func saveCockpitFrames(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	dir := artifactDir(t)
	saveArtifact(t, term, dir, name)
	s := term.Screen()
	p := shot.XTermPalette()
	cols, rows := s.Size()
	g := shot.NewGrid(cols, rows, p.FG, p.BG)
	for y := range rows {
		for x := range cols {
			c := s.Cell(x, y)
			fg, bg := resolveCell(c.Fg, p, p.FG), resolveCell(c.Bg, p, p.BG)
			bgDefault := c.Bg.Kind == tuitest.ColorDefault
			if c.Reverse {
				fg, bg, bgDefault = bg, fg, false
			}
			cell := shot.Cell{Cluster: c.Content, Width: uint8(max(min(c.Width, 2), 0)), FG: fg, BG: bg,
				BGDefault: bgDefault, Bold: c.Bold, Faint: c.Faint, Italic: c.Italic, Strike: c.Strikethrough}
			if cell.Cluster == " " {
				cell.Cluster = ""
			}
			if c.Underline {
				cell.Underline = shot.UnderlineSingle
			}
			g.Cells[y][x] = cell
		}
	}
	var f *shot.Frame
	if fonts := os.Getenv("TUIOS_E2E_FONT_DIR"); fonts != "" {
		regular, err1 := os.ReadFile(filepath.Join(fonts, "GeistMonoNerdFontMono-Regular.otf"))
		bold, err2 := os.ReadFile(filepath.Join(fonts, "GeistMonoNerdFontMono-Bold.otf"))
		if err1 == nil && err2 == nil {
			f = shot.BuildFrame(shot.FrameSpec{Frame: "window", Title: "tuios", Padding: 28, Radius: 10, Shadow: true, Controls: "macos",
				Font: shot.Font{Data: regular}, BoldFont: shot.Font{Data: bold}}, shot.FrameInputs{Palette: p})
			if missing, err := shot.MissingGlyphs(g, f); err == nil && len(missing) > 0 {
				t.Errorf("ASSERTION: the font has no glyph for %q", string(missing))
			}
		}
	}
	data, err := shot.Render(shot.FormatPNG, g, f)
	if err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".png"), data, 0o644); err != nil {
		t.Fatalf("save %s: %v", name, err)
	}
}
