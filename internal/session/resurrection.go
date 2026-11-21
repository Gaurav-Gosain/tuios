package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/adrg/xdg"
)

const (
	resurrectionDir           = "tuios/sessions"
	resurrectionInterval      = 30 * time.Second
	resurrectionDirtyInterval = 2 * time.Second
	signalHoldFor             = 2 * time.Minute

	// ResurrectionVersion is the current on-disk resurrection schema version.
	// It is bumped only when the schema changes in a way older daemons cannot
	// read. State whose ResurrectionVersion is greater than this is treated as
	// incompatible and archived rather than loaded. Version 0 (files written
	// before versioning existed) is a structural subset of the current schema
	// and loads without issue.
	ResurrectionVersion = 1

	// RestoredTag is the marker every surface shows on a session that came back
	// from saved state, and RestoredNote is the sentence that says what came back
	// with it. Both live here so the rail, the switcher, `tuios ls` and the
	// attach path cannot word the same fact differently.
	RestoredTag  = "restored"
	RestoredNote = "the layout came back from saved state, and the shells are new"

	// SavedTag and SavedNote word the state before that one: state that is on
	// disk with no daemon holding it. A saved session is not a restored session
	// and must not borrow its wording, or a listing would claim sessions are
	// back when nothing has started them yet.
	SavedTag  = "saved"
	SavedNote = "on disk only, with no daemon running to hold it"
)

var resurrectionDirOverride atomic.Value

func getResurrectionDir() string {
	if override, _ := resurrectionDirOverride.Load().(string); override != "" {
		return override
	}
	return filepath.Join(xdg.StateHome, resurrectionDir)
}

func getResurrectionPath(sessionName string) (string, error) {
	return stateJoin(getResurrectionDir(), sessionName, ".json")
}

func stateJoin(dir, name, suffix string) (string, error) {
	if name == "" || strings.Trim(name, ".") == "" || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") ||
		strings.ContainsAny(name, "/\\\x00") || (runtime.GOOS == "windows" && windowsDeviceName(name)) {
		return "", errfmt.Errorf("the name %q cannot name a saved state file", name)
	}
	path := filepath.Join(dir, name+suffix)
	if filepath.Dir(path) != filepath.Clean(dir) {
		return "", errfmt.Errorf("the name %q cannot name a saved state file", name)
	}
	return path, nil
}

// ValidateSessionName rejects a name a session could never be saved under. The
// name is also the name of its state file, so a name carrying a path separator
// produced a session that ran perfectly and silently never persisted: the write
// went to a directory that does not exist, and the error was thrown away. The
// only place the user can still be told is when they choose the name.
//
// An empty name is allowed: the manager generates one.
func ValidateSessionName(name string) error {
	if name == "" {
		return nil
	}
	if strings.TrimSpace(name) != name {
		return errfmt.Errorf("session name %q has leading or trailing whitespace", name)
	}
	if strings.Trim(name, ".") == "" {
		return errfmt.Errorf("session name %q is reserved. Use a name with a letter or a digit", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return errfmt.Errorf("session name %q contains a path separator. A session name is also the name of its state file", name)
	}
	if strings.HasSuffix(name, ".") {
		return errfmt.Errorf("session name %q ends in a dot. Remove the dot", name)
	}
	if windowsDeviceName(name) {
		return errfmt.Errorf("session name %q is a device name on Windows. Use a different name", name)
	}
	if strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return errfmt.Errorf("session name %q contains a control character", name)
	}
	return nil
}

func windowsDeviceName(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}

// ResurrectionStateDir returns the directory holding saved session state, so a
// message can name where sessions are persisted.
func ResurrectionStateDir() string {
	return getResurrectionDir()
}

// ResurrectionArchiveDir returns the directory where corrupt or incompatible
// state files are moved instead of being deleted, so they can be inspected. It
// is exported so an error message can tell the user where their session went
// rather than only that it is gone.
func ResurrectionArchiveDir() string {
	return filepath.Join(getResurrectionDir(), "archive")
}

func archiveResurrectionFile(path string) string {
	archiveDir := ResurrectionArchiveDir()
	if err := os.MkdirAll(archiveDir, 0700); err != nil {
		_ = os.Remove(path)
		return ""
	}
	base := filepath.Base(path)
	dest := filepath.Join(archiveDir, fmt.Sprintf("%s.%d.bak", base, time.Now().UnixNano()))
	if err := os.Rename(path, dest); err != nil {
		_ = os.Remove(path)
		return ""
	}
	return dest
}

func archivedNote(dest string) string {
	if dest == "" {
		return "it could not be archived and was removed"
	}
	return "archived to " + dest
}

const archiveRetention = 14 * 24 * time.Hour

const staleTempAge = 10 * time.Minute

// CleanResurrectionDir removes what the save and archive paths leave behind:
// temp files from writes that died before their rename, and archived state past
// archiveRetention. Neither had anything cleaning it, so both grew forever.
//
// Best effort and never fatal. It runs on daemon start, which is the one moment
// no save of this daemon's can be in flight, and only one daemon runs at a time.
func CleanResurrectionDir() {
	dir := getResurrectionDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			LogError("Failed to remove leftover resurrection temp file %s: %v", entry.Name(), err)
		}
	}

	cleanOrphanHistory()

	for _, d := range []string{
		filepath.Dir(outboxPath()), filepath.Dir(attentionPath()),
		filepath.Dir(reviewNotesPath()), filepath.Dir(appliedGrantsPath()), pushDir(),
	} {
		atomicfile.SweepStale(d, staleTempAge)
	}

	archiveDir := ResurrectionArchiveDir()
	archived, err := os.ReadDir(archiveDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-archiveRetention)
	for _, entry := range archived {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(archiveDir, entry.Name())); err != nil {
			LogError("Failed to prune archived session state %s: %v", entry.Name(), err)
		}
	}
}

// SaveSessionForResurrection persists the session state to disk.
func SaveSessionForResurrection(state *SessionState) error {
	if state == nil || state.Name == "" {
		return nil
	}

	state.ResurrectionVersion = ResurrectionVersion

	dir := getResurrectionDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errfmt.Errorf("failed to create resurrection dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return errfmt.Errorf("failed to marshal session state: %w", err)
	}

	path, err := getResurrectionPath(state.Name)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(path, data, 0o600, false); err != nil {
		return errfmt.Errorf("failed to write resurrection file: %w", err)
	}

	return nil
}

// LoadResurrectionState loads a saved session state from disk. Corrupt or
// version-incompatible files are archived (not deleted) and returned as an
// error, so a single bad file can never crash the daemon or block startup.
func LoadResurrectionState(sessionName string) (*SessionState, error) {
	path, err := getResurrectionPath(sessionName)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return nil, errfmt.Errorf("no resurrection data for session %q: %w", sessionName, err)
	}

	var state SessionState
	if err := jsonx.Unmarshal(data, &state); err != nil {
		dest := archiveResurrectionFile(path)
		return nil, errfmt.Errorf("saved state for session %q is corrupt and cannot be restored (%s): %w",
			sessionName, archivedNote(dest), err)
	}

	if state.ResurrectionVersion > ResurrectionVersion {
		dest := archiveResurrectionFile(path)
		return nil, errfmt.Errorf("saved state for session %q was written by a newer TUIOS (state version %d, this build reads up to %d) and cannot be restored (%s)",
			sessionName, state.ResurrectionVersion, ResurrectionVersion, archivedNote(dest))
	}

	return &state, nil
}

// ResurrectableInfo summarizes a resurrectable session for listing.
type ResurrectableInfo struct {
	Name        string    // Session name
	WindowCount int       // Number of windows saved
	SavedAt     time.Time // Modification time of the state file
	Space       string    // The space the session was filed under, or empty
}

// ListResurrectableInfos returns metadata for every resurrectable session,
// sorted by name. Corrupt/incompatible files are skipped (and archived).
func ListResurrectableInfos() ([]ResurrectableInfo, error) {
	names, err := ListResurrectableSessions()
	if err != nil {
		return nil, err
	}

	infos := make([]ResurrectableInfo, 0, len(names))
	for _, name := range names {
		state, err := LoadResurrectionState(name)
		if err != nil {
			continue
		}
		info := ResurrectableInfo{Name: name, WindowCount: len(state.Windows), Space: state.Space}
		if path, err := getResurrectionPath(name); err == nil {
			if fi, statErr := os.Stat(path); statErr == nil {
				info.SavedAt = fi.ModTime()
			}
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// ListResurrectableSessions returns names of sessions that can be restored.
func ListResurrectableSessions() ([]string, error) {
	dir := getResurrectionDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := entry.Name()[:len(entry.Name())-5]
		names = append(names, name)
	}
	return names, nil
}

// RemoveResurrectionState deletes the resurrection file for a session, and
// its saved pane history.
func RemoveResurrectionState(sessionName string) {
	path, err := getResurrectionPath(sessionName)
	if err != nil {
		return
	}
	_ = os.Remove(path)
	RemoveHistory(sessionName)
}

func startPeriodicSaveWith(every time.Duration, getState func() *SessionState, dirty, stale *wakeLatch, wakes *atomic.Int64, save func(*SessionState) error) func() {
	stopCh := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		start := time.Now()
		lastSave := start
		due := start.Add(resurrectionDirtyInterval)
		timer := time.NewTimer(resurrectionDirtyInterval)
		armed := true
		defer timer.Stop()
		arm := func(at time.Time) {
			if armed && !at.Before(due) {
				return
			}
			due, armed = at, true
			timer.Reset(time.Until(at))
		}
		staleDue := func(now time.Time) time.Time {
			at := lastSave.Add(every)
			if soon := now.Add(resurrectionDirtyInterval); at.Before(soon) {
				return soon
			}
			return at
		}

		for {
			select {
			case <-dirty.C():
				if dirty.Up() {
					arm(time.Now().Add(resurrectionDirtyInterval))
				}
			case <-stale.C():
				if stale.Up() {
					arm(staleDue(time.Now()))
				}
			case now := <-timer.C:
				armed = false
				if wakes != nil {
					wakes.Add(1)
				}
				isDirty := dirty.Take()
				if !isDirty && !(stale.Up() && now.Sub(lastSave) >= every) {
					if stale.Up() {
						arm(staleDue(now))
					}
					continue
				}
				stale.Take()
				lastSave = now
				state := getState()
				if state == nil {
					continue
				}
				if err := save(state); err != nil {
					LogError("Resurrection save for session %q failed: %v", state.Name, err)
					stale.Set()
				}
			case <-stopCh:
				return
			}
		}
	}()

	return func() {
		close(stopCh)
		<-done
	}
}

func (s *Session) holdingCheckpoint() bool {
	until := s.signalHold.Load()
	if until == 0 {
		return false
	}
	if time.Now().UnixNano() < until {
		s.saveStale.Set()
		return true
	}
	s.signalHold.CompareAndSwap(until, 0)
	return false
}
