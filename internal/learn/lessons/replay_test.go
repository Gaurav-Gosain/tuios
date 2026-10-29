package lessons

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// replayLog is one file under testdata/replay: the events a lesson engine
// was fed in a real run of a track, and what the page's TypeScript engine
// (tuios-docs lib/learn/engine.ts with its tracks/*.ts, before the lessons
// moved here) made of the same events.
type replayLog struct {
	Track       string `json:"track"`
	Completions []struct {
		Step   int    `json:"step"`
		Press  int    `json:"press"`
		Result Result `json:"result"`
	} `json:"completions"`
	Keys []struct {
		Press     int  `json:"press"`
		Index     int  `json:"index"`
		Pressed   int  `json:"pressed"`
		Wrong     int  `json:"wrong"`
		WrongMode bool `json:"wrongMode"`
		Hinted    bool `json:"hinted"`
	} `json:"keys"`
	Events []struct {
		T int64 `json:"t"`
		Event
	} `json:"events"`
}

// TestReplayMatchesThePageEngine is the compatibility contract between the
// lessons as data and the TypeScript lessons they replace: replayed through
// this engine, each recorded run finishes the same steps at the same key
// press, with the same result, and lights the same keycaps after every key.
//
// A step is compared by key press, not by event: the dock text checks the
// TypeScript tracks used ("Showkeys: ON", "Yanked") are events here, which
// arrive a few events later in the same key press.
func TestReplayMatchesThePageEngine(t *testing.T) {
	f, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	paths, _ := filepath.Glob("testdata/replay/*.json")
	if len(paths) == 0 {
		t.Fatal("no replay logs")
	}
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			var log replayLog
			if err := json.Unmarshal(b, &log); err != nil {
				t.Fatal(err)
			}
			track := f.Find(log.Track)
			if track == nil {
				t.Fatalf("no track %q", log.Track)
			}
			base := time.UnixMilli(0)
			l := Start(track, base)
			press, keyAt := 0, 0
			var completions []string
			for _, e := range log.Events {
				now := base.Add(time.Duration(e.T) * time.Millisecond)
				switch e.Type {
				case "lesson.showMe":
					l.MarkHinted()
					continue
				case "lesson.next", "lesson.skip":
					// The person read an explainer, or skipped the step.
					r := Clean
					if e.Type == "lesson.skip" {
						r = Skipped
					}
					before := l.Index
					l.Advance(r, now)
					completions = append(completions, verdict(before, press, r))
					continue
				}
				before := l.Index
				if l.Feed(e.Event, now) {
					completions = append(completions, verdict(before, press, l.Results[before]))
				}
				if e.Type != "key" {
					continue
				}
				press++
				if keyAt >= len(log.Keys) {
					t.Fatalf("press %d: the page engine saw fewer keys", press)
				}
				want := log.Keys[keyAt]
				keyAt++
				got := [5]any{l.Index, l.Pressed, l.Wrong, l.WrongMode, l.Hinted}
				if want := [5]any{want.Index, want.Pressed, want.Wrong, want.WrongMode, want.Hinted}; got != want {
					t.Errorf("press %d: step, pressed, wrong, wrong mode, hinted = %v, the page engine had %v", press, got, want)
				}
			}
			var want []string
			for _, c := range log.Completions {
				want = append(want, verdict(c.Step, c.Press, c.Result))
			}
			if len(want) == 0 {
				t.Fatal("the log completes no step")
			}
			if len(completions) != len(want) {
				t.Fatalf("completions\n got  %v\n want %v", completions, want)
			}
			for i := range want {
				if completions[i] != want[i] {
					t.Errorf("completion %d: got %s, the page engine had %s", i, completions[i], want[i])
				}
			}
		})
	}
}

func verdict(step, press int, r Result) string {
	b, _ := json.Marshal([]any{step, press, r})
	return string(b)
}
