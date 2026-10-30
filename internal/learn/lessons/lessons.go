// Package lessons holds the lessons of the tuios tour as data, and the engine
// that checks a person's progress through them against the events
// internal/learn reports.
//
// tracks.json lists the tracks in order, the groups the hub shows them under,
// and the coach's shared lines. tracks/<id>.json is one track: its steps, the
// keys of each, a hint, and a rule that says when the step is done (see
// Rule). Every surface reads these same files: the page at tuios.dev/learn
// through the wasm build's tuios.lessons(), and later the terminal tutor.
//
// A step's words may name one surface, such as a click in the browser. Its
// "web" or "terminal" object then holds that surface's title, note or hint,
// and Step.For puts them in place.
//
// The engine (Lesson) is the only one. The page renders the lesson.* events
// the wasm build sends from it, and keeps no step logic of its own.
package lessons

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/tarpack"
)

//go:generate go run ../../tarpack/gen.go lessons.tar.gz tracks.json tracks

// archive is tracks.json and tracks/*.json. Only Learn mode and the tutor read
// it, so it stays packed until then. TestArchiveMatchesFiles fails when it and
// the files differ.
//
//go:embed lessons.tar.gz
var archive []byte

// Version is the format of the lesson files. A change a reader of an older
// format would misread bumps it.
const Version = 1

// Surface names: where a lesson runs.
const (
	SurfaceWeb      = "web"
	SurfaceTerminal = "terminal"
)

// File is every lesson, as tuios.lessons() returns it.
type File struct {
	Version int      `json:"version"`
	Groups  []Group  `json:"groups"`
	Coach   Coach    `json:"coach"`
	Tracks  []*Track `json:"tracks"`
}

// Group is a heading the hub lists tracks under.
type Group struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Coach is what the coach (Tilly, on the page) says, shared by every track.
// A line may hold {name} slots the page fills, such as {n} or {steps}.
type Coach struct {
	// Greet and Finish are for a track with no lines of its own.
	Greet  []string `json:"greet"`
	Finish []string `json:"finish"`
	// HintedLead goes before the reaction when the step needed the hint.
	HintedLead []string `json:"hintedLead"`
	// Reactions maps a kind of step done, such as "split", to its lines.
	Reactions map[string][]string `json:"reactions"`
	// Nudges maps a reason the person looks stuck to its lines.
	Nudges map[string][]string `json:"nudges"`
	// Poke is for a click on the coach.
	Poke []string `json:"poke"`
}

// TrackCoach is a track's own greeting and cheer.
type TrackCoach struct {
	Greet  []string `json:"greet,omitempty"`
	Finish []string `json:"finish,omitempty"`
}

// Track is one chapter.
type Track struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Blurb    string `json:"blurb"`
	Audience string `json:"audience"`
	Group    string `json:"group"`
	Minutes  int    `json:"minutes"`
	// Surfaces are where the track is ready: "web", "terminal" or both.
	Surfaces []string    `json:"surfaces"`
	Next     []string    `json:"next,omitempty"`
	Setup    []Setup     `json:"setup,omitempty"`
	Coach    *TrackCoach `json:"coach,omitempty"`
	Steps    []Step      `json:"steps"`
}

// On reports whether the track is ready on surface.
func (t *Track) On(surface string) bool {
	return slices.Contains(t.Surfaces, surface)
}

// Setup is one thing the tour does to set the scene: a learn command (see
// learn.Commands), or raw input typed as if by the person.
type Setup struct {
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Input   string   `json:"input,omitempty"`
	// Until is the event the scene waits for before the next entry, such as
	// the window.open of a newWindow.
	Until *Rule `json:"until,omitempty"`
	// Wait is a pause after it, in milliseconds. Prefer Until: a guess is
	// too short on a slow machine.
	Wait int `json:"wait,omitempty"`
}

// Key is one keycap on a step card: a chord in tuios's notation, such as
// "ctrl+b", or text to type, such as a shell command.
type Key struct {
	Chord string
	Text  string
}

// UnmarshalJSON reads "ctrl+b" or {"text": "ls"}.
func (k *Key) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		k.Chord = s
		return nil
	}
	var t struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b, &t); err != nil || t.Text == "" {
		return fmt.Errorf(`a key is a chord string or {"text": ...}`)
	}
	k.Text = t.Text
	return nil
}

// MarshalJSON writes the form UnmarshalJSON reads.
func (k Key) MarshalJSON() ([]byte, error) {
	if k.Text != "" {
		return json.Marshal(map[string]string{"text": k.Text})
	}
	return json.Marshal(k.Chord)
}

// Explainer is a step about something the tour cannot run. It is done when
// the person says they read it.
type Explainer struct {
	Art      string `json:"art"`
	Body     string `json:"body"`
	Href     string `json:"href"`
	LinkText string `json:"linkText"`
}

// Override holds a step's words for one surface.
type Override struct {
	Title   string `json:"title,omitempty"`
	Note    string `json:"note,omitempty"`
	Hint    string `json:"hint,omitempty"`
	Waiting string `json:"waiting,omitempty"`
}

// Step is one thing to do.
type Step struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Note  string `json:"note,omitempty"`
	Keys  []Key  `json:"keys"`
	// Needs is the mode the keys work in, "window" or "terminal", for the
	// wrong mode nudge.
	Needs string  `json:"needs,omitempty"`
	Setup []Setup `json:"setup,omitempty"`
	// After are events the step depends on, such as an agent asking. Each
	// must have happened since the lesson started before the step can be
	// done: until then show me waits and the card says Waiting.
	After []*Rule `json:"after,omitempty"`
	// Waiting is what the card says while an After event has not come.
	Waiting string `json:"waiting,omitempty"`
	Done    *Rule  `json:"done,omitempty"`
	Hint    string `json:"hint"`
	// ShowMe is what "show me" types, when the keys' own bytes are not it.
	ShowMe    string     `json:"showMe,omitempty"`
	Explainer *Explainer `json:"explainer,omitempty"`
	Learned   string     `json:"learned,omitempty"`
	// Try is a command to run on a real machine after the tour, for an
	// explainer.
	Try      string    `json:"try,omitempty"`
	Web      *Override `json:"web,omitempty"`
	Terminal *Override `json:"terminal,omitempty"`
}

// For is the step with surface's own words in place.
func (s Step) For(surface string) Step {
	o := s.Web
	if surface == SurfaceTerminal {
		o = s.Terminal
	}
	if o == nil {
		return s
	}
	if o.Title != "" {
		s.Title = o.Title
	}
	if o.Note != "" {
		s.Note = o.Note
	}
	if o.Hint != "" {
		s.Hint = o.Hint
	}
	if o.Waiting != "" {
		s.Waiting = o.Waiting
	}
	return s
}

// index is tracks.json.
type index struct {
	Version int      `json:"version"`
	Groups  []Group  `json:"groups"`
	Tracks  []string `json:"tracks"`
	Coach   Coach    `json:"coach"`
}

var (
	loadOnce sync.Once
	loaded   *File
	joined   []byte
	loadErr  error
)

// Load parses the embedded lessons once and checks every rule.
func Load() (*File, error) {
	loadOnce.Do(func() { loaded, joined, loadErr = build() })
	return loaded, loadErr
}

// JSON is every lesson as one document: tracks.json with each track's file
// in place of its id, compacted. tuios.lessons() returns it and build.sh
// writes it as lessons.json, so the page and the hub read the same bytes.
func JSON() ([]byte, error) {
	if _, err := Load(); err != nil {
		return nil, err
	}
	return joined, nil
}

func build() (*File, []byte, error) {
	files, err := tarpack.Unpack(archive)
	if err != nil {
		return nil, nil, fmt.Errorf("lessons.tar.gz: %w", err)
	}
	raw, ok := files["tracks.json"]
	if !ok {
		return nil, nil, errors.New("lessons.tar.gz: no tracks.json")
	}
	var idx index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, nil, fmt.Errorf("tracks.json: %w", err)
	}
	if idx.Version != Version {
		return nil, nil, fmt.Errorf("tracks.json: version %d, want %d", idx.Version, Version)
	}
	parts := make([]json.RawMessage, 0, len(idx.Tracks))
	for _, id := range idx.Tracks {
		b, ok := files["tracks/"+id+".json"]
		if !ok {
			return nil, nil, fmt.Errorf("track %s: not in lessons.tar.gz", id)
		}
		parts = append(parts, b)
	}
	return Assemble(raw, parts)
}

// Assemble joins tracks.json and the track files into one File and its JSON,
// checking each. It is Load's work on files from anywhere, for the lint.
func Assemble(indexJSON []byte, trackJSON []json.RawMessage) (*File, []byte, error) {
	var idx index
	if err := json.Unmarshal(indexJSON, &idx); err != nil {
		return nil, nil, fmt.Errorf("tracks.json: %w", err)
	}
	f := &File{Version: idx.Version, Groups: idx.Groups, Coach: idx.Coach}
	var out bytes.Buffer
	out.WriteString(`{"version":`)
	fmt.Fprint(&out, idx.Version)
	for _, part := range []struct {
		name string
		v    any
	}{{"groups", idx.Groups}, {"coach", idx.Coach}} {
		b, err := json.Marshal(part.v)
		if err != nil {
			return nil, nil, err
		}
		fmt.Fprintf(&out, `,%q:`, part.name)
		out.Write(b)
	}
	out.WriteString(`,"tracks":[`)
	for i, raw := range trackJSON {
		var t Track
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&struct {
			Schema string `json:"$schema"`
			*Track
		}{Track: &t}); err != nil {
			return nil, nil, fmt.Errorf("track %d: %w", i, err)
		}
		if i < len(idx.Tracks) && t.ID != idx.Tracks[i] {
			return nil, nil, fmt.Errorf("track file %s holds track %q", idx.Tracks[i], t.ID)
		}
		if err := t.check(); err != nil {
			return nil, nil, fmt.Errorf("track %s: %w", t.ID, err)
		}
		f.Tracks = append(f.Tracks, &t)
		if i > 0 {
			out.WriteByte(',')
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return nil, nil, err
		}
		// The $schema line is for editors, not for readers of the lessons.
		out.Write(dropSchema(compact.Bytes()))
	}
	out.WriteString("]}")
	return f, out.Bytes(), nil
}

func dropSchema(b []byte) []byte {
	const prefix = `{"$schema":"`
	if !bytes.HasPrefix(b, []byte(prefix)) {
		return b
	}
	end := bytes.IndexByte(b[len(prefix):], '"')
	if end < 0 {
		return b
	}
	rest := b[len(prefix)+end+1:]
	rest = bytes.TrimPrefix(rest, []byte(","))
	return append([]byte("{"), rest...)
}

// check is what every track must hold for the engine to run it. The lint in
// internal/lint checks the rest against the app: action and overlay names,
// event types, setup commands and key chords.
func (t *Track) check() error {
	if t.ID == "" || t.Title == "" {
		return fmt.Errorf("a track needs an id and a title")
	}
	if len(t.Surfaces) == 0 {
		return fmt.Errorf("no surfaces")
	}
	if err := checkSetup(t.Setup); err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, s := range t.Steps {
		if s.ID == "" || ids[s.ID] {
			return fmt.Errorf("step id %q is empty or used twice", s.ID)
		}
		ids[s.ID] = true
		if len(s.Keys) == 0 {
			return fmt.Errorf("step %s: no keys", s.ID)
		}
		if err := checkSetup(s.Setup); err != nil {
			return fmt.Errorf("step %s: %w", s.ID, err)
		}
		switch {
		case s.Explainer != nil && s.Done != nil:
			return fmt.Errorf("step %s: an explainer has no done rule", s.ID)
		case s.Explainer == nil && s.Done == nil:
			return fmt.Errorf("step %s: no done rule", s.ID)
		case s.Explainer == nil && strings.TrimSpace(s.Hint) == "":
			return fmt.Errorf("step %s: no hint", s.ID)
		}
		if s.Done != nil {
			if err := s.Done.compile(); err != nil {
				return fmt.Errorf("step %s: %w", s.ID, err)
			}
		}
		for _, r := range s.After {
			if err := r.compile(); err != nil {
				return fmt.Errorf("step %s after: %w", s.ID, err)
			}
		}
		if (len(s.After) > 0) != (strings.TrimSpace(s.Waiting) != "") {
			return fmt.Errorf("step %s: after and waiting go together", s.ID)
		}
	}
	return nil
}

func checkSetup(list []Setup) error {
	for _, s := range list {
		if (s.Command == "") == (s.Input == "") {
			return fmt.Errorf("a setup entry needs a command or input, not both")
		}
		if s.Until != nil {
			if err := s.Until.compile(); err != nil {
				return fmt.Errorf("setup until: %w", err)
			}
		}
	}
	return nil
}

// Find returns the track with this id, or nil.
func (f *File) Find(id string) *Track {
	for _, t := range f.Tracks {
		if t.ID == id {
			return t
		}
	}
	return nil
}
