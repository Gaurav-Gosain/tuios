package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuitest"
)

const (
	ptyCols         = 120
	ptyRows         = 40
	ptyPanes        = 3
	daemonEvery     = 4
	scrollTail      = 400
	settle          = 12 * time.Millisecond
	structureSettle = 2 * time.Second
	spliceSettle    = 1 * time.Second
)

type ptyTarget struct {
	t          *testing.T
	term       *tuitest.Terminal
	base       string
	session    string
	current    string
	cols, rows int
	maxRows    int
	last       fuzz.Action
	step       int
	held       int
	detached   bool

	emitted        map[string]int
	tail           map[string]int
	screenSwitched map[string]bool
	alt            map[string]bool

	wins    []daemonWindow
	focused string

	probe   bool
	pending []fuzz.Violation
}

func newPTYTarget(t *testing.T) func() (fuzz.Target, error) {
	return func() (fuzz.Target, error) {
		return &ptyTarget{t: t, session: "fuzz", cols: ptyCols, rows: ptyRows}, nil
	}
}

func (p *ptyTarget) Reset() error {
	p.cols, p.rows, p.held, p.step = ptyCols, ptyRows, 0, 0
	p.maxRows = ptyRows
	p.detached, p.probe, p.pending = false, true, nil
	p.emitted, p.tail = map[string]int{}, map[string]int{}
	p.alt, p.screenSwitched = map[string]bool{}, map[string]bool{}
	p.wins, p.focused = nil, ""
	p.current = p.session

	p.base = p.t.TempDir()
	killDaemon(p.t, p.base)

	for _, name := range []string{p.session, p.session + "-b"} {
		if out, err := tuiosCLI(p.t, p.base, "new", name, "--detach"); err != nil {
			return fmt.Errorf("create session %s: %w: %s", name, err, strings.TrimSpace(out))
		}
	}
	for range ptyPanes - 1 {
		if out, err := tuiosCLI(p.t, p.base, "new-window", "-s", p.session); err != nil {
			return fmt.Errorf("new-window: %w: %s", err, strings.TrimSpace(out))
		}
	}

	p.term = startIn(p.t, p.base, startOpts{
		cols: p.cols, rows: p.rows,
		args: []string{"attach", p.session},
	})
	if err := waitDock(p.t, p.term); err != nil {
		return fmt.Errorf("the client never rehydrated the session: %w", err)
	}
	p.windowMode()
	if err := p.refresh(); err != nil {
		return err
	}
	for _, w := range p.wins {
		if err := p.burst(w, 12); err != nil {
			return err
		}
	}
	return nil
}

func (p *ptyTarget) Close() {
	if p.term != nil {
		_ = p.term.Close()
		p.term = nil
	}
	if p.base != "" {
		killDaemonNow(p.t, p.base)
	}
}

func (p *ptyTarget) Apply(a fuzz.Action) error {
	p.last = a
	p.step++
	if disturbs(a.Kind) {
		p.probe = true
	}

	switch a.Kind {
	case fuzz.Burst:
		p.applyBurst(a)
	case fuzz.AltScreen:
		p.applyAltScreen(a)
	case fuzz.Guest:
		p.applyGuest(a)
	case fuzz.Detach:
		p.applyDetach()
	case fuzz.Attach:
		p.applyAttach()
	case fuzz.SecondClient:
		p.applySecondClient()
	case fuzz.DaemonRestart:
		p.applyDaemonRestart()
	default:
		if err := p.applyToClient(a); err != nil {
			if _, exited := p.term.ExitCode(); !exited {
				return err
			}
		}
	}
	if p.probe {
		_ = p.refresh()
	}
	time.Sleep(settle)
	return nil
}

func disturbs(k fuzz.Kind) bool {
	switch k {
	case fuzz.Burst, fuzz.AltScreen, fuzz.Guest, fuzz.Detach, fuzz.Attach,
		fuzz.SecondClient, fuzz.DaemonRestart, fuzz.SwitchWorkspace,
		fuzz.SwitchSession, fuzz.NewPane, fuzz.ClosePane, fuzz.Resize:
		return true
	}
	return false
}

func (p *ptyTarget) applyToClient(a fuzz.Action) error {
	t := p.term
	if t == nil {
		return nil
	}
	switch a.Kind {
	case fuzz.Key:
		return t.SendKeys(ptyKey(a.S))
	case fuzz.Chord:
		return t.SendKeys(tuitest.Ctrl('b'), ptyKey(a.S))
	case fuzz.Text:
		return t.Type(a.S)
	case fuzz.MousePress:
		p.held = a.C
		return t.SendMouse(p.mouse(a, tuitest.MousePress))
	case fuzz.MouseMotion:
		return t.SendMouse(p.mouse(a, tuitest.MouseMove))
	case fuzz.MouseRelease:
		p.held = 0
		return t.SendMouse(p.mouse(a, tuitest.MouseRelease))
	case fuzz.MouseWheel:
		return t.SendMouse(p.mouse(a, tuitest.MousePress))
	case fuzz.Resize:
		p.cols, p.rows = max(a.A, 20), max(a.B, 6)
		p.maxRows = max(p.maxRows, p.rows)
		return t.Resize(p.cols, p.rows)
	case fuzz.NewPane:
		return t.SendKeys(tuitest.Ctrl('b'), "c")
	case fuzz.ClosePane:
		return t.SendKeys(tuitest.Ctrl('b'), "x")
	case fuzz.ZoomPane:
		return t.SendKeys(tuitest.Ctrl('b'), "z")
	case fuzz.FocusPane:
		return t.SendKeys(tuitest.Ctrl('b'), "n")
	case fuzz.MovePane:
		return t.SendKeys([]string{"left", "right", "up", "down"}[a.A%4])
	case fuzz.SwitchWorkspace:
		return t.SendKeys(tuitest.Ctrl('b'), "w", strconv.Itoa(1+a.A%9))
	case fuzz.SwitchSession:
		if err := t.SendKeys(tuitest.Alt("N")); err != nil {
			return err
		}
		p.current = attachedSession(p.base, p.current)
		return nil
	case fuzz.ToggleTiling:
		return t.SendKeys(tuitest.Ctrl('b'), " ")
	case fuzz.LayoutMode:
		return t.SendKeys(tuitest.Ctrl('b'), "L")
	case fuzz.ToggleSidebar, fuzz.SidebarCollapse, fuzz.SidebarPosition:
		return t.SendKeys(tuitest.Ctrl('b'), "b")
	case fuzz.OpenOverlay:
		return t.SendKeys(tuitest.Ctrl('b'), []string{"?", "P", "S", "W", ","}[a.A%5])
	case fuzz.CloseOverlay:
		return t.SendKeys("esc")
	case fuzz.Rename:
		if err := t.SendKeys(tuitest.Ctrl('b'), "r"); err != nil {
			return err
		}
		if err := t.Type(sanitiseName(a.S)); err != nil {
			return err
		}
		return t.SendKeys("enter")
	case fuzz.ToggleShared, fuzz.Setting:
		return nil
	case fuzz.Tick:
		time.Sleep(60 * time.Millisecond)
		return nil
	}
	return nil
}

func (p *ptyTarget) applyBurst(a fuzz.Action) {
	w, ok := p.pickWindow(a.B)
	if !ok {
		return
	}
	if err := p.burst(w, a.A); err != nil {
		p.note("burst", "pane %s refused %d lines: %v", w.tag(), a.A, err)
	}
}

func (p *ptyTarget) applyAltScreen(a fuzz.Action) {
	w, ok := p.pickWindow(a.B)
	if !ok {
		return
	}
	enter := a.A%2 == 1
	p.screenSwitched[w.ID] = true
	if err := paneSend(p.base, p.current, w.ID, paneAltCmd(w.tag(), enter)); err != nil {
		return
	}
	if !enter {
		delete(p.alt, w.ID)
		return
	}
	want := "ALT" + w.tag()
	for range 20 {
		time.Sleep(50 * time.Millisecond)
		lines, err := daemonPane(p.base, p.current, w.ID)
		if err != nil {
			return
		}
		if strings.Contains(strings.Join(lines, "\n"), want) {
			p.alt[w.ID] = true
			return
		}
	}
}

func (p *ptyTarget) applyGuest(a fuzz.Action) {
	w, ok := p.focusedWindow()
	if !ok {
		return
	}
	delete(p.alt, w.ID)
	p.screenSwitched[w.ID] = true
	_ = paneSend(p.base, p.current, w.ID, paneEmitCmd(a.S))
}

func (p *ptyTarget) applyDetach() {
	if p.detached || p.term == nil {
		return
	}
	if err := p.term.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		return
	}
	if _, err := p.term.Wait(uiTimeout); err != nil {
		return
	}
	_ = p.term.Close()
	p.term, p.detached = nil, true
}

func (p *ptyTarget) applyAttach() {
	if !p.detached {
		return
	}
	p.term = startIn(p.t, p.base, startOpts{
		cols: p.cols, rows: p.rows,
		args: []string{"attach", p.current},
	})
	if err := waitDock(p.t, p.term); err != nil {
		p.note("attach", "reattaching to %q never produced a dock: %v", p.current, err)
		return
	}
	p.detached = false
	p.windowMode()
}

func (p *ptyTarget) applySecondClient() {
	other := startIn(p.t, p.base, startOpts{
		cols: max(p.cols-13, 40), rows: max(p.rows-5, 12),
		args: []string{"attach", p.current},
	})
	if err := waitDock(p.t, other); err != nil {
		p.note("second-client", "a second client on %q never rehydrated: %v", p.current, err)
	}
	_ = other.Close()
	time.Sleep(200 * time.Millisecond)
}

func (p *ptyTarget) applyDaemonRestart() {
	if out, err := tuiosCLI(p.t, p.base, "kill-server"); err != nil {
		p.note("daemon-restart", "kill-server failed: %v: %s", err, strings.TrimSpace(out))
	}
	if p.term != nil {
		if _, err := p.term.Wait(uiTimeout); err != nil {
			p.note("daemon-restart", "the client outlived its daemon by %s", uiTimeout)
		}
		_ = p.term.Close()
		p.term = nil
	}
	p.detached = true

	p.term = startIn(p.t, p.base, startOpts{
		cols: p.cols, rows: p.rows,
		args: []string{"attach", p.current},
	})
	if err := waitDock(p.t, p.term); err != nil {
		p.note("daemon-restart", "%q did not come back after a restart: %v", p.current, err)
		return
	}
	p.detached = false
	p.windowMode()
	p.tail, p.alt = map[string]int{}, map[string]bool{}
}

func (p *ptyTarget) Check() []fuzz.Violation {
	if vs := p.pending; len(vs) > 0 {
		p.pending = nil
		return vs
	}
	if vs := p.checkClient(); len(vs) > 0 {
		return vs
	}
	if !p.probe && p.step%daemonEvery != 0 {
		return nil
	}
	p.probe = false
	return p.checkDaemon()
}

func (p *ptyTarget) checkClient() []fuzz.Violation {
	t := p.term
	if t == nil {
		return nil
	}
	if code, exited := t.ExitCode(); exited {
		if code == 0 {
			p.term, p.detached = nil, true
			return nil
		}
		return one("pty-exit", "tuios exited with code %d after %s", code, p.last)
	}
	s := t.Screen()
	text := s.Text()
	for _, marker := range []string{"panic:", "goroutine ", "runtime error:", "SIGSEGV"} {
		if strings.Contains(text, marker) {
			return one("pty-panic", "the screen shows %q after %s", marker, p.last)
		}
	}
	cols, rows := s.Size()
	if cols != p.cols || rows != p.rows {
		return one("pty-size", "the grid is %dx%d, the PTY is %dx%d", cols, rows, p.cols, p.rows)
	}
	if a, b, found := spliceIn(screenLines(s)); found {
		if a2, b2, still := p.spliceHolds(); still {
			return one("client-splice",
				"pane %s shows line %d directly above line %d, still there after %s, "+
					"so the client replayed a stream across a hole rather than "+
					"catching a repaint mid-frame; first seen as %d above %d after %s",
				a2.tag, a2.seq, b2.seq, spliceSettle, a.seq, b.seq, p.last)
		}
	}
	return nil
}

func (p *ptyTarget) spliceHolds() (witness, witness, bool) {
	deadline := time.Now().Add(spliceSettle)
	var a, b witness
	for {
		x, y, found := spliceIn(screenLines(p.term.Screen()))
		if !found {
			return witness{}, witness{}, false
		}
		a, b = x, y
		if !time.Now().Before(deadline) {
			return a, b, true
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (p *ptyTarget) checkDaemon() []fuzz.Violation {
	if _, err := daemonInfo(p.base, p.current); err != nil {
		return one("daemon-reachable", "session-info for %q failed after %s: %v\n%s",
			p.current, p.last, err, daemonLogTail(p.base, 60))
	}
	wl, err := daemonWindows(p.base, p.current)
	if err != nil {
		return one("daemon-reachable", "list-windows for %q failed after %s: %v",
			p.current, p.last, err)
	}
	p.wins, p.focused = wl.Windows, wl.FocusedWindowID

	if vs := p.checkStructure(); len(vs) > 0 {
		return vs
	}
	if vs := p.checkPanes(); len(vs) > 0 {
		return vs
	}
	return p.checkProvenance()
}

func (p *ptyTarget) checkStructure() []fuzz.Violation {
	if p.term == nil {
		return nil
	}
	deadline := time.Now().Add(structureSettle)
	var wantWS, gotWS, wantWins, gotWins int
	for {
		p.followSession()
		s := p.term.Screen()
		gotWins, gotWS = countWindows(s), dockWorkspace(s)
		info, err := daemonInfo(p.base, p.current)
		if err != nil {
			return nil
		}
		wantWS = info.CurrentWorkspace
		if gotWins < 0 || gotWS < 1 || gotWS > max(info.NumWorkspaces, 1) {
			return nil
		}
		wl, err := daemonWindows(p.base, p.current)
		if err != nil {
			return nil
		}
		wantWins = 0
		for _, w := range wl.Windows {
			if w.Workspace == wantWS {
				wantWins++
			}
		}
		if gotWS == wantWS && gotWins == wantWins {
			return nil
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(120 * time.Millisecond)
	}
	if gotWS != wantWS {
		return one("daemon-workspace",
			"the dock still says workspace %d after %s and the daemon says %d, following %s",
			gotWS, structureSettle, wantWS, p.last)
	}
	return one("daemon-window-count",
		"the dock still counts %d windows on workspace %d after %s and the daemon "+
			"lists %d for session %q (attached: %v), following %s; the client shows:\n%s",
		gotWins, wantWS, structureSettle, wantWins, p.current, attachedSessions(p.base),
		p.last, p.term.Snapshot())
}

func (p *ptyTarget) checkPanes() []fuzz.Violation {
	for _, w := range p.wins {
		grid, err := daemonPane(p.base, p.current, w.ID)
		if err != nil {
			continue
		}
		if a, b, found := spliceIn(grid); found {
			return one("daemon-splice",
				"the daemon's own grid for pane %s has line %d directly above line %d "+
					"after %s, so the hole is in the daemon rather than in the client",
				a.tag, a.seq, b.seq, p.last)
		}
		if vs := p.checkAlt(w, grid); len(vs) > 0 {
			return vs
		}
		if vs := p.checkScrollback(w, grid); len(vs) > 0 {
			return vs
		}
	}
	return nil
}

func (p *ptyTarget) checkAlt(w daemonWindow, grid []string) []fuzz.Violation {
	if !p.alt[w.ID] {
		return nil
	}
	joined := strings.Join(grid, "\n")
	if !altMarkerIn(grid, w.tag()) {
		if mainEchoRe.MatchString(joined) {
			return one("altscreen-retained",
				"pane %s was on the alternate screen and after %s the daemon's grid "+
					"shows the main screen instead (%dx%d):\n%s",
				w.tag(), p.last, w.Width, w.Height, joined)
		}
		delete(p.alt, w.ID)
		return nil
	}
	if _, _, any := seqRange(grid, w.tag()); any {
		return one("altscreen-retained",
			"pane %s is on the alternate screen and the daemon's grid for it also "+
				"carries main-screen output after %s", w.tag(), p.last)
	}
	return nil
}

func (p *ptyTarget) checkScrollback(w daemonWindow, grid []string) []fuzz.Violation {
	if p.emitted[w.ID] == 0 {
		return nil
	}
	if _, _, any := seqRange(grid, w.tag()); !any {
		return nil
	}
	if p.screenSwitched[w.ID] {
		return nil
	}
	hist, err := daemonScrollback(p.base, p.current, w.ID, scrollTail)
	if err != nil {
		return nil
	}
	if a, b, found := spliceIn(hist); found {
		return one("scrollback-retained",
			"pane %s has line %d directly above line %d in its history after %s",
			a.tag, a.seq, b.seq, p.last)
	}
	_, hi, any := seqRange(hist, w.tag())
	if !any {
		return nil
	}
	screenful := max(w.Height, p.maxRows)
	if screenful <= 0 {
		screenful = p.rows
	}
	if hi < p.tail[w.ID]-screenful-2 {
		return one("scrollback-retained",
			"pane %s held history up to line %d and now ends at %d after %s, "+
				"which is more than the screenful an erase could account for",
			w.tag(), p.tail[w.ID], hi, p.last)
	}
	if hi > p.tail[w.ID] {
		p.tail[w.ID] = hi
	}
	return nil
}

func (p *ptyTarget) checkProvenance() []fuzz.Violation {
	if p.term == nil {
		return nil
	}
	client := screenLines(p.term.Screen())
	for _, w := range p.wins {
		tag := w.tag()
		_, clientHi, any := seqRange(client, tag)
		if !any {
			continue
		}
		if clientHi > p.emitted[w.ID] {
			return one("witness-provenance",
				"the client shows pane %s at line %d and only %d were ever written, after %s",
				tag, clientHi, p.emitted[w.ID], p.last)
		}
		grid, err := daemonPane(p.base, p.current, w.ID)
		if err != nil {
			continue
		}
		_, daemonHi, ok := seqRange(grid, tag)
		if !ok {
			continue
		}
		if clientHi > daemonHi {
			return one("client-ahead",
				"the client shows pane %s up to line %d, the daemon's grid ends at %d, after %s",
				tag, clientHi, daemonHi, p.last)
		}
	}
	return nil
}

func (p *ptyTarget) Rules() []fuzz.RuleInfo {
	return []fuzz.RuleInfo{
		{Name: "pty-exit", Family: "process", Doc: "the client under test exited on its own"},
		{Name: "pty-panic", Family: "process", Doc: "the client printed a panic to its own screen"},
		{Name: "pty-size", Family: "geometry", Doc: "the client's grid and its PTY disagree about the size"},
		{Name: "client-splice", Family: "client", Doc: "the client shows two output lines with the middle missing"},
		{Name: "daemon-reachable", Family: "daemon", Doc: "the daemon stopped answering while a client was attached"},
		{Name: "daemon-workspace", Family: "daemon", Doc: "the dock and the daemon disagree about the current workspace"},
		{Name: "daemon-window-count", Family: "daemon", Doc: "the dock and the daemon disagree about how many panes exist"},
		{Name: "daemon-splice", Family: "daemon", Doc: "the daemon's own grid has a gap in a pane's output"},
		{Name: "altscreen-retained", Family: "scrollback", Doc: "a pane left the alternate screen behind without being told to"},
		{Name: "scrollback-retained", Family: "scrollback", Doc: "history a pane held has gone missing"},
		{Name: "witness-provenance", Family: "client", Doc: "the client shows a line further than anything ever written"},
		{Name: "client-ahead", Family: "client", Doc: "the client shows output the daemon never produced"},
		{Name: "burst", Family: "session", Doc: "a pane dropped lines from a burst it was sent"},
		{Name: "attach", Family: "session", Doc: "a reattached client never rehydrated its panes"},
		{Name: "second-client", Family: "session", Doc: "a second client on the same session never rehydrated"},
		{Name: "daemon-restart", Family: "session", Doc: "sessions did not come back after the daemon restarted"},
	}
}

func (p *ptyTarget) note(rule, format string, args ...any) {
	p.pending = append(p.pending, fuzz.Violation{
		Rule:   rule,
		Detail: fmt.Sprintf(format, args...),
	})
}

func daemonLogTail(base string, lines int) string {
	data, err := os.ReadFile(filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log"))
	if err != nil {
		return "(no daemon log: " + err.Error() + ")"
	}
	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return "daemon log tail:\n" + strings.Join(all, "\n")
}

func one(rule, format string, args ...any) []fuzz.Violation {
	return []fuzz.Violation{{Rule: rule, Detail: fmt.Sprintf(format, args...)}}
}

func (p *ptyTarget) refresh() error {
	p.followSession()
	wl, err := daemonWindows(p.base, p.current)
	if err != nil {
		return err
	}
	p.wins, p.focused = wl.Windows, wl.FocusedWindowID
	return nil
}

func (p *ptyTarget) pickWindow(i int) (daemonWindow, bool) {
	if len(p.wins) == 0 && p.refresh() != nil {
		return daemonWindow{}, false
	}
	if len(p.wins) == 0 {
		return daemonWindow{}, false
	}
	n := len(p.wins)
	return p.wins[((i%n)+n)%n], true
}

func (p *ptyTarget) focusedWindow() (daemonWindow, bool) {
	for _, w := range p.wins {
		if w.ID == p.focused {
			return w, true
		}
	}
	return p.pickWindow(0)
}

func (p *ptyTarget) burst(w daemonWindow, n int) error {
	n = min(max(n, 1), 5000)
	delete(p.alt, w.ID)
	lo := p.emitted[w.ID] + 1
	hi := lo + n - 1
	p.emitted[w.ID] = hi
	return paneSend(p.base, p.current, w.ID, paneWitnessCmd(w.tag(), lo, hi))
}

func (p *ptyTarget) windowMode() {
	if p.term == nil {
		return
	}
	_ = p.term.SendKeys(tuitest.Alt(tuitest.Esc))
	_ = p.term.WaitForText("Window management mode", uiTimeout)
}

func attachedSession(base, current string) string {
	deadline := time.Now().Add(4 * time.Second)
	for {
		for _, name := range attachedSessions(base) {
			if name != current {
				return name
			}
		}
		if !time.Now().Before(deadline) {
			return current
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (p *ptyTarget) followSession() {
	if p.term == nil {
		return
	}
	if names := attachedSessions(p.base); len(names) == 1 && names[0] != p.current {
		p.current = names[0]
	}
}

func attachedSessions(base string) []string {
	var sessions []struct {
		Name     string `json:"name"`
		Attached bool   `json:"attached"`
	}
	out, err := tuiosOut(base, "ls", "--json")
	if err != nil || json.Unmarshal([]byte(out), &sessions) != nil {
		return nil
	}
	var names []string
	for _, s := range sessions {
		if s.Attached {
			names = append(names, s.Name)
		}
	}
	return names
}

func (p *ptyTarget) mouse(a fuzz.Action, action tuitest.MouseAction) tuitest.MouseEvent {
	return tuitest.MouseEvent{
		Col:    min(max(a.A, 0), p.cols-1),
		Row:    min(max(a.B, 0), p.rows-1),
		Button: ptyButton(a.C),
		Action: action,
	}
}

func ptyButton(c int) tuitest.MouseButton {
	switch c {
	case fuzz.ButtonRight:
		return tuitest.MouseRight
	case fuzz.ButtonMiddle:
		return tuitest.MouseMiddle
	}
	return tuitest.MouseLeft
}

func ptyKey(name string) any {
	if rest, ok := strings.CutPrefix(name, "ctrl+"); ok && len(rest) == 1 {
		return tuitest.Ctrl(rune(rest[0]))
	}
	if rest, ok := strings.CutPrefix(name, "alt+"); ok {
		return tuitest.Alt(rest)
	}
	if rest, ok := strings.CutPrefix(name, "shift+"); ok {
		return strings.ToUpper(rest)
	}
	if name == "space" {
		return " "
	}
	return name
}

func sanitiseName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r != 0x7f {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "a"
	}
	return b.String()
}

func ptyWeights() []int {
	w := make([]int, int(fuzz.DaemonRestart)+1)
	w[fuzz.Key] = 40
	w[fuzz.Chord] = 70
	w[fuzz.Text] = 0
	w[fuzz.MousePress] = 30
	w[fuzz.MouseMotion] = 40
	w[fuzz.MouseRelease] = 30
	w[fuzz.MouseWheel] = 15
	w[fuzz.Resize] = 45
	w[fuzz.NewPane] = 35
	w[fuzz.ClosePane] = 12
	w[fuzz.ZoomPane] = 18
	w[fuzz.FocusPane] = 25
	w[fuzz.MovePane] = 15
	w[fuzz.SwitchWorkspace] = 55
	w[fuzz.SwitchSession] = 25
	w[fuzz.ToggleTiling] = 30
	w[fuzz.ToggleShared] = 0
	w[fuzz.LayoutMode] = 18
	w[fuzz.ToggleSidebar] = 18
	w[fuzz.SidebarCollapse] = 0
	w[fuzz.SidebarPosition] = 0
	w[fuzz.OpenOverlay] = 20
	w[fuzz.CloseOverlay] = 18
	w[fuzz.Rename] = 12
	w[fuzz.Detach] = 20
	w[fuzz.Attach] = 20
	w[fuzz.Setting] = 0
	w[fuzz.Tick] = 20
	w[fuzz.Guest] = 35
	w[fuzz.AltScreen] = 30
	w[fuzz.Burst] = 55
	w[fuzz.SecondClient] = 22
	w[fuzz.DaemonRestart] = 6
	return w
}

// TestFuzzPTY is the bounded PTY campaign:
//
//	cd e2e/tui && TUIOS_E2E=1 go test -count=1 -run TestFuzzPTY ./...
//
// Seeds and steps are settable so a local run can go wider:
//
//	TUIOS_E2E=1 TUIOS_FUZZ_SEEDS=200 TUIOS_FUZZ_STEPS=120 \
//	  go test -count=1 -run TestFuzzPTY -timeout 4h ./...
//
// TUIOS_FUZZ_FIRST moves the starting seed, which is what makes a wide campaign
// survivable. A panic in tuitest's emulator takes the test binary down with
// every finding it had not yet printed (see the third footgun in
// harness_test.go). Batches of ten cost one batch when that happens instead of
// the whole run:
//
//	for f in 0 10 20 30; do
//	  TUIOS_E2E=1 TUIOS_FUZZ_FIRST=$f TUIOS_FUZZ_SEEDS=10 \
//	    go test -count=1 -run TestFuzzPTY -timeout 1h ./...
//	done
func TestFuzzPTY(t *testing.T) {
	first := uint64(ptyEnvInt(t, "TUIOS_FUZZ_FIRST", 0))
	seeds := ptyEnvInt(t, "TUIOS_FUZZ_SEEDS", 2)
	steps := ptyEnvInt(t, "TUIOS_FUZZ_STEPS", 40)
	shrink := os.Getenv("TUIOS_FUZZ_SHRINK") != ""

	for i := range uint64(seeds) {
		seed := first + i
		res, err := fuzz.Run(newPTYTarget(t), fuzz.Config{
			Seed: seed, Steps: steps,
			MinWidth: 40, MinHeight: 12,
			Weights:  ptyWeights(),
			NoShrink: !shrink, ShrinkBudget: 40,
		})
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if res.Failed {
			t.Errorf("seed %d broke %s\n%s", seed, res.Violations[0].Rule, res.Repro())
		}
	}
}

func ptyEnvInt(t *testing.T, name string, def int) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("%s=%q: %v", name, v, err)
	}
	return n
}
