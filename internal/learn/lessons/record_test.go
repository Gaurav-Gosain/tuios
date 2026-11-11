package lessons

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recordFrom is the log.json a run of cmd/tuios-wasm/smoke-lessons.mjs wrote
// for a track. Naming it records that run as testdata/replay/TRACK.json, which
// TestReplayMatchesThePageEngine and TestNoStepFinishesBeforeItsLastKey then
// replay:
//
//	node cmd/tuios-wasm/smoke-lessons.mjs BUILD OUT cockpit
//	go test ./internal/learn/lessons -run TestRecordReplay -record OUT/cockpit/log.json
//
// The step completions and the keys lit after each press are what this engine
// made of the events, so the log is the run as it was, and the replay checks
// the engine still makes the same of it.
var recordFrom = flag.String("record", "", "record this smoke-lessons log.json as testdata/replay/TRACK.json")

func TestRecordReplay(t *testing.T) {
	if *recordFrom == "" {
		t.Skip("-record names the log to record")
	}
	raw, err := os.ReadFile(*recordFrom)
	if err != nil {
		t.Fatal(err)
	}
	var in struct {
		Track  string `json:"track"`
		Events []struct {
			T int64 `json:"t"`
			Event
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	f, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	track := f.Find(in.Track)
	if track == nil || len(in.Events) == 0 {
		t.Fatalf("no track %q or no events", in.Track)
	}
	type out struct {
		T     int64          `json:"t"`
		Type  string         `json:"type"`
		Data  map[string]any `json:"data,omitempty"`
		State map[string]any `json:"state,omitempty"`
	}
	line := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	t0 := in.Events[0].T
	base := time.UnixMilli(0)
	l := Start(track, base)
	var completions, keys, events []string
	press := 0
	for _, e := range in.Events {
		rel := e.T - t0
		now := base.Add(time.Duration(rel) * time.Millisecond)
		events = append(events, line(out{T: rel, Type: e.Type, Data: e.Data, State: e.State}))
		switch e.Type {
		case "lesson.showMe":
			l.MarkHinted()
			continue
		case "lesson.next", "lesson.skip":
			r := Clean
			if e.Type == "lesson.skip" {
				r = Skipped
			}
			before := l.Index
			l.Advance(r, now)
			completions = append(completions, line(map[string]any{"step": before, "press": press, "result": r}))
			continue
		}
		before := l.Index
		if l.Feed(e.Event, now) {
			completions = append(completions, line(map[string]any{"step": before, "press": press, "result": l.Results[before]}))
		}
		if e.Type == "key" {
			press++
			keys = append(keys, line(map[string]any{"press": press, "index": l.Index, "pressed": l.Pressed, "wrong": l.Wrong, "wrongMode": l.WrongMode, "hinted": l.Hinted}))
		}
	}
	if !l.Done() {
		t.Fatalf("the run ends on step %d of %d: record a run that finishes the track", l.Index, len(track.Steps))
	}
	body := "{\n\"track\":" + line(track.ID) + ",\n\"source\":" + line("cmd/tuios-wasm/smoke-lessons.mjs show me run, recorded by TestRecordReplay") +
		",\n\"completions\":[\n" + strings.Join(completions, ",\n") + "\n],\n\"keys\":[\n" + strings.Join(keys, ",\n") +
		"\n],\n\"events\":[\n" + strings.Join(events, ",\n") + "\n]\n}\n"
	if err := os.WriteFile(filepath.Join("testdata", "replay", track.ID+".json"), []byte(body), 0o644); err != nil { //nolint:gosec // test data
		t.Fatal(err)
	}
}
