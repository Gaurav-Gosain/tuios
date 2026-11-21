package app

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	uv "github.com/charmbracelet/ultraviolet"
)

const (
	rigCols             = 80
	rigRows             = 24
	rigWait             = 10 * time.Second
	rigScrollbackOracle = 20000
)

type rig struct {
	t              *testing.T
	daemon         *session.Daemon
	client         *session.TUIClient
	m              *OS
	ctl            *session.TUIClient
	session        string
	other          string
	rebuiltWindows bool
	cols, rows     int
	keepExits      bool
	producing      string
}

func keepExits(r *rig) { r.keepExits = true }

func ownSocket(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
}

func rigShell() string {
	if _, err := os.Stat("/bin/dash"); err == nil {
		return "/bin/dash"
	}
	return "/bin/sh"
}

func newRig(t *testing.T, panes int, opts ...func(*rig)) *rig {
	t.Helper()
	return newRigSized(t, panes, rigCols, rigRows, opts...)
}

func newRigSized(t *testing.T, panes, cols, rows int, opts ...func(*rig)) *rig {
	t.Helper()
	ownSocket(t)
	t.Setenv("SHELL", rigShell())
	t.Setenv("PS1", "$ ")

	d := session.NewDaemon(&session.DaemonConfig{Version: "test", DisableAutoRestore: true, WindowSize: config.WindowSizeSmallest})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(d.Stop)

	name := "rehydrate"
	boot := session.NewTUIClient()
	if err := boot.Connect("test", cols, rows); err != nil {
		t.Fatalf("bootstrap connect: %v", err)
	}
	if err := boot.CreateDetachedSession(name, cols, rows); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := boot.AttachSession(name, false, cols, rows); err != nil {
		t.Fatalf("bootstrap attach: %v", err)
	}
	boot.StartReadLoop()
	for range panes - 1 {
		if err := boot.SendIntent("NewWindow"); err != nil {
			t.Fatalf("new window: %v", err)
		}
	}
	rigWaitUntil(t, fmt.Sprintf("the session to hold %d windows", panes), func() bool {
		list, err := boot.RefreshSessionList()
		if err != nil {
			return false
		}
		for _, s := range list {
			if s.Name == name {
				return s.WindowCount == panes
			}
		}
		return false
	})
	ctl := session.NewTUIClient()
	if err := ctl.Connect("test", cols, rows); err != nil {
		t.Fatalf("control connect: %v", err)
	}
	if _, err := ctl.AttachSession(name, false, cols, rows); err != nil {
		t.Fatalf("control attach: %v", err)
	}
	ctl.StartReadLoop()
	t.Cleanup(func() { _ = ctl.Close() })

	if err := boot.Detach(); err != nil {
		t.Fatalf("bootstrap detach: %v", err)
	}
	_ = boot.Close()

	r := &rig{t: t, daemon: d, ctl: ctl, session: name, cols: cols, rows: rows}
	for _, opt := range opts {
		opt(r)
	}
	r.attach()
	return r
}

func (r *rig) otherSession() string {
	r.t.Helper()
	if r.other != "" {
		return r.other
	}
	r.other = "elsewhere"
	if err := r.ctl.CreateDetachedSession(r.other, rigCols, rigRows); err != nil {
		r.t.Fatalf("create second session: %v", err)
	}
	return r.other
}

func (r *rig) attach() {
	r.t.Helper()
	r.m, r.client = attachClientOS(r.t, r.session, r.cols, r.rows, r.keepExits)
}

func attachClientOS(t *testing.T, name string, cols, rows int, keepExits bool) (*OS, *session.TUIClient) {
	t.Helper()

	c := session.NewTUIClient()
	if err := c.Connect("test", cols, rows); err != nil {
		t.Fatalf("connect: %v", err)
	}
	state, err := c.AttachSession(name, false, cols, rows)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	c.StartReadLoop()
	t.Cleanup(func() { _ = c.Close() })

	m := NewOS(OSOptions{
		UserConfig:      config.DefaultConfig(),
		IsDaemonSession: true,
		DaemonClient:    c,
		SessionName:     c.SessionName(),
		Width:           cols,
		Height:          rows,
	})
	if m.KeybindRegistry == nil {
		m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())
	}
	m.Width, m.Height = cols, rows
	m.EffectiveWidth, m.EffectiveHeight = cols, rows

	if state == nil || len(state.Windows) == 0 {
		t.Fatalf("attach returned no windows")
	}
	if err := m.RestoreFromState(state); err != nil {
		t.Fatalf("restore state: %v", err)
	}
	if err := m.RestoreTerminalStates(); err != nil {
		t.Fatalf("restore terminal states: %v", err)
	}
	if err := m.SetupPTYOutputHandlers(); err != nil {
		t.Fatalf("setup pty handlers: %v", err)
	}
	if m.AutoTiling {
		m.TileAllWindows()
	}
	m.SyncDaemonPTYDimensions()
	for _, w := range m.Windows {
		w.EnableCallbacks()
	}
	if !keepExits {
		drainExits(t, m)
	}
	return m, c
}

func drainExits(t *testing.T, m *OS) {
	t.Helper()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-m.WindowExitChan:
			}
		}
	}()
}

func (r *rig) detach() {
	r.t.Helper()
	for _, w := range r.m.Windows {
		w.Close()
	}
	if err := r.client.Detach(); err != nil {
		r.t.Fatalf("detach: %v", err)
	}
	_ = r.client.Close()
}

func (r *rig) win(i int) *terminal.Window {
	r.t.Helper()
	if i >= len(r.m.Windows) {
		r.t.Fatalf("window %d of %d", i, len(r.m.Windows))
	}
	return r.m.Windows[i]
}

func (r *rig) winByPTY(ptyID string) *terminal.Window {
	r.t.Helper()
	for _, w := range r.m.Windows {
		if w.PTYID == ptyID {
			return w
		}
	}
	r.t.Fatalf("no window for PTY %s among %d windows", ptyID, len(r.m.Windows))
	return nil
}

func (r *rig) daemonCells(ptyID string, maxScrollback int) (*session.TerminalState, error) {
	st, err := r.ctl.GetTerminalState(ptyID, maxScrollback, 0)
	if err != nil || st == nil {
		return st, err
	}
	return st, st.Unpack()
}

func (r *rig) ptySize(ptyID string) (int, int) {
	r.t.Helper()
	st, err := r.ctl.GetTerminalState(ptyID, -1, 0)
	if err != nil || st == nil {
		r.t.Fatalf("read pane size: %v", err)
	}
	return st.Width, st.Height
}

func (r *rig) startPTY(ptyID, command string) {
	r.t.Helper()
	r.typeAtPrompt(ptyID, command)
}

func (r *rig) feedPTY(ptyID, command, want string) {
	r.t.Helper()
	r.typeAtPrompt(ptyID, command)
	r.waitDaemonShows(ptyID, want)
}

func (r *rig) typeAtPrompt(ptyID, command string) {
	r.t.Helper()
	deadline := time.Now().Add(rigWait)
	for !r.atPrompt(ptyID) {
		if time.Now().After(deadline) {
			st, err := r.daemonCells(ptyID, -1)
			screen := ""
			if err == nil && st != nil {
				screen = fmt.Sprintf("cursor %d,%d\n%s", st.CursorX, st.CursorY, stateText(st))
			}
			r.t.Fatalf("timed out waiting for the shell's prompt before typing %q (err %v); the daemon's screen:\n%s\nthis process's children:\n%s",
				command, err, screen, childProcesses())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := r.ctl.WritePTY(ptyID, []byte(command+"\n")); err != nil {
		r.t.Fatalf("write pty: %v", err)
	}
}

func childProcesses() string {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,pgid=,stat=,wchan=,etime=,command=").Output()
	if err != nil {
		return "(ps: " + err.Error() + ")"
	}
	self := strconv.Itoa(os.Getpid())
	var b strings.Builder
	for line := range strings.SplitSeq(string(out), "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[1] == self {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (r *rig) atPrompt(ptyID string) bool {
	st, err := r.daemonCells(ptyID, -1)
	if err != nil || st == nil || st.CursorY < 0 || st.CursorY >= len(st.Screen) {
		return false
	}
	row := st.Screen[st.CursorY]
	var before strings.Builder
	for x := 0; x < st.CursorX && x < len(row); x++ {
		if c := row[x].Content; c != "" {
			before.WriteString(c)
		} else {
			before.WriteByte(' ')
		}
	}
	if !strings.HasSuffix(before.String(), "$ ") {
		text := stateRow(row)
		return strings.Contains(text, "$") && strings.Trim(text, "$ ") == ""
	}
	for x := st.CursorX; x < len(row); x++ {
		if c := row[x].Content; c != "" && c != " " {
			return false
		}
	}
	return true
}

func (r *rig) waitDaemonShows(ptyID, want string) {
	r.t.Helper()
	deadline := time.Now().Add(rigWait)
	for !r.daemonShows(ptyID, want) {
		if time.Now().After(deadline) {
			st, err := r.daemonCells(ptyID, -1)
			screen := ""
			if err == nil && st != nil {
				screen = stateText(st)
			}
			r.t.Fatalf("timed out waiting for the daemon to show %s (err %v); the daemon's screen:\n%s", want, err, screen)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (r *rig) daemonShows(ptyID, want string) bool {
	r.t.Helper()
	st, err := r.daemonCells(ptyID, rigScrollbackOracle)
	if err != nil || st == nil {
		return false
	}
	return strings.Contains(stateText(st), want)
}

func (r *rig) converge(ptyID string) {
	r.t.Helper()
	deadline := time.Now().Add(rigWait)
	for time.Now().Before(deadline) {
		st, err := r.daemonCells(ptyID, rigScrollbackOracle)
		if err == nil && st != nil {
			if strings.HasSuffix("\n"+stateText(st), "\n"+clientText(r.winByPTY(ptyID))) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (r *rig) latestRPLine(ptyID string) int {
	r.t.Helper()
	st, err := r.daemonCells(ptyID, rigScrollbackOracle)
	if err != nil || st == nil {
		r.t.Fatalf("read the daemon's copy: %v", err)
	}
	latest := 0
	for _, m := range rpLine.FindAllStringSubmatch(stateText(st), -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > latest {
			latest = n
		}
	}
	return latest
}

var rpLine = regexp.MustCompile(`RP-([0-9]+)-A`)

func (r *rig) settle() {
	r.t.Helper()
	prev := ""
	stable := 0
	deadline := time.Now().Add(rigWait)
	for time.Now().Before(deadline) {
		var b strings.Builder
		for _, w := range r.m.Windows {
			b.WriteString(clientText(w))
		}
		if cur := b.String(); cur == prev {
			stable++
			if stable >= 5 {
				return
			}
		} else {
			stable = 0
			prev = cur
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func rigWaitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(rigWait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func stateCellSig(cs session.CellState) string {
	if (cs.Content == "" || cs.Content == " ") && cs.FgColor == "" && cs.BgColor == "" &&
		cs.UlColor == "" && cs.Attrs == 0 && cs.Underline == 0 && cs.LinkURL == "" {
		return " "
	}
	return fmt.Sprintf("%q|%d|%s|%s|%s|%08b|%d|%s",
		cs.Content, cs.Width, cs.FgColor, cs.BgColor, cs.UlColor,
		cs.Attrs, cs.Underline, cs.LinkURL)
}

func uvCellSig(cell *uv.Cell) string {
	if cell == nil {
		return " "
	}
	return stateCellSig(session.CellStateOf(cell))
}

func stateRow(row []session.CellState) string {
	var b strings.Builder
	for _, c := range row {
		if c.Content == "" {
			b.WriteByte(' ')
			continue
		}
		b.WriteString(c.Content)
	}
	return strings.TrimRight(b.String(), " ")
}

func stateText(st *session.TerminalState) string {
	var b strings.Builder
	for _, row := range st.Scrollback {
		b.WriteString(stateRow(row))
		b.WriteByte('\n')
	}
	for _, row := range st.Screen {
		b.WriteString(stateRow(row))
		b.WriteByte('\n')
	}
	return b.String()
}

func clientText(w *terminal.Window) string {
	if w.Terminal == nil {
		return ""
	}
	w.RLockIO()
	defer w.RUnlockIO()
	var b strings.Builder
	for i := range w.Terminal.ScrollbackLen() {
		line := w.Terminal.ScrollbackLine(i)
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	width, height := w.Terminal.Width(), w.Terminal.Height()
	for y := range height {
		var row strings.Builder
		for x := range width {
			cell := w.Terminal.CellAt(x, y)
			if cell == nil || cell.Content == "" {
				row.WriteByte(' ')
				continue
			}
			row.WriteString(cell.Content)
		}
		b.WriteString(strings.TrimRight(row.String(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

func (r *rig) feed(w *terminal.Window, command, want string) {
	r.t.Helper()
	r.typeAtPrompt(w.PTYID, command)
	r.waitDaemonShows(w.PTYID, want)
}
