package session

import (
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"tuios.dev/vt"
	"tuios.dev/vt/snapshot"
)

const (
	// DefaultHistoryLines is how many history rows a pane saves when the
	// config names no number: daemon.persist_scrollback_lines. It is the
	// number a client asks for when it attaches, so a restore does not save
	// rows no client is sent. It also bounds how long a save holds a pane:
	// libghostty hands history out a cell at a time, and 5000 rows of a
	// 200-column pane took over 250 ms there.
	DefaultHistoryLines = DefaultStateScrollback
	// DefaultHistoryKB is the most one pane's saved file may take on disk,
	// compressed, when the config names no number: daemon.persist_scrollback_kb.
	DefaultHistoryKB = 2048
	historyDirName   = "scrollback"
	historyExt       = ".hist.gz"
	historyVersion   = snapshot.PackedFormatVersion*100 + 1

	maxHistoryDim   = hostedPaneMaxDim
	maxHistoryCells = 1 << 20
)

var historySessionBytes int64 = 16 << 20

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

type savedHistory struct {
	Version int
	SavedAt time.Time
	State   *TerminalState
}

type restoreSpec struct {
	history  *savedHistory
	savedCwd string
}

type historyMark struct {
	ptyID   string
	seq     int64
	at      time.Time
	savedAt time.Time
}

type historySaver struct {
	mu    sync.Mutex
	marks map[string]historyMark
}

func historyRoot() string {
	return filepath.Join(getResurrectionDir(), historyDirName)
}

func historyDir(sessionName string) (string, error) {
	return stateJoin(historyRoot(), sessionName, "")
}

func historyPath(sessionName, windowID string) (string, error) {
	dir, err := historyDir(sessionName)
	if err != nil {
		return "", err
	}
	return stateJoin(dir, windowID, historyExt)
}

func validHistoryWindowID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`) &&
		!strings.ContainsFunc(id, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// RemoveHistory deletes every saved pane history of a session.
func RemoveHistory(sessionName string) {
	dir, err := historyDir(sessionName)
	if err != nil {
		return
	}
	_ = os.RemoveAll(dir)
}

// RemoveAllHistory deletes every saved pane history of every session. The
// daemon calls it on start when persist_scrollback is off, so turning the
// setting off also takes what was saved while it was on off the disk.
func RemoveAllHistory() {
	_ = os.RemoveAll(historyRoot())
}

func cleanOrphanHistory() {
	entries, err := os.ReadDir(historyRoot())
	if err != nil {
		return
	}
	for _, e := range entries {
		if path, err := getResurrectionPath(e.Name()); err == nil {
			if _, err := os.Stat(path); err == nil {
				continue
			}
		}
		if err := os.RemoveAll(filepath.Join(historyRoot(), e.Name())); err != nil {
			LogError("Failed to remove saved history of gone session %s: %v", e.Name(), err)
		}
	}
}

func (d *Daemon) dropHistoryWhenOff() {
	if d.manager.HistoryPolicy().TurnedOff {
		RemoveAllHistory()
	}
}

func moveHistory(oldName, newName string) {
	from, err := historyDir(oldName)
	if err != nil {
		return
	}
	to, err := historyDir(newName)
	if err != nil {
		return
	}
	if _, err := os.Stat(from); err != nil {
		return
	}
	_ = os.RemoveAll(to)
	if err := os.Rename(from, to); err != nil {
		LogError("Moving saved history of session %q to %q failed: %v", oldName, newName, err)
	}
}

func (s *Session) saveHistory(state *SessionState, force bool) {
	pol := s.historyPolicy()
	if !pol.Enabled || state == nil || state.Name != s.Name() || s.discardHistory.Load() {
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
				continue
			}
			if !force && now.Sub(mark.at) < scrollbackSaveInterval {
				continue
			}
		}
		savedAt := now
		if seen && mark.ptyID == pty.ID && !mark.savedAt.IsZero() {
			savedAt = mark.savedAt
		}
		seq, err := s.saveOnePane(name, w.ID, pty, pol, savedAt)
		if err != nil {
			LogError("Saving the history of pane %s in session %q failed: %v", shortID(w.ID), name, err)
			continue
		}
		s.history.setMark(w.ID, historyMark{ptyID: pty.ID, seq: seq, at: now})
	}
	pruneHistory(name, keep)
	s.history.prune(keep)
}

func historyWanted(w WindowState) bool {
	if w.Popup && !w.Scratch {
		return false
	}
	return w.Host == ""
}

func (s *Session) saveOnePane(sessionName, windowID string, pty *PTY, pol HistoryPolicy, savedAt time.Time) (int64, error) {
	budget := min(pol.Bytes, historySessionBytes-otherHistoryBytes(sessionName, windowID))
	rows, seq := pty.captureHistory(pol.Lines)
	if rows == nil {
		return seq, nil
	}
	lines := rows.Len()
	var data []byte
	for range 4 {
		var err error
		data, err = encodeHistory(&savedHistory{Version: historyVersion, SavedAt: savedAt, State: rows.State(lines)})
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
		lines = int(float64(lines) * float64(budget) / float64(len(data)) * 0.8)
		data = nil
	}
	path, err := historyPath(sessionName, windowID)
	if err != nil {
		return seq, err
	}
	if data == nil {
		debugLog("[DEBUG] pane %s history does not fit in %d bytes, not saved", shortID(windowID), budget)
		_ = os.Remove(path)
		return seq, nil
	}
	if historyWriteHook != nil {
		historyWriteHook(sessionName, windowID)
	}
	return seq, writePrivateFile(path, data)
}

var historyWriteHook func(sessionName, windowID string)

func writePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// #nosec G302 - a directory needs its execute bit, and 0700 is owner only
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	return atomicfile.Write(path, data, 0o600, false)
}

func otherHistoryBytes(sessionName, windowID string) int64 {
	dir, err := historyDir(sessionName)
	if err != nil {
		return 0
	}
	entries, err := os.ReadDir(dir)
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

func pruneHistory(sessionName string, keep map[string]bool) {
	dir, err := historyDir(sessionName)
	if err != nil {
		return
	}
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

func (p *PTY) consumedSeq() int64 {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return p.vtSeq
}

func (p *PTY) captureHistory(lines int) (*snapshot.History, int64) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	if p.terminal == nil {
		return nil, p.vtSeq
	}
	return snapshot.CaptureHistory(p.terminal, lines), p.vtSeq
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

const maxHistoryFile = 256 << 20

type errHistoryVersion struct{ v int }

func (e errHistoryVersion) Error() string {
	return fmt.Sprintf("history version %d, this build reads %d", e.v, historyVersion)
}

func decodeHistory(r io.Reader) (*savedHistory, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var h savedHistory
	lr := io.LimitReader(zr, maxHistoryFile)
	if err := gob.NewDecoder(lr).Decode(&h); err != nil {
		return nil, err
	}
	if _, err := io.Copy(io.Discard, lr); err != nil {
		return nil, err
	}
	if h.Version != historyVersion {
		return nil, errHistoryVersion{h.Version}
	}
	if st := h.State; st == nil || st.Width <= 0 || st.Height <= 0 ||
		st.Width > maxHistoryDim || st.Height > maxHistoryDim || st.Width*st.Height > maxHistoryCells {
		return nil, errfmt.Errorf("history screen size is out of bounds")
	}
	return &h, nil
}

func loadHistory(sessionName string) map[string]*savedHistory {
	dir, err := historyDir(sessionName)
	if err != nil {
		return nil
	}
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
		if _, ok := errors.AsType[errHistoryVersion](err); ok {
			log.Printf("Skipping saved history %s: %v", path, err)
			continue
		}
		if err != nil {
			log.Printf("Discarding saved history %s: %v", path, err)
			_ = os.Remove(path)
			continue
		}
		out[id] = h
	}
	return out
}

func restoredBanner(cwd string, r *restoreSpec) string {
	var msg string
	if r.history != nil {
		msg = "-- tuios: restored from " + r.history.SavedAt.Local().Format("Jan 2 15:04") + ", fresh shell"
	} else {
		msg = "-- tuios: session restored, fresh shell"
	}
	if cwd != "" {
		msg += " in " + cwd
	}
	if r.savedCwd != "" && r.savedCwd != cwd {
		msg += " (" + r.savedCwd + " no longer available)"
	}
	msg += " --"
	return "\x1b[2m" + msg + "\x1b[0m\r\n"
}

func restoreHistory(t vt.Terminal, h *savedHistory) {
	if err := snapshot.ApplyHistory(t, h.State); err != nil {
		debugLog("[DEBUG] saved history does not unpack: %v", err)
	}
}

func newRestoredEmulator(width, height, scrollback int, cwd string, r *restoreSpec) vt.Terminal {
	h := r.history
	if h == nil {
		t := vt.NewWithScrollback(width, height, scrollback)
		_, _ = t.Write([]byte(restoredBanner(cwd, r)))
		return t
	}
	t := vt.NewWithScrollback(h.State.Width, h.State.Height, scrollback)
	restoreHistory(t, h)
	_, _ = t.Write([]byte(restoredBanner(cwd, r)))
	if t.Width() != width || t.Height() != height {
		t.Resize(width, height)
	}
	return t
}
