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

// SaveSessionForResurrection persists the session state to disk.
func SaveSessionForResurrection(state *SessionState) error {
	if state == nil || state.Name == "" {
		return nil
	}

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

// LoadResurrectionState loads a saved session state from disk.
func LoadResurrectionState(sessionName string) (*SessionState, error) {
	path := getResurrectionPath(sessionName)
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("no resurrection data for session %q: %w", sessionName, err)
	}

	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse resurrection data: %w", err)
	}

	return &state, nil
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
