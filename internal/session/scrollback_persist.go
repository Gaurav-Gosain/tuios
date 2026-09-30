package session

// Persisted scrollback: a pane's history survives the daemon.
//
// Resurrection brings a session's layout back with a fresh shell in each pane,
// and before this the pane came back empty. Now each pane's history is saved
// next to the session's state file, and a restore lays it back into the new
// pane's emulator above a dim divider, with the new shell's prompt under it.
//
// What is saved is the emulator's own picture, not the bytes the program
// wrote: a TerminalState in its packed form (snapshot_pack.go), the same
// structure a client rehydrates from. It carries every cell with its style,
// wide runes and the soft-wrap flags, it is written and read by both emulator
// backends through the code the wire already tests (terminalStateOf and
// ApplyTerminalState), and it is bounded by rows rather than by how chatty the
// program was. Raw bytes would have needed a replay through a parser, a guess
// at where a cut stream is safe to start, and would have carried every mode a
// full-screen program set.
//
// One file per pane, gzip over gob, in <state dir>/scrollback/<session>/,
// directory 0700 and files 0600, because history holds whatever was printed,
// secrets included. gzip is in the standard library; zstd would have added a
// dependency to a binary with a size budget, for a file written at most every
// scrollbackSaveInterval.
//
// When: a pane is saved by the session's periodic saver, only when its
// emulator consumed output since its last save, and at most once per
// scrollbackSaveInterval; and on a clean stop, whatever the interval. So an
// idle pane is never rewritten and a pane flooding output costs one capture
// and one compressed write per interval, not one per tick.

import (
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

const (
	// DefaultHistoryLines is how many history rows a pane saves when the
	// config names no number: daemon.persist_scrollback_lines.
	DefaultHistoryLines = 5000
	// DefaultHistoryKB is the most one pane's saved file may take on disk,
	// compressed, when the config names no number: daemon.persist_scrollback_kb.
	DefaultHistoryKB = 2048
	historyDirName   = "scrollback"
	historyExt       = ".hist.gz"
	// historyVersion is bumped when the file's layout changes in a way an
	// older reader cannot take. A file at another version is ignored.
	historyVersion = 1
)

// historySessionBytes is the most one session's saved history may take on disk
// in all. A pane that would take it past this saves fewer rows. A variable so
// a test can lower it.
var historySessionBytes int64 = 16 << 20

// scrollbackSaveInterval is the least time between two saves of one pane's
// history. A variable so a test can shorten it.
var scrollbackSaveInterval = 30 * time.Second

// HistoryPolicy is what the daemon does with pane history across a restart:
// the resolved daemon.persist_scrollback settings.
type HistoryPolicy struct {
	// Enabled saves each pane's history and restores it with the session.
	Enabled bool
	// Lines is the most history rows one pane saves, the screen not counted.
	Lines int
	// Bytes is the most one pane's file may take on disk, compressed.
	Bytes int64
	// TurnedOff is set when the config says false, as against saying nothing
	// or not being read at all. Only then does the daemon delete what was
	// saved before.
	TurnedOff bool
}

// ResolveHistoryPolicy turns the config's three settings into a policy. nil
// enabled means on; a zero or negative bound means its default.
func ResolveHistoryPolicy(enabled *bool, lines, kb int) HistoryPolicy {
	p := HistoryPolicy{Enabled: enabled == nil || *enabled, Lines: lines, Bytes: int64(kb) << 10}
	p.TurnedOff = !p.Enabled
	if p.Lines <= 0 {
		p.Lines = DefaultHistoryLines
	}
	if p.Bytes <= 0 {
		p.Bytes = DefaultHistoryKB << 10
	}
	return p
}

// savedHistory is one pane's history file.
type savedHistory struct {
	Version int
	SavedAt time.Time
	// State is the pane's main screen and history, packed. Modes, the pen and
	// everything else a running program set are left out: the history comes
	// back as something to read, under a new shell that sets its own.
	State *TerminalState
}

// restoreSpec marks a pane being made for a restored window. A nil one is a
// pane made for a new window.
type restoreSpec struct {
	// history is the pane's saved history, nil when it has none.
	history *savedHistory
}

// historyMark is what a session remembers about the last save of one pane.
type historyMark struct {
	ptyID string
	seq   int64
	at    time.Time
}

// historySaver is a session's record of its panes' saves. Guarded by mu; a
// session's saves run one at a time under persistMu anyway, and mu is for the
// test and the rename that read it from elsewhere.
type historySaver struct {
	mu    sync.Mutex
	marks map[string]historyMark // by window id
}

func historyRoot() string {
	return filepath.Join(getResurrectionDir(), historyDirName)
}

// historyDir is where one session's pane files live.
func historyDir(sessionName string) string {
	return filepath.Join(historyRoot(), sessionName)
}

func historyPath(sessionName, windowID string) string {
	return filepath.Join(historyDir(sessionName), windowID+historyExt)
}

// validHistoryWindowID keeps a window id from naming a path outside the
// session's directory. Window ids are UUIDs a client made up, so this is a
// check on input, not a formality.
func validHistoryWindowID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`) &&
		!strings.ContainsFunc(id, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// RemoveHistory deletes every saved pane history of a session.
func RemoveHistory(sessionName string) {
	if sessionName == "" {
		return
	}
	_ = os.RemoveAll(historyDir(sessionName))
}

// RemoveAllHistory deletes every saved pane history of every session. The
// daemon calls it on start when persist_scrollback is off, so turning the
// setting off also takes what was saved while it was on off the disk.
func RemoveAllHistory() {
	_ = os.RemoveAll(historyRoot())
}

// dropHistoryWhenOff deletes the history saved while daemon.persist_scrollback
// was on, once it is set to false, so turning it off also takes the saved text
// off the disk. A daemon that read no config saves nothing and deletes
// nothing. Called once at start, before the restore.
func (d *Daemon) dropHistoryWhenOff() {
	if d.manager.HistoryPolicy().TurnedOff {
		RemoveAllHistory()
	}
}

// moveHistory moves a session's saved history to its new name. Best effort:
// a failure loses the history, never the session.
func moveHistory(oldName, newName string) {
	from, to := historyDir(oldName), historyDir(newName)
	if _, err := os.Stat(from); err != nil {
		return
	}
	_ = os.RemoveAll(to)
	if err := os.Rename(from, to); err != nil {
		LogError("Moving saved history of session %q to %q failed: %v", oldName, newName, err)
	}
}

// saveHistory writes the history of each of the session's panes that needs
// it, and removes the files of panes the session no longer has. force skips
// the interval, for the last save before a stop. Called under persistMu.
func (s *Session) saveHistory(state *SessionState, force bool) {
	pol := s.historyPolicy()
	if !pol.Enabled || state == nil || state.Name != s.Name() {
		return
	}
	name := state.Name
	keep := make(map[string]bool, len(state.Windows))
	now := time.Now()
	for _, w := range state.Windows {
		if !validHistoryWindowID(w.ID) || !historyWanted(w) {
			continue
		}
		keep[w.ID] = true
		pty := s.GetPTY(w.PTYID)
		if pty == nil || pty.host != "" {
			continue
		}
		mark, seen := s.history.mark(w.ID)
		if seen && mark.ptyID == pty.ID {
			if pty.consumedSeq() == mark.seq {
				continue // nothing new since the last save
			}
			if !force && now.Sub(mark.at) < scrollbackSaveInterval {
				continue
			}
		}
		seq, err := s.saveOnePane(name, w.ID, pty, pol, now)
		if err != nil {
			LogError("Saving the history of pane %s in session %q failed: %v", shortID(w.ID), name, err)
			continue
		}
		s.history.setMark(w.ID, historyMark{ptyID: pty.ID, seq: seq, at: now})
	}
	pruneHistory(name, keep)
	s.history.prune(keep)
}

// historyWanted reports whether a window's history is worth saving: every pane
// a restore brings back. A popup other than the scratch shell is dropped by
// the restore, and a pane whose process ran on another machine comes back as
// a local shell, while the history belongs to the machine that ran it.
func historyWanted(w WindowState) bool {
	if w.Popup && (!w.Scratch || w.ScratchKey() != "scratch") {
		return false
	}
	return w.Host == ""
}

// saveOnePane captures one pane and writes its file, and returns the stream
// position the capture was taken at. The file is kept within the pane's bound
// and the session's: a capture that compresses too big is taken again with
// fewer rows, and one that still does not fit is not written.
func (s *Session) saveOnePane(sessionName, windowID string, pty *PTY, pol HistoryPolicy, now time.Time) (int64, error) {
	budget := min(pol.Bytes, historySessionBytes-otherHistoryBytes(sessionName, windowID))
	lines := pol.Lines
	var data []byte
	var seq int64
	for range 4 {
		var st *TerminalState
		st, seq = pty.historyState(lines)
		if st == nil {
			return seq, nil
		}
		var err error
		data, err = encodeHistory(&savedHistory{Version: historyVersion, SavedAt: now, State: st})
		if err != nil {
			return seq, err
		}
		if int64(len(data)) <= budget {
			break
		}
		if lines == 0 {
			data = nil
			break
		}
		// Scale the rows to the budget with a margin, since compression is not
		// linear in the rows kept.
		lines = int(float64(lines) * float64(budget) / float64(len(data)) * 0.8)
		data = nil
	}
	path := historyPath(sessionName, windowID)
	if data == nil {
		debugLog("[DEBUG] pane %s history does not fit in %d bytes, not saved", shortID(windowID), budget)
		_ = os.Remove(path)
		return seq, nil
	}
	return seq, writePrivateFile(path, data)
}

// writePrivateFile writes data to path through a temporary file and a rename,
// with the directory 0700 and the file 0600.
func writePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// otherHistoryBytes is what the session's files other than this pane's take on
// disk.
func otherHistoryBytes(sessionName, windowID string) int64 {
	entries, err := os.ReadDir(historyDir(sessionName))
	if err != nil {
		return 0
	}
	var n int64
	for _, e := range entries {
		if e.IsDir() || e.Name() == windowID+historyExt {
			continue
		}
		if info, err := e.Info(); err == nil {
			n += info.Size()
		}
	}
	return n
}

// pruneHistory removes the files of panes the session no longer has, and any
// temporary file a crash left behind.
func pruneHistory(sessionName string, keep map[string]bool) {
	dir := historyDir(sessionName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		id, ok := strings.CutSuffix(name, historyExt)
		if ok && keep[id] {
			continue
		}
		_ = os.RemoveAll(filepath.Join(dir, name))
	}
}

func (h *historySaver) mark(windowID string) (historyMark, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	m, ok := h.marks[windowID]
	return m, ok
}

func (h *historySaver) setMark(windowID string, m historyMark) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.marks == nil {
		h.marks = make(map[string]historyMark)
	}
	h.marks[windowID] = m
}

func (h *historySaver) prune(keep map[string]bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id := range h.marks {
		if !keep[id] {
			delete(h.marks, id)
		}
	}
}

// consumedSeq is the stream position the pane's emulator has consumed.
func (p *PTY) consumedSeq() int64 {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return p.vtSeq
}

// historyState captures the pane's history for saving, with at most lines
// history rows, and the stream position it was taken at. Only the read of the
// emulator happens under its lock; the encoding is the caller's.
func (p *PTY) historyState(lines int) (*TerminalState, int64) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	if p.terminal == nil {
		return nil, p.vtSeq
	}
	n := lines
	if n <= 0 {
		n = -1 // none; zero would mean the default
	}
	return historyStateOf(p.terminal, n), p.vtSeq
}

// historyStateOf is the part of a pane's emulator a restore needs: the main
// screen and up to maxScrollback history rows, packed.
//
// A full-screen program on the alternate screen at the moment of the save is
// left out. Its screen is gone the moment the program is, and what the user
// had before it, the shell's screen, is the main one, which the snapshot
// carries while the alternate one is up.
func historyStateOf(t vt.Terminal, maxScrollback int) *TerminalState {
	st := terminalStateOf(t, t.Width(), t.Height(), maxScrollback, 0, true)
	if st.IsAltScreen {
		st.PackedScreen, st.PackedMain = st.PackedMain, nil
		// The flags read were the alternate screen's. The main screen's are
		// not reachable while it is hidden, and no flag is the safe answer:
		// a wrongly joined row glues two lines into one.
		st.ScreenWraps = nil
		st.IsAltScreen = false
		st.CursorY = -1
	}
	st.Modes, st.KittyKbdStack, st.Pen, st.Margins, st.Charsets, st.CursorShape = nil, nil, nil, nil, nil, 0
	return st
}

func encodeHistory(h *savedHistory) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if err := gob.NewEncoder(zw).Encode(h); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// maxHistoryFile bounds what a history file may inflate to when it is read,
// so a damaged or planted file cannot take the daemon's memory at start.
const maxHistoryFile = 256 << 20

func decodeHistory(r io.Reader) (*savedHistory, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var h savedHistory
	if err := gob.NewDecoder(io.LimitReader(zr, maxHistoryFile)).Decode(&h); err != nil {
		return nil, err
	}
	if h.Version != historyVersion {
		return nil, fmt.Errorf("history version %d, this build reads %d", h.Version, historyVersion)
	}
	if h.State == nil || h.State.Width <= 0 || h.State.Height <= 0 {
		return nil, fmt.Errorf("history has no screen")
	}
	return &h, nil
}

// loadHistory reads a session's saved pane histories, by window id. A file
// that does not read is removed and skipped: it is history, and a restore
// never waits on it or fails for it.
func loadHistory(sessionName string) map[string]*savedHistory {
	dir := historyDir(sessionName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make(map[string]*savedHistory, len(entries))
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), historyExt)
		if !ok || e.IsDir() || !validHistoryWindowID(id) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, err := os.Open(path) // #nosec G304 -- a name listed from our own directory
		if err != nil {
			continue
		}
		h, err := decodeHistory(f)
		_ = f.Close()
		if err != nil {
			log.Printf("Discarding saved history %s: %v", path, err)
			_ = os.Remove(path)
			continue
		}
		out[id] = h
	}
	return out
}

// historyBanner is the divider a restored pane shows under its history, or
// the banner alone when it has none.
func historyBanner(cwd string, savedAt time.Time) string {
	msg := "-- tuios: restored from " + savedAt.Local().Format("Jan 2 15:04") + ", fresh shell"
	if cwd != "" {
		msg += " in " + cwd
	}
	msg += " --"
	return "\x1b[2m" + msg + "\x1b[0m\r\n"
}

// restoreHistory lays a saved history into a new pane's emulator: the saved
// history rows go into its history, the saved screen's used rows onto its
// screen, and the cursor under them, where the caller writes the divider.
//
// The emulator is at the saved size while this happens, and the caller
// resizes it afterwards, so a pane that comes back at another width reflows
// its history through the emulator's own reflow rather than being cut.
func restoreHistory(t vt.Terminal, h *savedHistory) {
	st := h.State
	if err := st.Unpack(); err != nil {
		debugLog("[DEBUG] saved history does not unpack: %v", err)
		return
	}
	rows := st.Scrollback
	wraps := wrapFlags(st.ScrollbackWraps, len(st.Scrollback))
	used := -1
	for y, row := range st.Screen {
		if !blankRow(row) {
			used = y
		}
	}
	if st.CursorY >= 0 && st.CursorY < len(st.Screen) && st.CursorY > used {
		// The prompt the shell was sitting at, even with nothing typed on it.
		used = st.CursorY
	}
	screenWraps := wrapFlags(st.ScreenWraps, len(st.Screen))
	rows = append(rows[:len(rows):len(rows)], st.Screen[:used+1]...)
	wraps = append(wraps, screenWraps[:used+1]...)
	if len(wraps) > 0 {
		// The divider goes on the next row, so the last saved row does not
		// carry on into it.
		wraps[len(wraps)-1] = false
	}

	// Room under the saved rows for the divider and the new prompt.
	keep := min(len(rows), max(st.Height-2, 0))
	split := len(rows) - keep
	out := &TerminalState{
		Width:         st.Width,
		Height:        st.Height,
		CursorX:       0,
		CursorY:       keep,
		ScrollbackLen: split,
		Scrollback:    rows[:split],
		Screen:        rows[split:],
	}
	out.ScrollbackWraps = wrapBits(wraps[:split])
	out.ScreenWraps = wrapBits(wraps[split:])
	ApplyTerminalState(t, out)
}

// blankRow reports whether a row shows nothing.
func blankRow(row []CellState) bool {
	for i := range row {
		c := &row[i]
		if c.Content != "" && c.Content != " " {
			return false
		}
		if c.StyleState != (StyleState{}) {
			return false
		}
	}
	return true
}

// newRestoredEmulator builds the emulator for a restored pane of width x
// height: the saved history, when there is one, and the banner.
func newRestoredEmulator(width, height, scrollback int, cwd string, h *savedHistory) vt.Terminal {
	if h == nil {
		t := vt.NewWithScrollback(width, height, scrollback)
		_, _ = t.Write([]byte(restoredBanner(cwd)))
		return t
	}
	t := vt.NewWithScrollback(h.State.Width, h.State.Height, scrollback)
	restoreHistory(t, h)
	_, _ = t.Write([]byte(historyBanner(cwd, h.SavedAt)))
	if t.Width() != width || t.Height() != height {
		t.Resize(width, height)
	}
	return t
}
