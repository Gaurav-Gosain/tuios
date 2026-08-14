package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// This is the rig the rehydration matrix runs on: a real daemon in this
// process, a real TUIClient over its socket, and a real OS driven through the
// same entry points cmd/tuios uses to attach. Nothing here reimplements the
// client; the point of the matrix is to compare what the client's emulator ends
// up holding against what the daemon's holds, and a reimplementation would only
// prove itself right.

const (
	rigCols = 80
	rigRows = 24
	rigWait = 10 * time.Second
	// rigScrollbackOracle asks for more history than any case here produces, so
	// the comparison is never limited by what the wire chose to carry.
	rigScrollbackOracle = 20000
)

type rig struct {
	t      *testing.T
	daemon *session.Daemon
	client *session.TUIClient
	m      *OS
	// ctl is attached for the whole test and never subscribes to a pane. It is
	// how the test reads the daemon's copy and types at a shell across a route
	// that closes the client under test's connection. Not subscribing keeps it
	// out of the ring's bookkeeping, which is the thing under test.
	ctl     *session.TUIClient
	session string
	other   string
}

// ownSocket gives this test its own daemon socket. The whole binary already
// runs against a throwaway XDG tree (see TestMain), but every test in it shares
// that tree, and two daemons cannot share one socket. GetSocketPath reads the
// variable on each call, so moving it here is enough and no xdg reload is
// involved.
func ownSocket(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
}

// newRig brings up a daemon, creates a session with panes windows, and attaches
// a client OS to it by the attach path. The returned rig is at route "first
// attach" with every pane subscribed.
func newRig(t *testing.T, panes int) *rig {
	t.Helper()
	ownSocket(t)
	// A predictable shell keeps the pane's own output out of the comparison's
	// way; the oracle is daemon-versus-client, so any prompt appears on both
	// sides, but a shell that draws its own banner makes a failure unreadable.
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("PS1", "$ ")

	d := session.NewDaemon(&session.DaemonConfig{Version: "test", DisableAutoRestore: true})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(d.Stop)

	name := "rehydrate"
	boot := session.NewTUIClient()
	if err := boot.Connect("test", rigCols, rigRows); err != nil {
		t.Fatalf("bootstrap connect: %v", err)
	}
	// A detached create comes back with one real window and one real PTY;
	// attaching to a name that does not exist yet comes back empty.
	if err := boot.CreateDetachedSession(name, rigCols, rigRows); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := boot.AttachSession(name, false, rigCols, rigRows); err != nil {
		t.Fatalf("bootstrap attach: %v", err)
	}
	boot.StartReadLoop()
	// A new session comes with one window; ask for the rest.
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
	if err := boot.Detach(); err != nil {
		t.Fatalf("bootstrap detach: %v", err)
	}
	_ = boot.Close()

	ctl := session.NewTUIClient()
	if err := ctl.Connect("test", rigCols, rigRows); err != nil {
		t.Fatalf("control connect: %v", err)
	}
	if _, err := ctl.AttachSession(name, false, rigCols, rigRows); err != nil {
		t.Fatalf("control attach: %v", err)
	}
	ctl.StartReadLoop()
	t.Cleanup(func() { _ = ctl.Close() })

	r := &rig{t: t, daemon: d, ctl: ctl, session: name}
	r.attach()
	return r
}

// otherSession creates and names a second session for the session-switch route
// to travel through.
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

// attach runs the client-side attach sequence cmd/tuios runs: connect, attach,
// build the OS, restore state, restore terminal content, wire the PTYs.
func (r *rig) attach() {
	t := r.t
	t.Helper()

	c := session.NewTUIClient()
	if err := c.Connect("test", rigCols, rigRows); err != nil {
		t.Fatalf("connect: %v", err)
	}
	state, err := c.AttachSession(r.session, false, rigCols, rigRows)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	c.StartReadLoop()
	t.Cleanup(func() { _ = c.Close() })
	r.client = c

	m := NewOS(OSOptions{
		UserConfig:      config.DefaultConfig(),
		IsDaemonSession: true,
		DaemonClient:    c,
		SessionName:     c.SessionName(),
		Width:           rigCols,
		Height:          rigRows,
	})
	if m.KeybindRegistry == nil {
		m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())
	}
	m.Width, m.Height = rigCols, rigRows
	m.EffectiveWidth, m.EffectiveHeight = rigCols, rigRows
	r.m = m

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
	// RestoreFromState leaves VT callbacks suppressed for Update to re-enable,
	// and nothing here runs Update. Without this the alt-screen flag never
	// tracks live output and the alt-screen rows of the matrix would pass by
	// never noticing anything.
	for _, w := range m.Windows {
		w.EnableCallbacks()
	}
	// OnPTYClosed sends on WindowExitChan from the client read loop, which is
	// the same goroutine that dispatches PTY output. A shell that exits with
	// nobody reading would wedge the whole stream.
	drainExits(t, m)
}

// drainExits keeps the window-exit channel empty for the test's lifetime.
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

// detach drops the OS and its connection, as leaving a session does.
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

// win returns the OS's i'th window in the order the daemon lists them.
func (r *rig) win(i int) *terminal.Window {
	r.t.Helper()
	if i >= len(r.m.Windows) {
		r.t.Fatalf("window %d of %d", i, len(r.m.Windows))
	}
	return r.m.Windows[i]
}

// winByPTY finds the window a pane is showing in now. A route that rebuilds the
// window set hands back a different pointer for the same PTY, so nothing may
// hold a window across a route.
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

// ptySize reports the size the daemon has the pane at.
func (r *rig) ptySize(ptyID string) (int, int) {
	r.t.Helper()
	st, err := r.ctl.GetTerminalState(ptyID, -1)
	if err != nil || st == nil {
		r.t.Fatalf("read pane size: %v", err)
	}
	return st.Width, st.Height
}

// startPTY types a command at a pane and returns without waiting for it, so the
// pane is still producing while the caller goes on.
func (r *rig) startPTY(ptyID, command string) {
	r.t.Helper()
	if err := r.ctl.WritePTY(ptyID, []byte(command+"\n")); err != nil {
		r.t.Fatalf("write pty: %v", err)
	}
}

// feedPTY is feed addressed by PTY, for the shapes that run while the client
// holds no window for the pane at all.
func (r *rig) feedPTY(ptyID, command, want string) {
	r.t.Helper()
	if err := r.ctl.WritePTY(ptyID, []byte(command+"\n")); err != nil {
		r.t.Fatalf("write pty: %v", err)
	}
	r.waitDaemonShows(ptyID, want)
}

// feed writes a shell command to a pane and waits for want to appear in the
// daemon's own copy of the screen, so the pane is settled on the authoritative
// side before anything is compared.
func (r *rig) feed(w *terminal.Window, command, want string) {
	r.t.Helper()
	if err := r.ctl.WritePTY(w.PTYID, []byte(command+"\n")); err != nil {
		r.t.Fatalf("write pty: %v", err)
	}
	r.waitDaemonShows(w.PTYID, want)
}

// waitDaemonShows blocks until the daemon's emulator shows want on screen or in
// its scrollback.
func (r *rig) waitDaemonShows(ptyID, want string) {
	r.t.Helper()
	rigWaitUntil(r.t, "the daemon to show "+want, func() bool {
		st, err := r.ctl.GetTerminalState(ptyID, rigScrollbackOracle)
		if err != nil || st == nil {
			return false
		}
		return strings.Contains(stateText(st), want)
	})
}

// converge waits for the two copies of a pane to agree, or gives up and lets
// the comparison report how they differ.
//
// The daemon broadcasts a chunk to its subscribers before feeding its own
// emulator, so a client can be a chunk ahead of the daemon rather than behind
// it. That is a moment, not a state: waiting it out is what makes the
// comparison about rehydration instead of about scheduling.
func (r *rig) converge(ptyID string) {
	r.t.Helper()
	deadline := time.Now().Add(rigWait)
	for time.Now().Before(deadline) {
		st, err := r.ctl.GetTerminalState(ptyID, rigScrollbackOracle)
		if err == nil && st != nil {
			if clientText(r.winByPTY(ptyID)) == stateText(st) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// settle waits for the client to stop changing, so a comparison is taken after
// every byte in flight has been applied on both sides. The client emulator is
// fed by a goroutine, so there is no synchronous point to wait on.
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

// --- reading the two sides ---------------------------------------------------

// cellSig describes a cell to the fidelity the wire can carry, which is the
// fidelity a comparison across it can ask about.
func cellSig(content string, width int, fg, bg string, bold, faint, italic, reverse, underline bool) string {
	if content == "" || content == " " {
		// A blank is a blank however it was produced: an unwritten cell and a
		// cell holding a space differ in the emulator and not on screen.
		if !reverse && bg == "" {
			return " "
		}
		content = " "
	}
	return fmt.Sprintf("%s|%d|%s|%s|%t%t%t%t%t", content, width, fg, bg, bold, faint, italic, reverse, underline)
}

func hexOf(c any) string {
	col, ok := c.(interface {
		RGBA() (uint32, uint32, uint32, uint32)
	})
	if !ok || c == nil {
		return ""
	}
	r, g, b, _ := col.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

func uvCellSig(cell *uv.Cell) string {
	if cell == nil {
		return " "
	}
	fg, bg := "", ""
	if cell.Style.Fg != nil {
		fg = hexOf(cell.Style.Fg)
	}
	if cell.Style.Bg != nil {
		bg = hexOf(cell.Style.Bg)
	}
	return cellSig(cell.Content, cell.Width, fg, bg,
		cell.Style.Attrs&uv.AttrBold != 0,
		cell.Style.Attrs&uv.AttrFaint != 0,
		cell.Style.Attrs&uv.AttrItalic != 0,
		cell.Style.Attrs&uv.AttrReverse != 0,
		cell.Style.Underline != ansi.UnderlineNone)
}

func stateCellSig(cs session.CellState) string {
	return cellSig(cs.Content, cs.Width, cs.FgColor, cs.BgColor,
		cs.Bold, cs.Faint, cs.Italic, cs.Reverse, cs.Underline)
}

// stateRow renders one wire row as plain text.
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

// stateText renders the daemon's scrollback and screen as plain text.
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

// clientText renders a client window's scrollback and screen as plain text.
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
