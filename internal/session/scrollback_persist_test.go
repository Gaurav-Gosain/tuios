package session

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
	uv "github.com/charmbracelet/ultraviolet"
)

// These run on whichever emulator the build links: the pure one by default and
// libghostty under -tags ghostty, which CI runs over this package. So each
// round trip is proven on both backends, save and restore alike.

// rowText is one row of cells as text, trailing blanks trimmed.
func rowText(line uv.Line) string {
	var b strings.Builder
	for _, c := range line {
		if c.Width == 0 && c.Content == "" {
			continue
		}
		if c.Content == "" {
			b.WriteByte(' ')
			continue
		}
		b.WriteString(c.Content)
	}
	return strings.TrimRight(b.String(), " ")
}

func screenRow(t vt.Terminal, y int) uv.Line {
	line := make(uv.Line, t.Width())
	for x := range t.Width() {
		if c := t.CellAt(x, y); c != nil {
			line[x] = *c
		}
	}
	return line
}

// allRows is every history row and then every screen row, as text.
func allRows(t vt.Terminal) []string {
	var out []string
	for i := range t.ScrollbackLen() {
		out = append(out, rowText(t.ScrollbackLine(i)))
	}
	for y := range t.Height() {
		out = append(out, rowText(screenRow(t, y)))
	}
	return out
}

// throughDisk saves the emulator's history as a pane's file would hold it and
// reads it back.
func throughDisk(t *testing.T, term vt.Terminal, lines int) *savedHistory {
	t.Helper()
	at := time.Date(2026, 9, 30, 14, 2, 0, 0, time.Local)
	data, err := encodeHistory(&savedHistory{Version: historyVersion, SavedAt: at, State: historyStateOf(term, lines)})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	h, err := decodeHistory(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return h
}

func indexOf(rows []string, want string) int {
	for i, r := range rows {
		if r == want {
			return i
		}
	}
	return -1
}

func TestHistoryRoundTripKeepsTextStylesWideRunesAndWraps(t *testing.T) {
	src := vt.NewWithScrollback(80, 8, 1000)
	var in strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&in, "line-%02d\r\n", i)
	}
	in.WriteString("\x1b[1;31mBOLDRED\x1b[0m \x1b[48;2;10;20;30mTRUEBG\x1b[0m\r\n")
	in.WriteString("wide 日本語 end\r\n")
	// 100 letters in an 80-column pane: the emulator wraps it, a soft wrap.
	in.WriteString(strings.Repeat("w", 100) + "\r\n")
	in.WriteString("$ ")
	if _, err := src.Write([]byte(in.String())); err != nil {
		t.Fatal(err)
	}

	h := throughDisk(t, src, 1000)
	dst := newRestoredEmulator(80, 8, 1000, "/tmp/x", h)
	rows := allRows(dst)

	// Every line, in order, then the prompt it was sitting at, then the divider.
	prev := -1
	for i := 1; i <= 30; i++ {
		at := indexOf(rows, fmt.Sprintf("line-%02d", i))
		if at <= prev {
			t.Fatalf("line-%02d at row %d, after row %d; rows:\n%s", i, at, prev, strings.Join(rows, "\n"))
		}
		prev = at
	}
	divider := -1
	for i, r := range rows {
		if strings.HasPrefix(r, "-- tuios: restored from ") {
			divider = i
		}
	}
	if divider < 0 {
		t.Fatalf("no divider; rows:\n%s", strings.Join(rows, "\n"))
	}
	if rows[divider-1] != "$" {
		t.Errorf("the row above the divider is %q, want the old prompt", rows[divider-1])
	}
	if want := "-- tuios: restored from Sep 30 14:02, fresh shell in /tmp/x --"; rows[divider] != want {
		t.Errorf("divider %q, want %q", rows[divider], want)
	}
	// The cursor waits under the divider, where the new shell prints.
	if y := dst.CursorPosition().Y + dst.ScrollbackLen(); y != divider+1 {
		t.Errorf("cursor on row %d, want %d (under the divider)", y, divider+1)
	}

	// Styles, from the history or the screen, wherever the row landed.
	cellAt := func(row, x int) *uv.Cell {
		if row < dst.ScrollbackLen() {
			line := dst.ScrollbackLine(row)
			if x < len(line) {
				return &line[x]
			}
			return nil
		}
		return dst.CellAt(x, row-dst.ScrollbackLen())
	}
	styled := indexOf(rows, "BOLDRED TRUEBG")
	if styled < 0 {
		t.Fatalf("styled row missing; rows:\n%s", strings.Join(rows, "\n"))
	}
	if c := cellAt(styled, 0); c == nil || c.Style.Attrs&uv.AttrBold == 0 || colorToWire(c.Style.Fg) != "a1" {
		t.Errorf("B lost bold red: %+v", c)
	}
	if c := cellAt(styled, 8); c == nil || colorToWire(c.Style.Bg) == "" {
		t.Errorf("T lost its truecolor background: %+v", c)
	}
	if c := cellAt(styled, 7); c == nil || c.Style.Bg != nil {
		t.Errorf("the space between took a background: %+v", c)
	}

	wide := indexOf(rows, "wide 日本語 end")
	if wide < 0 {
		t.Fatalf("wide row missing; rows:\n%s", strings.Join(rows, "\n"))
	}
	if c := cellAt(wide, 5); c == nil || c.Content != "日" || c.Width != 2 {
		t.Errorf("wide rune cell %+v, want 日 of width 2", c)
	}

	// The wrapped line still says it carries on, so a later resize reflows it.
	wrapped := indexOf(rows, strings.Repeat("w", 80))
	if wrapped < 0 || rows[wrapped+1] != strings.Repeat("w", 20) {
		t.Fatalf("wrapped line missing; rows:\n%s", strings.Join(rows, "\n"))
	}
	var soft bool
	if wrapped < dst.ScrollbackLen() {
		soft, _ = dst.ScrollbackSoftWrapped(wrapped)
	} else {
		soft, _ = dst.RowSoftWrapped(wrapped - dst.ScrollbackLen())
	}
	if !soft {
		t.Error("the wrapped row lost its soft-wrap flag")
	}
}

// A pane restored at another width keeps its history. libghostty reflows it at
// the resize, joining the soft-wrapped halves again; the pure emulator pads
// the rows and keeps the flag, which is what copy and hints join lines by.
func TestHistoryRestoreAtAnotherWidth(t *testing.T) {
	src := vt.NewWithScrollback(20, 6, 1000)
	_, _ = src.Write([]byte("first\r\n" + strings.Repeat("r", 30) + "\r\nlast\r\n$ "))
	h := throughDisk(t, src, 1000)

	dst := newRestoredEmulator(60, 6, 1000, "", h)
	if dst.Width() != 60 || dst.Height() != 6 {
		t.Fatalf("restored emulator is %dx%d, want 60x6", dst.Width(), dst.Height())
	}
	rows := allRows(dst)
	if indexOf(rows, "first") < 0 || indexOf(rows, "last") < 0 {
		t.Errorf("lines lost; rows:\n%s", strings.Join(rows, "\n"))
	}
	if indexOf(rows, strings.Repeat("r", 30)) >= 0 {
		return // reflowed
	}
	half := indexOf(rows, strings.Repeat("r", 20))
	if half < 0 || rows[half+1] != strings.Repeat("r", 10) {
		t.Fatalf("the wrapped line is neither reflowed nor whole; rows:\n%s", strings.Join(rows, "\n"))
	}
	var soft bool
	if half < dst.ScrollbackLen() {
		soft, _ = dst.ScrollbackSoftWrapped(half)
	} else {
		soft, _ = dst.RowSoftWrapped(half - dst.ScrollbackLen())
	}
	if !soft {
		t.Error("the wrapped line lost its soft-wrap flag at the new width")
	}
}

// A full-screen program up at the save is not what comes back: the shell's
// screen under it and the history are.
func TestHistorySavesTheMainScreenUnderAnAlternateOne(t *testing.T) {
	src := vt.NewWithScrollback(40, 6, 1000)
	_, _ = src.Write([]byte("SHELL-BEFORE\r\n$ vim\r\n\x1b[?1049h\x1b[HALT-SCREEN-TEXT"))
	if !src.IsAltScreen() {
		t.Fatal("the source never entered the alternate screen")
	}
	h := throughDisk(t, src, 1000)
	dst := newRestoredEmulator(40, 6, 1000, "", h)
	if dst.IsAltScreen() {
		t.Error("the restored pane is on the alternate screen")
	}
	rows := allRows(dst)
	joined := strings.Join(rows, "\n")
	if strings.Contains(joined, "ALT-SCREEN-TEXT") {
		t.Errorf("the alternate screen was saved:\n%s", joined)
	}
	if indexOf(rows, "SHELL-BEFORE") < 0 || indexOf(rows, "$ vim") < 0 {
		t.Errorf("the main screen was not saved:\n%s", joined)
	}
}

// The lines bound is the number of history rows kept, newest last.
func TestHistoryLinesBound(t *testing.T) {
	src := vt.NewWithScrollback(40, 5, 1000)
	var in strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&in, "n%03d\r\n", i)
	}
	_, _ = src.Write([]byte(in.String()))
	h := throughDisk(t, src, 50)
	if got := packedRowCount(h.State.PackedScrollback); got != 50 {
		t.Fatalf("saved %d history rows, want 50", got)
	}
	rows := allRows(newRestoredEmulator(40, 5, 1000, "", h))
	// 50 history rows and the 5-row screen: n247 to n300.
	if indexOf(rows, "n246") >= 0 || indexOf(rows, "n247") < 0 || indexOf(rows, "n300") < 0 {
		t.Errorf("wrong rows kept:\n%s", strings.Join(rows, "\n"))
	}
}

// historyTestSession is a session with fake panes: an emulator each and no
// process, which is all saveHistory reads.
func historyTestSession(t *testing.T, name string, pol HistoryPolicy, panes map[string]string) (*Session, *SessionState) {
	t.Helper()
	s := &Session{ptys: map[string]*PTY{}, config: &SessionConfig{history: &pol}}
	s.setName(name)
	state := &SessionState{Name: name}
	for win, text := range panes {
		p := &PTY{ID: "pty-" + win, terminal: vt.NewWithScrollback(40, 6, 5000)}
		writePane(p, text)
		s.ptys[p.ID] = p
		state.Windows = append(state.Windows, WindowState{ID: win, PTYID: p.ID})
	}
	return s, state
}

func writePane(p *PTY, text string) {
	p.terminalMu.Lock()
	defer p.terminalMu.Unlock()
	_, _ = p.terminal.Write([]byte(text))
	p.vtSeq += int64(len(text))
}

func manyLines(prefix string, n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "%s-%05d %s\r\n", prefix, i, strings.Repeat("x", i%31))
	}
	return b.String()
}

func TestHistoryFilesArePrivateAndPrunedWithThePane(t *testing.T) {
	defer useResurrectionDir(t.TempDir())()
	pol := ResolveHistoryPolicy(nil, 0, 0)
	s, state := historyTestSession(t, "priv", pol, map[string]string{"win-a": "secret-a\r\n", "win-b": "secret-b\r\n"})

	s.saveHistory(state, false)
	for _, win := range []string{"win-a", "win-b"} {
		fi, err := os.Stat(historyPath("priv", win))
		if err != nil {
			t.Fatalf("%s not saved: %v", win, err)
		}
		if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v, want 0600", win, fi.Mode().Perm())
		}
	}
	if fi, err := os.Stat(historyDir("priv")); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700) {
		t.Errorf("history dir: %v, %v, want mode 0700", fi, err)
	}

	// Pane b closes: its file goes on the next save.
	state.Windows = state.Windows[:0]
	state.Windows = append(state.Windows, WindowState{ID: "win-a", PTYID: "pty-win-a"})
	s.saveHistory(state, true)
	if _, err := os.Stat(historyPath("priv", "win-b")); !os.IsNotExist(err) {
		t.Errorf("a closed pane's history is still on disk: %v", err)
	}
	if _, err := os.Stat(historyPath("priv", "win-a")); err != nil {
		t.Errorf("an open pane's history went: %v", err)
	}

	// A rename takes the history along; a kill removes it.
	moveHistory("priv", "priv2")
	if _, err := os.Stat(historyPath("priv2", "win-a")); err != nil {
		t.Errorf("the history did not follow the rename: %v", err)
	}
	RemoveResurrectionState("priv2")
	if _, err := os.Stat(historyDir("priv2")); !os.IsNotExist(err) {
		t.Errorf("removing the session left its history: %v", err)
	}
}

func TestHistorySaveIsThrottledAndSkipsIdlePanes(t *testing.T) {
	defer useResurrectionDir(t.TempDir())()
	s, state := historyTestSession(t, "busy", ResolveHistoryPolicy(nil, 0, 0), map[string]string{"win": "one\r\n"})
	path := historyPath("busy", "win")

	s.saveHistory(state, false)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("first save: %v", err)
	}

	// New output inside the interval: not written yet.
	writePane(s.ptys["pty-win"], "two\r\n")
	s.saveHistory(state, false)
	if now, _ := os.ReadFile(path); !bytes.Equal(now, first) {
		t.Error("a pane was rewritten inside the save interval")
	}
	// The final save writes it whatever the interval.
	s.saveHistory(state, true)
	second, _ := os.ReadFile(path)
	if bytes.Equal(second, first) {
		t.Fatal("the forced save did not write the new output")
	}
	// Nothing new: not even a forced save rewrites it.
	_ = os.Remove(path)
	s.saveHistory(state, true)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("an idle pane was rewritten")
	}
}

func TestHistoryPaneAndSessionByteBounds(t *testing.T) {
	defer useResurrectionDir(t.TempDir())()
	// Output that does not compress to nothing, so the bound bites.
	big := manyLines("row", 4000)

	pol := ResolveHistoryPolicy(nil, 5000, 16) // 16 KiB a pane
	s, state := historyTestSession(t, "bound", pol, map[string]string{"win": big})
	s.saveHistory(state, true)
	fi, err := os.Stat(historyPath("bound", "win"))
	if err != nil {
		t.Fatalf("not saved: %v", err)
	}
	if fi.Size() > 16<<10 {
		t.Errorf("pane file is %d bytes, over its 16 KiB bound", fi.Size())
	}
	h := loadHistory("bound")["win"]
	if h == nil {
		t.Fatal("the bounded file does not load")
	}
	rows := allRows(newRestoredEmulator(40, 6, 5000, "", h))
	// The newest rows are the ones kept.
	if indexOf(rows, strings.TrimRight(fmt.Sprintf("row-%05d %s", 3999, strings.Repeat("x", 3999%31)), " ")) < 0 {
		t.Errorf("the newest row was not kept")
	}

	// The session bound: a second pane gets what is left, and no more.
	prev := historySessionBytes
	historySessionBytes = fi.Size() + 4<<10
	defer func() { historySessionBytes = prev }()
	s2, state2 := historyTestSession(t, "bound", pol, map[string]string{"win": big, "win2": big})
	s2.saveHistory(state2, true)
	total := otherHistoryBytes("bound", "")
	if total > historySessionBytes {
		t.Errorf("session history is %d bytes, over its %d bound", total, historySessionBytes)
	}
}

func TestHistoryOffSavesNothing(t *testing.T) {
	dir := t.TempDir()
	defer useResurrectionDir(dir)()
	s, state := historyTestSession(t, "off", HistoryPolicy{}, map[string]string{"win": "x\r\n"})
	s.saveHistory(state, true)
	if _, err := os.Stat(filepath.Join(dir, historyDirName)); !os.IsNotExist(err) {
		t.Errorf("history saved with the setting off: %v", err)
	}
}

func TestHistoryCorruptFileIsDiscarded(t *testing.T) {
	defer useResurrectionDir(t.TempDir())()
	path := historyPath("bad", "win")
	if err := writePrivateFile(path, []byte("not gzip")); err != nil {
		t.Fatal(err)
	}
	if got := loadHistory("bad"); len(got) != 0 {
		t.Errorf("a corrupt file loaded: %v", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the corrupt file was kept")
	}
}

// Through the daemon's restore: the respawned pane's emulator, which is what
// every client rehydrates from, holds the old lines above the divider.
func TestDaemonRestoreShowsSavedHistory(t *testing.T) {
	defer useResurrectionDir(t.TempDir())()
	src := vt.NewWithScrollback(78, 22, 1000)
	_, _ = src.Write([]byte(manyLines("OLD", 60) + "$ "))
	data, err := encodeHistory(&savedHistory{Version: historyVersion, SavedAt: time.Now(), State: historyStateOf(src, 1000)})
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(historyPath("hist", "win-1"), data); err != nil {
		t.Fatal(err)
	}

	d := NewDaemon(&DaemonConfig{History: ResolveHistoryPolicy(nil, 0, 0)})
	defer d.manager.Shutdown()
	sess, err := d.restoreSession(&SessionState{Name: "hist", Width: 80, Height: 24,
		Windows: []WindowState{{ID: "win-1", Width: 80, Height: 24}}})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	st := sess.GetState()
	pty := sess.GetPTY(st.Windows[0].PTYID)
	if pty == nil {
		t.Fatal("no pane")
	}
	pty.terminalMu.RLock()
	rows := allRows(pty.terminal)
	pty.terminalMu.RUnlock()
	joined := strings.Join(rows, "\n")
	first, divider := indexOf(rows, "OLD-00000"), -1
	for i, r := range rows {
		if strings.HasPrefix(r, "-- tuios: restored from ") {
			divider = i
		}
	}
	if first < 0 || divider < 0 || first > divider {
		t.Fatalf("want the old lines above the divider:\n%s", joined)
	}
}

// Turning the setting off deletes what was saved while it was on, when the
// daemon next starts. A daemon that read no config deletes nothing.
func TestHistoryTurnedOffIsDeletedAtStart(t *testing.T) {
	defer useResurrectionDir(t.TempDir())()
	path := historyPath("gone", "win")
	if err := writePrivateFile(path, []byte("x")); err != nil {
		t.Fatal(err)
	}

	d := NewDaemon(&DaemonConfig{})
	defer d.manager.Shutdown()
	d.dropHistoryWhenOff()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a daemon with no config deleted saved history: %v", err)
	}

	off := false
	pol := ResolveHistoryPolicy(&off, 0, 0)
	if pol.Enabled || !pol.TurnedOff {
		t.Fatalf("persist_scrollback = false resolved to %+v", pol)
	}
	d2 := NewDaemon(&DaemonConfig{History: pol})
	defer d2.manager.Shutdown()
	d2.dropHistoryWhenOff()
	if _, err := os.Stat(historyRoot()); !os.IsNotExist(err) {
		t.Errorf("history saved before the setting went off is still on disk: %v", err)
	}
}

// The default is the number of rows a client asks for when it attaches, so a
// restore saves no row a client is never sent, and a save holds a pane for a
// fifth of what 5000 rows cost on libghostty.
func TestHistoryDefaultLinesMatchWhatAClientIsSent(t *testing.T) {
	if got := ResolveHistoryPolicy(nil, 0, 0).Lines; got != DefaultStateScrollback {
		t.Errorf("default history lines %d, want %d, what an attach sends", got, DefaultStateScrollback)
	}
}

// Packing reads nothing from the emulator, so only the read of the rows has
// to hold the pane's lock: output written after the read does not reach the
// state packed from it.
func TestHistoryPackingReadsOnlyTheCapturedRows(t *testing.T) {
	p := &PTY{ID: "p", terminal: vt.NewWithScrollback(40, 5, 100)}
	writePane(p, "BEFORE\r\n")
	rows, _ := p.captureHistory(100)
	writePane(p, "AFTER\r\n")
	st := rows.state(len(rows.history))
	if err := st.Unpack(); err != nil {
		t.Fatal(err)
	}
	var text []string
	for _, r := range append(st.Scrollback, st.Screen...) {
		var b strings.Builder
		for _, c := range r {
			b.WriteString(c.Content)
		}
		text = append(text, strings.TrimSpace(b.String()))
	}
	joined := strings.Join(text, "\n")
	if !strings.Contains(joined, "BEFORE") || strings.Contains(joined, "AFTER") {
		t.Errorf("packed state is not the captured one:\n%s", joined)
	}
}

func savedFile(t *testing.T, lines int) []byte {
	t.Helper()
	src := vt.NewWithScrollback(40, 5, 1000)
	_, _ = src.Write([]byte(manyLines("t", lines)))
	data, err := encodeHistory(&savedHistory{Version: historyVersion, SavedAt: time.Now(), State: historyStateOf(src, 1000)})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// gzip checks its checksum only at the end of the stream, which gob never
// reads to.
func TestHistoryTruncatedFileIsCorrupt(t *testing.T) {
	data := savedFile(t, 50)
	if _, err := decodeHistory(bytes.NewReader(data)); err != nil {
		t.Fatalf("the whole file does not decode: %v", err)
	}
	if _, err := decodeHistory(bytes.NewReader(data[:len(data)-4])); err == nil {
		t.Error("a file cut 4 bytes short decoded as valid")
	}
	bad := bytes.Clone(data)
	bad[len(bad)-6] ^= 0xff // the checksum
	if _, err := decodeHistory(bytes.NewReader(bad)); err == nil {
		t.Error("a file with a wrong checksum decoded as valid")
	}
}

// A restore builds an emulator of the saved size at daemon start.
func TestHistoryScreenSizeIsBounded(t *testing.T) {
	for _, size := range [][2]int{{65536, 65536}, {maxHistoryDim + 1, 10}, {2000, 2000}} {
		st := &TerminalState{Width: size[0], Height: size[1], Styles: []StyleState{{}}}
		data, err := encodeHistory(&savedHistory{Version: historyVersion, State: st})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeHistory(bytes.NewReader(data)); err == nil {
			t.Errorf("a %dx%d screen decoded", size[0], size[1])
		}
	}
}

func TestHistoryWriteIgnoresWhatIsAlreadyThere(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes and symlinks")
	}
	dir := t.TempDir()
	defer useResurrectionDir(dir)()
	path := historyPath("s", "win")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// A leftover temporary file with a wide mode, and a planted symlink at
	// the name a fixed temporary file would take.
	if err := os.WriteFile(path+".tmp", []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path+".tmp"); err != nil {
		t.Fatal(err)
	}

	if err := writePrivateFile(path, []byte("data")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "keep" {
		t.Errorf("the write went through a planted symlink: target now %q", got)
	}
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
		t.Errorf("saved file %v %v, want a regular 0600 file", fi.Mode(), err)
	}
	if di, _ := os.Stat(filepath.Dir(path)); di.Mode().Perm() != 0o700 {
		t.Errorf("session history dir is %v, want 0700", di.Mode().Perm())
	}

	// And a leftover 0644 file at the old fixed name does not lend its mode.
	_ = os.Remove(path + ".tmp")
	if err := os.WriteFile(path+".tmp", []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(path, []byte("data2")); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("saved file is %v after a leftover 0644 temp file, want 0600", fi.Mode().Perm())
	}
}

// History with no state file can never be restored, and it holds what the
// panes printed.
func TestHistoryWithoutStateIsCleanedAtStart(t *testing.T) {
	defer useResurrectionDir(t.TempDir())()
	if err := SaveSessionForResurrection(&SessionState{Name: "kept", Windows: []WindowState{{ID: "w"}}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kept", "orphan"} {
		if err := writePrivateFile(historyPath(name, "w"), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	CleanResurrectionDir()
	if _, err := os.Stat(historyPath("kept", "w")); err != nil {
		t.Errorf("history of a saved session went: %v", err)
	}
	if _, err := os.Stat(historyDir("orphan")); !os.IsNotExist(err) {
		t.Errorf("history of a session with no state is still there: %v", err)
	}
}

// A killed session's history is deleted with its state, so the stop does not
// write it first.
func TestKillSessionDoesNotSaveHistory(t *testing.T) {
	defer useResurrectionDir(t.TempDir())()
	prev := scrollbackSaveInterval
	scrollbackSaveInterval = time.Hour
	defer func() { scrollbackSaveInterval = prev }()

	d := NewDaemon(&DaemonConfig{History: ResolveHistoryPolicy(nil, 0, 0)})
	defer d.manager.Shutdown()
	sess, err := d.manager.CreateSession("doomed", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	pty, err := sess.CreatePTY("win-1", 40, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.UpdateState(&SessionState{Name: "doomed", Windows: []WindowState{{ID: "win-1", PTYID: pty.ID}}})
	// Saved once, so the periodic saver waits out the hour, and then given
	// output the forced save on a stop would write.
	sess.persistMu.Lock()
	sess.saveHistory(sess.ResurrectionState(), true)
	sess.persistMu.Unlock()
	writePane(pty, "secret\r\n")

	var writes atomic.Int32
	historyWriteHook = func(name, _ string) {
		if name == "doomed" {
			writes.Add(1)
		}
	}
	defer func() { historyWriteHook = nil }()
	if err := d.manager.DeleteSession("doomed"); err != nil {
		t.Fatal(err)
	}
	if n := writes.Load(); n != 0 {
		t.Errorf("killing the session wrote its history %d times before deleting it", n)
	}
}

// The first save after a restore keeps the time of the save it was restored
// from; a save after that records its own.
func TestHistoryFirstSaveAfterRestoreKeepsItsTime(t *testing.T) {
	defer useResurrectionDir(t.TempDir())()
	orig := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	src := vt.NewWithScrollback(78, 22, 1000)
	_, _ = src.Write([]byte("OLD\r\n$ "))
	data, err := encodeHistory(&savedHistory{Version: historyVersion, SavedAt: orig, State: historyStateOf(src, 1000)})
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(historyPath("again", "win-1"), data); err != nil {
		t.Fatal(err)
	}
	d := NewDaemon(&DaemonConfig{History: ResolveHistoryPolicy(nil, 0, 0)})
	defer d.manager.Shutdown()
	sess, err := d.restoreSession(&SessionState{Name: "again", Width: 80, Height: 24,
		Windows: []WindowState{{ID: "win-1", Width: 80, Height: 24}}})
	if err != nil {
		t.Fatal(err)
	}
	save := func() time.Time {
		sess.persistMu.Lock()
		sess.saveHistory(sess.ResurrectionState(), true)
		sess.persistMu.Unlock()
		h := loadHistory("again")["win-1"]
		if h == nil {
			t.Fatal("not saved")
		}
		return h.SavedAt
	}
	if got := save(); !got.Equal(orig) {
		t.Errorf("first save after the restore records %v, want the restored %v", got, orig)
	}
	writePane(sess.GetPTY(sess.GetState().Windows[0].PTYID), "new\r\n")
	if got := save(); got.Equal(orig) {
		t.Error("a later save still records the restored time")
	}
}

// A file of another layout is another build's: skipped, and left in place.
func TestHistoryOtherVersionIsSkippedNotDeleted(t *testing.T) {
	defer useResurrectionDir(t.TempDir())()
	src := vt.NewWithScrollback(40, 5, 100)
	data, err := encodeHistory(&savedHistory{Version: historyVersion + 1, SavedAt: time.Now(), State: historyStateOf(src, 100)})
	if err != nil {
		t.Fatal(err)
	}
	path := historyPath("newer", "win")
	if err := writePrivateFile(path, data); err != nil {
		t.Fatal(err)
	}
	if got := loadHistory("newer"); len(got) != 0 {
		t.Errorf("a file of another version loaded")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("a file of another version was deleted: %v", err)
	}
}
