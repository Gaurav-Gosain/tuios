// Package lessons holds the lessons of the tuios tour as data, and the engine
// that checks a reader's progress through them against the events
// internal/learn reports.
//
// lessons.json is the content: every track, step, key and hint, with each
// step's completion rule written as a small JSON matcher instead of a
// JavaScript function. The SSH tour (cmd/tuios-learn) reads it from here. The
// browser tour at tuios.dev/learn keeps its tracks in TypeScript today; the
// file was generated from them (tuios-docs lib/learn/tracks), and Matcher
// documents the grammar, one rule per matcher in lib/learn/matchers.ts, so
// the page can read this file too and stop keeping a second copy.
//
// A step may carry an "ssh" object with a title, note or hint for the SSH
// tour, where the shared words talk about a browser tab.
package lessons

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
)

//go:embed lessons.json
var lessonsJSON []byte

// File is lessons.json.
type File struct {
	Version int     `json:"version"`
	Tracks  []Track `json:"tracks"`
}

// Track is one chapter.
type Track struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Blurb    string   `json:"blurb"`
	Audience string   `json:"audience"`
	Minutes  int      `json:"minutes"`
	Next     []string `json:"next,omitempty"`
	Setup    []Setup  `json:"setup,omitempty"`
	Steps    []Step   `json:"steps"`
}

// Setup is one thing the tour does to set the scene: a learn command, or raw
// input typed as if by the reader.
type Setup struct {
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Input   string   `json:"input,omitempty"`
	// Wait is how long to pause after it, in milliseconds.
	Wait int `json:"wait,omitempty"`
}

// Key is one keycap on a step card: a chord such as "ctrl+b", or text to
// type, such as a shell command.
type Key struct {
	Chord string
	Text  string
}

// UnmarshalJSON reads "ctrl+b" or {"text": "neofetch"}.
func (k *Key) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		k.Chord = s
		return nil
	}
	var t struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b, &t); err != nil {
		return fmt.Errorf("a key is a chord string or {\"text\": ...}: %w", err)
	}
	k.Text = t.Text
	return nil
}

// Explainer is a step about something the demo cannot run. It completes when
// the reader says they read it.
type Explainer struct {
	Art      string `json:"art"`
	Body     string `json:"body"`
	Href     string `json:"href"`
	LinkText string `json:"linkText"`
}

// Override replaces a step's words on one surface, where the shared words
// talk about the other one (a browser tab, a click).
type Override struct {
	Title string `json:"title,omitempty"`
	Note  string `json:"note,omitempty"`
	Hint  string `json:"hint,omitempty"`
}

// Step is one thing to do.
type Step struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Note      string     `json:"note,omitempty"`
	Keys      []Key      `json:"keys"`
	Needs     string     `json:"needs,omitempty"`
	Setup     []Setup    `json:"setup,omitempty"`
	Done      *Matcher   `json:"done,omitempty"`
	Hint      string     `json:"hint"`
	ShowMe    string     `json:"showMe,omitempty"`
	Explainer *Explainer `json:"explainer,omitempty"`
	Learned   string     `json:"learned,omitempty"`
	// SSH is what the SSH tour says instead, where the shared text is about
	// the browser.
	SSH *Override `json:"ssh,omitempty"`
}

// ForSSH is the step with the SSH tour's words in place.
func (s Step) ForSSH() Step {
	if s.SSH == nil {
		return s
	}
	if s.SSH.Title != "" {
		s.Title = s.SSH.Title
	}
	if s.SSH.Note != "" {
		s.Note = s.SSH.Note
	}
	if s.SSH.Hint != "" {
		s.Hint = s.SSH.Hint
	}
	return s
}

// Load parses the embedded lessons and checks every matcher.
func Load() (*File, error) { return Parse(lessonsJSON) }

// Parse reads a lessons file and checks every matcher and setup command.
func Parse(b []byte) (*File, error) {
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Version != 1 {
		return nil, fmt.Errorf("lessons: version %d, want 1", f.Version)
	}
	ids := map[string]bool{}
	for _, t := range f.Tracks {
		if t.ID == "" || ids[t.ID] {
			return nil, fmt.Errorf("lessons: track id %q is empty or used twice", t.ID)
		}
		ids[t.ID] = true
		if err := checkSetup(t.Setup); err != nil {
			return nil, fmt.Errorf("track %s: %w", t.ID, err)
		}
		for _, s := range t.Steps {
			if err := checkSetup(s.Setup); err != nil {
				return nil, fmt.Errorf("track %s step %s: %w", t.ID, s.ID, err)
			}
			if s.Explainer == nil && s.Done == nil {
				return nil, fmt.Errorf("track %s step %s: no done matcher", t.ID, s.ID)
			}
			if s.Done != nil {
				if err := s.Done.check(); err != nil {
					return nil, fmt.Errorf("track %s step %s: %w", t.ID, s.ID, err)
				}
			}
		}
	}
	return &f, nil
}

// SetupCommands are the learn command names a setup may use. It matches
// learn.Commands; a test keeps the two in step.
var SetupCommands = []string{
	"action", "agent", "cascade", "celebrate", "closeWindow", "layout", "mode",
	"newWindow", "notify", "reset", "tape", "theme", "tiling", "type", "workspace",
}

func checkSetup(list []Setup) error {
	for _, s := range list {
		if s.Command == "" && s.Input == "" {
			return fmt.Errorf("setup entry with no command and no input")
		}
		if s.Command != "" && !slices.Contains(SetupCommands, s.Command) {
			return fmt.Errorf("unknown setup command %q", s.Command)
		}
	}
	return nil
}

// Find returns the track with this id, or nil.
func (f *File) Find(id string) *Track {
	for i := range f.Tracks {
		if f.Tracks[i].ID == id {
			return &f.Tracks[i]
		}
	}
	return nil
}
