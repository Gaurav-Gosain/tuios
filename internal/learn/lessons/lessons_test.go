package lessons

import (
	"slices"
	"testing"
	"time"
)

func TestLessonsLoad(t *testing.T) {
	f, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Tracks) < 9 {
		t.Fatalf("%d tracks, want the nine of tuios.dev/learn", len(f.Tracks))
	}
	for _, tr := range f.Tracks {
		for _, s := range tr.Steps {
			if len(s.Keys) == 0 {
				t.Errorf("%s/%s has no keys", tr.ID, s.ID)
			}
			if s.Explainer == nil && s.Hint == "" {
				t.Errorf("%s/%s has no hint", tr.ID, s.ID)
			}
		}
	}
}

func TestParseRejectsBadMatchers(t *testing.T) {
	for _, bad := range []string{
		`{"version":1,"tracks":[{"id":"a","steps":[{"id":"s","keys":["n"],"hint":"h","done":{}}]}]}`,
		`{"version":1,"tracks":[{"id":"a","steps":[{"id":"s","keys":["n"],"hint":"h","done":{"on":"x","opened":"y"}}]}]}`,
		`{"version":1,"tracks":[{"id":"a","steps":[{"id":"s","keys":["n"],"hint":"h","done":{"ranLine":"("}}]}]}`,
		`{"version":1,"tracks":[{"id":"a","steps":[{"id":"s","keys":["n"],"hint":"h","done":{"via":["x"]}}]}]}`,
		`{"version":1,"tracks":[{"id":"a","setup":[{"command":"rm"}],"steps":[]}]}`,
		`{"version":2,"tracks":[]}`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestMatchers(t *testing.T) {
	yes := true
	ev := func(typ string, data map[string]any) Event { return Event{Type: typ, Data: data} }
	cases := []struct {
		m    Matcher
		e    Event
		want bool
	}{
		{Matcher{On: "window.open"}, ev("window.open", nil), true},
		{Matcher{On: "window.open", Where: map[string]any{"data.workspace": 2.0}}, ev("window.open", map[string]any{"workspace": 2}), true},
		{Matcher{On: "window.open", Where: map[string]any{"data.workspace": 2.0}}, ev("window.open", map[string]any{"workspace": 1}), false},
		{Matcher{On: "action", Where: map[string]any{"data.name": map[string]any{"prefix": "preselect_"}}}, ev("action", map[string]any{"name": "preselect_down"}), true},
		{Matcher{On: "window.focus", Where: map[string]any{"data.to": map[string]any{"truthy": true}}}, ev("window.focus", map[string]any{"to": ""}), false},
		{Matcher{Changed: "mode", To: "terminal"}, ev("mode", map[string]any{"to": "terminal"}), true},
		{Matcher{Ran: "neofetch"}, ev("shell.command", map[string]any{"command": "neofetch", "exitCode": 127}), false},
		{Matcher{Zoomed: &yes}, ev("window.zoom", map[string]any{"zoomed": true}), true},
	}
	for i, c := range cases {
		if err := c.m.check(); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if got := c.m.Match(c.e, &Ctx{Mem: map[string]int{}}); got != c.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}

	// window(data.to).agent looks the window up in the state.
	m := Matcher{On: "window.focus", Where: map[string]any{"window(data.to).agent": map[string]any{"truthy": true}}}
	e := Event{Type: "window.focus", Data: map[string]any{"to": "w2"}, State: map[string]any{
		"windowList": []any{map[string]any{"id": "w1", "agent": ""}, map[string]any{"id": "w2", "agent": "needs_input"}},
	}}
	if !m.Match(e, &Ctx{Mem: map[string]int{}}) {
		t.Error("window lookup did not match")
	}
}

func TestSeqAndVia(t *testing.T) {
	m := Matcher{Seq: []*Matcher{{Opened: "help"}, {Closed: "help"}}}
	if err := m.check(); err != nil {
		t.Fatal(err)
	}
	ctx := &Ctx{Mem: map[string]int{}}
	if m.Match(Event{Type: "overlay.close", Data: map[string]any{"name": "help"}}, ctx) {
		t.Fatal("closed before opened matched")
	}
	m.Match(Event{Type: "overlay.open", Data: map[string]any{"name": "help"}}, ctx)
	if !m.Match(Event{Type: "overlay.close", Data: map[string]any{"name": "help"}}, ctx) {
		t.Fatal("open then close did not match")
	}

	v := Matcher{Via: []string{"prefix_split_vertical"}, Then: &Matcher{On: "window.open"}}
	if v.Match(Event{Type: "window.open"}, &Ctx{LastAction: "prefix_new_window", Mem: map[string]int{}}) {
		t.Error("via matched the wrong action")
	}
	if !v.Match(Event{Type: "window.open"}, &Ctx{LastAction: "prefix_split_vertical", Mem: map[string]int{}}) {
		t.Error("via missed its action")
	}
}

func TestLessonKeysAndHints(t *testing.T) {
	f, _ := Load()
	now := time.Now()
	l := Start(&f.Tracks[0], now)
	key := func(k, mode string) Event {
		return Event{Type: "key", Data: map[string]any{"key": k, "mode": mode}}
	}
	// The first step wants n in window mode. n in typing mode is the wrong
	// mode, and shows the hint at once.
	l.Feed(key("n", "terminal"), now)
	if !l.WrongMode || l.HintLevel(now) != 1 {
		t.Fatalf("wrong mode not flagged: %+v", l)
	}
	if !l.Feed(Event{Type: "window.open", Data: map[string]any{}}, now) {
		t.Fatal("window.open did not finish the first step")
	}
	if l.Index != 1 || l.Results[0] != Hinted {
		t.Fatalf("index %d results %v", l.Index, l.Results)
	}
	if lvl := l.HintLevel(now.Add(ShowMeAfter)); lvl != 2 {
		t.Fatalf("hint level after the wait is %d", lvl)
	}
}

func TestKeyBytes(t *testing.T) {
	for chord, want := range map[string]string{
		"ctrl+b": "\x02", "alt+j": "\x1bj", "enter": "\r", "esc": "\x1b", "|": "|", "shift+tab": "\x1b[Z", "R": "R",
	} {
		if got := KeyBytes(Key{Chord: chord}); got != want {
			t.Errorf("%s: %q, want %q", chord, got, want)
		}
	}
	if !SameChord("shift+|", "|") || !SameChord("escape", "esc") || SameChord("n", "m") {
		t.Error("SameChord")
	}
	if !slices.Contains(SetupCommands, "celebrate") {
		t.Error("celebrate missing")
	}
}
