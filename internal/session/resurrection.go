// Package session provides session resurrection - the ability to restore
// session state after a daemon crash or restart.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/adrg/xdg"
)

const (
	resurrectionDir      = "tuios/sessions"
	resurrectionInterval = 30 * time.Second

	// ResurrectionVersion is the current on-disk resurrection schema version.
	// It is bumped only when the schema changes in a way older daemons cannot
	// read. State whose ResurrectionVersion is greater than this is treated as
	// incompatible and archived rather than loaded. Version 0 (files written
	// before versioning existed) is a structural subset of the current schema
	// and loads without issue.
	ResurrectionVersion = 1
)

// resurrectionDirOverride is set during tests to use a temp directory.
var resurrectionDirOverride string

// getResurrectionDir returns the directory for session resurrection files.
func getResurrectionDir() string {
	if resurrectionDirOverride != "" {
		return resurrectionDirOverride
	}
	return filepath.Join(xdg.StateHome, resurrectionDir)
}

// getResurrectionPath returns the path for a specific session's resurrection file.
func getResurrectionPath(sessionName string) string {
	return filepath.Join(getResurrectionDir(), sessionName+".json")
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

// archiveResurrectionFile moves a bad state file into the archive directory,
// tagging it with a timestamp, and returns where it landed. Best effort: on any
// failure the original file is removed so it is not retried on every load, and
// an empty path is returned. Never returns an error to callers because the load
// must continue regardless.
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

// archivedNote renders where an archived state file was moved to, for inclusion
// in the error that reports it. It degrades to naming the archive directory when
// the move itself failed.
func archivedNote(dest string) string {
	if dest == "" {
		return "it could not be archived and was removed"
	}
	return "archived to " + dest
}

// SaveSessionForResurrection persists the session state to disk.
func SaveSessionForResurrection(state *SessionState) error {
	if state == nil || state.Name == "" {
		return nil
	}

	// Stamp the current schema version on every write regardless of caller, so
	// the file is always self-describing even though clients that build the
	// state do not set this field.
	state.ResurrectionVersion = ResurrectionVersion

	dir := getResurrectionDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create resurrection dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session state: %w", err)
	}

	path := getResurrectionPath(state.Name)
	// Write to temp file then rename for atomicity
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write resurrection file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename resurrection file: %w", err)
	}

	return nil
}

// LoadResurrectionState loads a saved session state from disk. Corrupt or
// version-incompatible files are archived (not deleted) and returned as an
// error, so a single bad file can never crash the daemon or block startup.
func LoadResurrectionState(sessionName string) (*SessionState, error) {
	path := getResurrectionPath(sessionName)
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("no resurrection data for session %q: %w", sessionName, err)
	}

	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		dest := archiveResurrectionFile(path)
		return nil, fmt.Errorf("saved state for session %q is corrupt and cannot be restored (%s): %w",
			sessionName, archivedNote(dest), err)
	}

	if state.ResurrectionVersion > ResurrectionVersion {
		dest := archiveResurrectionFile(path)
		return nil, fmt.Errorf("saved state for session %q was written by a newer TUIOS (state version %d, this build reads up to %d) and cannot be restored (%s)",
			sessionName, state.ResurrectionVersion, ResurrectionVersion, archivedNote(dest))
	}

	return &state, nil
}

// ResurrectableInfo summarizes a resurrectable session for listing.
type ResurrectableInfo struct {
	Name        string    // Session name
	WindowCount int       // Number of windows saved
	SavedAt     time.Time // Modification time of the state file
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
		info := ResurrectableInfo{Name: name, WindowCount: len(state.Windows)}
		if fi, statErr := os.Stat(getResurrectionPath(name)); statErr == nil {
			info.SavedAt = fi.ModTime()
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// processCwd returns the working directory of the process with the given PID.
// It reads /proc/<pid>/cwd, which exists on Linux (and some BSDs). On platforms
// without procfs the readlink fails and (,"", false) is returned, in which case
// resurrection falls back to spawning the shell in its default directory.
func processCwd(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil || cwd == "" {
		return "", false
	}
	return cwd, true
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
		name := entry.Name()[:len(entry.Name())-5] // strip .json
		names = append(names, name)
	}
	return names, nil
}

// RemoveResurrectionState deletes the resurrection file for a session.
func RemoveResurrectionState(sessionName string) {
	path := getResurrectionPath(sessionName)
	_ = os.Remove(path)
}

// StartPeriodicSave starts a goroutine that periodically saves session state.
// Returns a stop function to halt the periodic saving.
func StartPeriodicSave(getState func() *SessionState) func() {
	stopCh := make(chan struct{})

	go func() {
		ticker := time.NewTicker(resurrectionInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				state := getState()
				if state != nil {
					_ = SaveSessionForResurrection(state)
				}
			case <-stopCh:
				return
			}
		}
	}()

	return func() {
		close(stopCh)
	}
}
