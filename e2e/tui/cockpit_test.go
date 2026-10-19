package tuie2e

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
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
	regularPath, boldPath := os.Getenv("TUIOS_E2E_FONT_REGULAR"), os.Getenv("TUIOS_E2E_FONT_BOLD")
	if fonts := os.Getenv("TUIOS_E2E_FONT_DIR"); fonts != "" && regularPath == "" {
		regularPath = filepath.Join(fonts, "GeistMonoNerdFontMono-Regular.otf")
		boldPath = filepath.Join(fonts, "GeistMonoNerdFontMono-Bold.otf")
	}
	if regularPath != "" {
		regular, err1 := os.ReadFile(regularPath)
		bold, err2 := os.ReadFile(boldPath)
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

// TestCockpitThreadCards reads a Claude Code transcript as cards: a run of
// reads folds to one line, the Edit opens on its diff with the changed words
// lit, the todo list is a plan with its count, and the failed command shows
// its output. A click on the diff's header folds it, and ] shows the last
// turn's changes alone.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: with the changed
// range left out of the diff lines the word check fails; with the todos
// drawn as one line the plan check fails.
func TestCockpitThreadCards(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	for _, s := range []string{"home200", "home100", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "transcript", "cockpit-auth.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(atProjects(base), "src-api", "9a1f0c2e.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "api", "-w", "0", "--name", "api-auth"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", "api-auth")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(hookPayload(path, "9a1f0c2e"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	for _, args := range [][]string{
		{"set-agent-state", "working", "-s", "api", "-w", "api-auth", "--harness", "claude-code"},
		{"set-agent-meta", "-s", "api", "-w", "api-auth", "model=opus-4.5", "context=62%", "diff=+214/-37"},
	} {
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	for _, sz := range [][2]int{{200, 55}, {100, 30}} {
		// Each size attaches a session of its own, so the two clients do
		// not share a size.
		term := startIn(t, base, startOpts{args: []string{"attach", "home" + strconv.Itoa(sz[0])}, cols: sz[0], rows: sz[1], shippedLooks: true, env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
		if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
			t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
		}
		windowManagementMode(t, term)
		if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
			t.Fatalf("open the cockpit: %v", err)
		}
		// The newest card is at the bottom; the plan and the edit above it.
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return containsAll(s.Text(), "Running the auth tests.", "go vet ./internal/auth/...", "refreshSkew declared and not used")
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the thread never showed the failed command's output: %v\n%s", err, term.Snapshot())
		}
		// scrollTo presses key, three lines a press, until want shows.
		scrollTo := func(key, want string) {
			t.Helper()
			for range 12 {
				if strings.Contains(term.Screen().Text(), want) {
					return
				}
				if err := term.SendKeys(key); err != nil {
					t.Fatal(err)
				}
				_ = term.WaitStable(uiTimeout)
			}
		}
		scrollTo("K", "2 of 5 done")
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return containsAll(s.Text(), "2 of 5 done", "Test the boundary at 30 s")
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the thread has no plan card: %v\n%s", err, term.Snapshot())
		}
		scrollTo("J", "Add(refreshSkew).Before")
		if err := term.WaitForText("Add(refreshSkew).Before", uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the thread has no diff card: %v\n%s", err, term.Snapshot())
		}
		// The changed words are lit: their cells sit on another ground than
		// the rest of the added line.
		s := term.Screen()
		row := rowWith(s, "Add(refreshSkew).Before")
		line := []rune(s.Line(row))
		word := strings.Index(string(line), "Add(refreshSkew)")
		word = len([]rune(string(line)[:word]))
		ret := len([]rune(string(line)[:strings.Index(string(line), "return")]))
		if s.Cell(word, row).Bg == s.Cell(ret, row).Bg {
			t.Fatalf("ASSERTION: the changed words of the diff are not lit\n%s", term.Snapshot())
		}
		// The light starts at the word, not before it: Go's diff lines start
		// with a tab, and an offset taken after the tab became four spaces
		// lit three cells early.
		if s.Cell(word-1, row).Bg != s.Cell(ret, row).Bg {
			t.Fatalf("ASSERTION: the cell before the changed words is lit too\n%s", term.Snapshot())
		}
		saveCockpitFrames(t, term, "thread-"+strconv.Itoa(sz[0])+"x"+strconv.Itoa(sz[1]))
		// Further up, the three reads and the search are one line.
		scrollTo("K", "Read 3 · Search 1")
		if err := term.WaitForText("Read 3 · Search 1", uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the reads did not fold to one line: %v\n%s", err, term.Snapshot())
		}
		scrollTo("J", "Add(refreshSkew).Before")
		if err := term.WaitStable(uiTimeout); err != nil {
			t.Fatal(err)
		}

		// A click on the edit's header folds its diff.
		hr, hc := findText(t, term, "Edit   internal/auth/token.go")
		mouseClick(t, term, hc, hr, tuitest.MouseLeft, 0)
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return !strings.Contains(s.Text(), "Add(refreshSkew).Before")
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: a click on the header did not fold the diff: %v\n%s", err, term.Snapshot())
		}
		// ] shows the last turn's changes, each whole.
		if err := term.SendKeys("]"); err != nil {
			t.Fatal(err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			text := s.Text()
			return strings.Contains(text, "const refreshSkew = 30 * time.Second") && !strings.Contains(text, "2 of 5 done")
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: ] did not show the changes alone: %v\n%s", err, term.Snapshot())
		}
		saveCockpitFrames(t, term, "changes-"+strconv.Itoa(sz[0])+"x"+strconv.Itoa(sz[1]))
		if err := term.SendKeys("["); err != nil {
			t.Fatal(err)
		}
		alive(t, term, "after the thread cards")
	}
}

// TestCockpitReplyAreaAndImageView writes a reply in the agents view with an
// image dropped in, opens the image from its chip, and closes the view by
// esc and by a click outside the picture. The Send button queues the reply.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: with the chip given no
// click target the view never opens; with a click on the picture's backdrop
// read as inside, the click outside does not close it.
func TestCockpitReplyAreaAndImageView(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	for _, s := range []string{"home", "docs"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	for _, args := range [][]string{
		{"set-window", "-s", "docs", "-w", "0", "--name", "docs-cli"},
		{"set-agent-state", "idle", "-s", "docs", "-w", "docs-cli", "--harness", "claude-code"},
		{"set-agent-meta", "-s", "docs", "-w", "docs-cli", "model=gpt-5", "diff=+88/-12"},
	} {
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	shot := filepath.Join(t.TempDir(), "login-error.png")
	if err := os.WriteFile(shot, gradientPNG(t, 640, 360), 0o644); err != nil {
		t.Fatal(err)
	}
	// Without kitty graphics the picture is cells, which the screen holds
	// and the frames show.
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 200, rows: 55, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the closed reply area", "Reply to docs-cli", "Write a reply.", "Send")
	saveCockpitFrames(t, term, "reply-closed-200x55")

	// A click on the area opens the editor.
	row, col := findText(t, term, "Write a reply.")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	waitText(t, term, "the open reply editor", "Type your reply.")
	if err := term.SendKeys("Before you merge, look at the login error. "); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the typed words", "look at the login error.")
	if err := term.Paste(shot); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the image chip", "login-error.png  640×360", "click to view")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "reply-open-200x55")

	// The chip opens the image view.
	openView := func() {
		t.Helper()
		row, col := findText(t, term, "login-error.png  640×360")
		mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return containsAll(s.Text(), "login-error.png", "640×360", "pasted by you for docs-cli", "1 of 1", "Click outside the picture to close it.")
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: a click on the chip did not open the image view: %v\n%s", err, term.Snapshot())
		}
	}
	openView()
	// The picture is drawn: the middle row of the screen holds its glyphs.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, rows := s.Size()
		return strings.Trim(s.Line(rows/2), " ") != ""
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the image view drew no picture: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "image-200x55")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Click outside the picture") && strings.Contains(s.Text(), "click to view")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc did not close the image view back to the draft: %v\n%s", err, term.Snapshot())
	}

	// A click outside the picture closes it too.
	openView()
	row, col = findText(t, term, "Click outside the picture")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Click outside the picture") && strings.Contains(s.Text(), "click to view")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a click outside the picture did not close the view: %v\n%s", err, term.Snapshot())
	}

	// Send queues the reply and closes the editor.
	row, col = findText(t, term, "Send")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	waitText(t, term, "the closed area after Send", "Write a reply.")
	alive(t, term, "after the reply area and the image view")
}

// gradientPNG is a w by h PNG of soft colour bands, a picture that reads as
// one at a glance.
func gradientPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			i := img.PixOffset(x, y)
			img.Pix[i] = uint8(40 + 140*x/w)
			img.Pix[i+1] = uint8(30 + 60*y/h)
			img.Pix[i+2] = uint8(120 + 120*(w-x)/w)
			img.Pix[i+3] = 255
			if (x-w/2)*(x-w/2)+(y-h/2)*(y-h/2) < (h/4)*(h/4) {
				img.Pix[i], img.Pix[i+1], img.Pix[i+2] = 250, 190, 90
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestCockpitSideColumn shows the side column: the live copy of the agent's
// pane, the files its last turn changed, and its checkpoints. The pane prints
// a marker before the agents view opens and another while it is open, and
// both reach the live box. At 100x30 the same pieces are the Checkpoints and
// Pane tabs.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: with the live pane not
// marked on output the second marker never reaches the box; with the
// checkpoints read from nowhere the count check fails.
func TestCockpitSideColumn(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	repo := testutil.GitRepo(t)
	if out, err := tuiosCLIIn(t, base, repo, "new", "agent", "--detach"); err != nil {
		t.Fatalf("new session in the repository: %v: %s", err, out)
	}
	gate := filepath.Join(t.TempDir(), "gate")
	script := `printf 'LIVE-%s\n' first-7; while [ ! -e ` + gate + ` ]; do sleep 0.1; done; printf 'LIVE-%s\n' second-9; sleep 900`
	if out, err := tuiosCLIIn(t, base, repo, "new-window", "api-auth", "-s", "agent", "--", "/bin/sh", "-c", script); err != nil {
		t.Fatalf("open the agent's pane: %v\n%s", err, out)
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "transcript", "cockpit-auth.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(atProjects(base), "repo", "9a1f0c2e.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "agent", "--window", "api-auth")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(hookPayload(path, "9a1f0c2e"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	// Two turns, each changing a file, so two checkpoints.
	notes := filepath.Join(repo, "notes.txt")
	for n, label := range []string{"Read the auth package", "Expiry check with skew"} {
		if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "agent", "-w", "api-auth", "--harness", "claude-code"); err != nil {
			t.Fatalf("working: %v\n%s", err, out)
		}
		if err := os.WriteFile(notes, []byte(strings.Repeat("turn\n", n+1)), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := tuiosCLI(t, base, "set-agent-state", "done", "-s", "agent", "-w", "api-auth", "--harness", "claude-code", "-m", label); err != nil {
			t.Fatalf("done: %v\n%s", err, out)
		}
		deadline := time.Now().Add(20 * time.Second)
		for {
			out, _ := tuiosCLIIn(t, base, repo, "checkpoint", "list", "-s", "agent", "-w", "api-auth", "--json")
			if strings.Count(out, `"kind": "turn"`)+strings.Count(out, `"kind":"turn"`) >= n+1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("turn %d left no checkpoint:\n%s", n+1, out)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if out, err := tuiosCLI(t, base, "set-agent-meta", "-s", "agent", "-w", "api-auth", "model=opus-4.5", "context=62%", "diff=+214/-37"); err != nil {
		t.Fatalf("meta: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "agent"}, cols: 200, rows: 55, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "LIVE-first-7") }, bootTimeout); err != nil {
		t.Fatalf("client never showed the pane: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	side := func(s tuitest.Screen, want string) bool {
		_, rows := s.Size()
		for r := range rows {
			line := []rune(s.Line(r))
			if len(line) > 154 && strings.Contains(string(line[154:]), want) {
				return true
			}
		}
		return false
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return side(s, "LIVE-first-7") && side(s, "Last turn") && side(s, "token.go") && side(s, "Expiry check with skew") && side(s, "Read the auth package")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the side column lacks the live pane, the last turn or the checkpoints: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	if !strings.Contains(string([]rune(s.Line(rowWith(s, "Checkpoints")))[154:]), " 2") {
		t.Fatalf("ASSERTION: the checkpoint count is not the two the CLI lists\n%s", term.Snapshot())
	}
	// The pane prints while the view is open: the box follows.
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return side(s, "LIVE-second-9") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the live box did not follow the pane's output: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "side-200x55")
	alive(t, term, "after the side column")
}

// TestCockpitSideTabs is the side column's pieces as focus tabs on a screen
// narrower than it: Checkpoints lists them, and Pane shows the live pane.
func TestCockpitSideTabs(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	if out, err := tuiosCLI(t, base, "new", "agent", "--detach"); err != nil {
		t.Fatalf("new session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "worker", "-s", "agent", "--", "/bin/sh", "-c", "printf 'TAB-%s\n' marker-3; sleep 900"); err != nil {
		t.Fatalf("open the agent's pane: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "agent", "-w", "worker", "--harness", "codex", "-m", "Bash go test ./..."); err != nil {
		t.Fatalf("report: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "agent"}, cols: 100, rows: 30, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "TAB-marker-3") }, bootTimeout); err != nil {
		t.Fatalf("client never showed the pane: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the tabs", "Thread", "Checkpoints", "Live")
	if err := term.SendKeys("]"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the checkpoints tab", "Checkpoints need the pane in a git work tree.")
	if err := term.SendKeys("]"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the pane tab", "TAB-marker-3")
	saveCockpitFrames(t, term, "pane-tab-100x30")
	alive(t, term, "after the side tabs")
}
