package tutor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

const progressVersion = 1

// Progress is what the person has done, by track. It is the shape the page
// at tuios.dev/learn keeps in its localStorage, under a version.
type Progress struct {
	Version int                       `json:"version"`
	Tracks  map[string]*TrackProgress `json:"tracks"`
}

// TrackProgress is one track: the steps done, by id, and the best time of a
// finished run in seconds.
type TrackProgress struct {
	Done     []string `json:"done"`
	Finished bool     `json:"finished,omitempty"`
	Best     int      `json:"best,omitempty"`
}

type progressStore struct {
	path string
	p    Progress
}

func loadProgress(path string) *progressStore {
	s := &progressStore{path: path, p: Progress{Version: progressVersion, Tracks: map[string]*TrackProgress{}}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var p Progress
	if jsonx.Unmarshal(raw, &p) != nil || p.Version != progressVersion {
		return s
	}
	for id, t := range p.Tracks {
		if t != nil {
			s.p.Tracks[id] = t
		}
	}
	return s
}

func (s *progressStore) track(id string) *TrackProgress { return s.p.Tracks[id] }

func (s *progressStore) doneCount(id string) int {
	if t := s.track(id); t != nil {
		return len(t.Done)
	}
	return 0
}

func (s *progressStore) markStep(track, step string) error {
	t := s.p.Tracks[track]
	if t == nil {
		t = &TrackProgress{}
		s.p.Tracks[track] = t
	}
	if slices.Contains(t.Done, step) {
		return nil
	}
	t.Done = append(t.Done, step)
	return s.save()
}

func (s *progressStore) markFinished(track string, seconds int) error {
	t := s.p.Tracks[track]
	if t == nil {
		t = &TrackProgress{}
		s.p.Tracks[track] = t
	}
	t.Finished = true
	if seconds > 0 && (t.Best == 0 || seconds < t.Best) {
		t.Best = seconds
	}
	return s.save()
}

func (s *progressStore) save() error {
	raw, err := json.MarshalIndent(s.p, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return atomicfile.Write(s.path, append(raw, '\n'), 0o600, false)
}

func resetProgress(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
