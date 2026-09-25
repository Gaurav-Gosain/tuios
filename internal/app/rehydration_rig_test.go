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
	// rebuiltWindows records whether the route under test closes and rebuilds
	// the window set, which decides what client-local view state can survive.
	rebuiltWindows bool
	// cols and rows are the size every client of this rig attaches at, so a
	// test about two differently sized clients can pick its own.
	cols, rows int
	// keepExits leaves WindowExitChan undrained, which is what an Update
	// goroutine that is busy elsewhere looks like from the read loop.
	keepExits bool
	// producing is the file a guest started by the resized-while-producing
	// shape keeps writing for as long as it exists.
	producing string
}

// keepExits is the newRig option that leaves window exits queued.
func keepExits(r *rig) { r.keepExits = true }

// ownSocket gives this test its own daemon socket. The whole binary already
// runs against a throwaway XDG tree (see TestMain), but every test in it shares
// that tree, and two daemons cannot share one socket. GetSocketPath reads the
// variable on each call, so moving it here is enough and no xdg reload is
// involved.
func ownSocket(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
}

// newRig brings up a daemon, creates a session with panes windows, and attaches
// a client OS to it by the attach path. The returned rig is at route "first
// attach" with every pane subscribed.
func newRig(t *testing.T, panes int, opts ...func(*rig)) *rig {
	t.Helper()
	return newRigSized(t, panes, rigCols, rigRows, opts...)
}

// newRigSized is newRig with the client size chosen, for the tests about what
// two differently sized clients do to each other.
func newRigSized(t *testing.T, panes, cols, rows int, opts ...func(*rig)) *rig {
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
	if err := boot.Connect("test", cols, rows); err != nil {
		t.Fatalf("bootstrap connect: %v", err)
	}
	// A detached create comes back with one real window and one real PTY;
	// attaching to a name that does not exist yet comes back empty.
	if err := boot.CreateDetachedSession(name, cols, rows); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := boot.AttachSession(name, false, cols, rows); err != nil {
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
	// The control connection attaches while the bootstrap one is still here, and
	// starts reading straight after. Every client joining or leaving this
	// session is announced to it, and an announcement is only told apart from a
	// reply once the read loop is running: attaching after the bootstrap client
	// had already left let its client-left notification be read as the attach's
	// own answer.
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
	r.t.Helper()
	r.m, r.client = attachClientOS(r.t, r.session, r.cols, r.rows, r.keepExits)
}

// attachClientOS is the client-side attach sequence on its own, so a test that
// needs two clients on one session gets the second one by the same route as the
// first rather than by a second implementation of it.
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
	// Announcing this client's chrome is deliberately left to the caller. A real
	// client registers its handlers first and announces on the first window-size
	// message, and the order matters: the daemon answers an announcement with a
	// broadcast to everyone, so announcing before the handler exists throws away
	// the answer and leaves this OS holding no session reserve at all.
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
	if !keepExits {
		drainExits(t, m)
	}
	return m, c
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

// daemonCells reads the daemon's copy of a pane with its cells unpacked. The
// client keeps a snapshot in the packed form ApplyTerminalState reads, so an
// oracle that compares cells or text unpacks it first.
func (r *rig) daemonCells(ptyID string, maxScrollback int) (*session.TerminalState, error) {
	st, err := r.ctl.GetTerminalState(ptyID, maxScrollback, 0)
	if err != nil || st == nil {
		return st, err
	}
	return st, st.Unpack()
}

// ptySize reports the size the daemon has the pane at.
func (r *rig) ptySize(ptyID string) (int, int) {
	r.t.Helper()
	st, err := r.ctl.GetTerminalState(ptyID, -1, 0)
	if err != nil || st == nil {
		r.t.Fatalf("read pane size: %v", err)
	}
	return st.Width, st.Height
}

// startPTY types a command at a pane and returns without waiting for it, so the
// pane is still producing while the caller goes on.
func (r *rig) startPTY(ptyID, command string) {
	r.t.Helper()
	r.typeAtPrompt(ptyID, command)
}

// feedPTY is feed addressed by PTY, for the shapes that run while the client
// holds no window for the pane at all.
func (r *rig) feedPTY(ptyID, command, want string) {
	r.t.Helper()
	r.typeAtPrompt(ptyID, command)
	r.waitDaemonShows(ptyID, want)
}

// typeAtPrompt waits for the shell to be sitting at an empty prompt and then
// types command at it.
//
// Typing before that is typing ahead of the shell, and a shell with line
// editing does not take typed-ahead input cleanly. macOS's /bin/sh is bash:
// input that arrives while the terminal is still in cooked mode is echoed by the
// line discipline, echoed again when readline takes it, and the command's output
// then starts on the row the second echo left the cursor on instead of a row of
// its own. At the rig's pane width that wraps the sentinel mid-word, so the wait
// for it spends its whole deadline and the case fails as a timeout. Under load
// the window before the first prompt is long enough to hit on every shape.
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

// childProcesses lists the processes this test binary started, which is where
// the in-process daemon's shells are, with their state and what they are
// waiting in. A shell that never printed its prompt is one of them.
func childProcesses() string {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,pgid=,stat=,wchan=,etime=,command=").Output()
	if err != nil {
		return "(ps: " + err.Error() + ")"
	}
	self := strconv.Itoa(os.Getpid())
	var b strings.Builder
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[1] == self {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// atPrompt reports whether the daemon's copy of the pane has the cursor just
// past the rig's "$ " prompt with nothing typed after it.
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
		// bash redraws its prompt when the pane is resized under it, and a
		// redraw caught by an attach's resize can leave the row reading "$ $"
		// with the cursor at its start. The shell is at its prompt all the same.
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

// waitDaemonShows blocks until the daemon's emulator shows want on screen or in
// its scrollback.
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

// daemonShows asks once instead of waiting, for the cases that need to know
// whether the guest has got somewhere yet rather than to block until it does.
func (r *rig) daemonShows(ptyID, want string) bool {
	r.t.Helper()
	st, err := r.daemonCells(ptyID, rigScrollbackOracle)
	if err != nil || st == nil {
		return false
	}
	return strings.Contains(stateText(st), want)
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
		st, err := r.daemonCells(ptyID, rigScrollbackOracle)
		// The client may hold less history than the daemon, which is what
		// compareSides allows too, so agreement is the client's text being the
		// daemon's tail. Waiting for the two to be equal spent the whole
		// deadline on every route that rebuilds a pane with more history than
		// the snapshot carries.
		if err == nil && st != nil {
			if strings.HasSuffix("\n"+stateText(st), "\n"+clientText(r.winByPTY(ptyID))) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// latestRPLine is the highest line number the resized-while-producing guest
// has on the daemon's copy of the pane, or zero before its first line.
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

// rpLine matches the start of a line the resized-while-producing guest wrote.
var rpLine = regexp.MustCompile(`RP-([0-9]+)-A`)

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

// stateCellSig describes a cell whole: what it holds, and every part of how it
// is painted. Comparing only the characters is what let a route that restored a
// pane's text while losing its colours pass every case in the matrix and be
// obviously wrong on screen.
//
// Both sides are read as a CellState so the comparison is over one description
// and cannot drift from what the wire carries.
func stateCellSig(cs session.CellState) string {
	if (cs.Content == "" || cs.Content == " ") && cs.FgColor == "" && cs.BgColor == "" &&
		cs.UlColor == "" && cs.Attrs == 0 && cs.Underline == 0 && cs.LinkURL == "" {
		// A blank is a blank however it was produced: an unwritten cell and a
		// cell holding an unstyled space differ in the emulator and not on
		// screen.
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
