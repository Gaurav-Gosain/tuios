package tutor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// progressVersion is the format of tutor.json.
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

// progressStore reads and writes tutor.json. It is the only file the tutor
// writes outside its sandbox.
type progressStore struct {
	path string
	p    Progress
}

// loadProgress reads path. A missing or unreadable file is no progress, so a
// broken file never stops the tutor.
func loadProgress(path string) *progressStore {
	s := &progressStore{path: path, p: Progress{Version: progressVersion, Tracks: map[string]*TrackProgress{}}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var p Progress
	if json.Unmarshal(raw, &p) != nil || p.Version != progressVersion {
		return s
	}
	for id, t := range p.Tracks {
		if t != nil {
			s.p.Tracks[id] = t
		}
	}
	return s
}

// track is the progress on id, nil when there is none.
func (s *progressStore) track(id string) *TrackProgress { return s.p.Tracks[id] }

// doneCount is how many steps of id are done.
func (s *progressStore) doneCount(id string) int {
	if t := s.track(id); t != nil {
		return len(t.Done)
	}
	return 0
}

// markStep records a done step and writes the file.
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

// markFinished records a finished run and its time, and writes the file.
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

// save writes the file whole: a temp file beside it, then a rename, so a
// crash leaves the old file or the new one and never half of one.
func (s *progressStore) save() error {
	raw, err := json.MarshalIndent(s.p, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tutor-*.json")
	if err != nil {
		return err
	}
	_, werr := f.Write(append(raw, '\n'))
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	if err := os.Rename(f.Name(), s.path); err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	return nil
}

// resetProgress removes the file. A file that is not there is no error.
func resetProgress(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
