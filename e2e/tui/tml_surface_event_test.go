package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A client that draws TML from verbs, such as guios, has no attach to take
// the surface pushes a TUI gets. The daemon publishes a tml-surface event
// with each push: the surface, its versions, the JSON Patch when one made
// the version, and the close. Such a client applies the patch to the
// version it holds, or reads the surface again with tml-get.
//
// How this could pass wrongly, written down first:
//   - The events could come from another surface. The test opens one, in a
//     session of its own, and checks the surface id of each event.
//   - A patch event could carry ops that are not the patch. The test sends
//     one patch and compares the ops it gets back.
//   - The event could leak to every subscriber and flood it. The test also
//     subscribes without types and checks that no tml-surface reaches it.
func TestTMLSurfaceEventFollowsEachChange(t *testing.T) {
	base := t.TempDir()
	const session = "e2e-tml-surface"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	killDaemon(t, base)
	read := subscribeLines(t, base, "-s", session, "--types", "tml-surface")
	all := subscribeLines(t, base, "-s", session)

	doc := writeTMLDoc(t, base, "count.tml", `<tml version="1" title="Count"><state>{"n": 1}</state><text>n is {state.n}</text></tml>`)
	out, err := tuiosCLI(t, base, "tml", "show", doc, "--pane", "-s", session)
	if err != nil {
		t.Fatalf("tml show: %v\n%s", err, out)
	}
	// It prints the surface id and the window id.
	last := strings.TrimSpace(out[strings.LastIndex(strings.TrimSpace(out), "\n")+1:])
	surface := strings.Fields(last)[0]

	type surfaceEvent struct {
		Type       string `json:"type"`
		Window     string `json:"window"`
		TMLSurface *struct {
			Surface    string          `json:"surface"`
			Version    uint64          `json:"version"`
			DocVersion uint64          `json:"doc_version"`
			Ops        json.RawMessage `json:"ops"`
			Closed     bool            `json:"closed"`
		} `json:"tml_surface"`
	}
	events := func() []surfaceEvent {
		var evs []surfaceEvent
		for _, l := range read() {
			var ev surfaceEvent
			if json.Unmarshal([]byte(l), &ev) == nil && ev.Type == "tml-surface" && ev.TMLSurface != nil {
				evs = append(evs, ev)
			}
		}
		return evs
	}
	wait := func(what string, ok func(surfaceEvent) bool) surfaceEvent {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for {
			for _, ev := range events() {
				if ok(ev) {
					if ev.TMLSurface.Surface != surface {
						t.Fatalf("ASSERTION: %s: the event names surface %s, want %s", what, ev.TMLSurface.Surface, surface)
					}
					return ev
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("ASSERTION: no tml-surface event for %s. The events:\n%s", what, strings.Join(read(), "\n"))
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	opened := wait("the open", func(ev surfaceEvent) bool { return !ev.TMLSurface.Closed && len(ev.TMLSurface.Ops) == 0 })
	if opened.Window == "" {
		t.Fatalf("ASSERTION: the open event names no window: %+v", opened)
	}

	const patch = `[{"op":"replace","path":"/n","value":2}]`
	if out, err := tuiosCLI(t, base, "tml", "patch", surface, patch); err != nil {
		t.Fatalf("tml patch: %v\n%s", err, out)
	}
	patched := wait("the patch", func(ev surfaceEvent) bool { return len(ev.TMLSurface.Ops) > 0 })
	if patched.TMLSurface.Version != opened.TMLSurface.Version+1 {
		t.Fatalf("ASSERTION: the patch made version %d, want %d", patched.TMLSurface.Version, opened.TMLSurface.Version+1)
	}
	var got, want any
	_ = json.Unmarshal(patched.TMLSurface.Ops, &got)
	_ = json.Unmarshal([]byte(patch), &want)
	if gb, _ := json.Marshal(got); string(gb) != mustJSON(want) {
		t.Fatalf("ASSERTION: the event's ops are %s, want %s", patched.TMLSurface.Ops, patch)
	}
	if patched.TMLSurface.DocVersion != opened.TMLSurface.DocVersion {
		t.Fatalf("ASSERTION: a state patch moved doc_version from %d to %d", opened.TMLSurface.DocVersion, patched.TMLSurface.DocVersion)
	}

	doc2 := writeTMLDoc(t, base, "count2.tml", `<tml version="1" title="Count"><state>{"n": 3}</state><heading>n is {state.n}</heading></tml>`)
	if out, err := tuiosCLI(t, base, "tml", "replace", surface, doc2); err != nil {
		t.Fatalf("tml replace: %v\n%s", err, out)
	}
	wait("the replace", func(ev surfaceEvent) bool { return ev.TMLSurface.DocVersion > opened.TMLSurface.DocVersion })

	if out, err := tuiosCLI(t, base, "tml", "close", surface); err != nil {
		t.Fatalf("tml close: %v\n%s", err, out)
	}
	wait("the close", func(ev surfaceEvent) bool { return ev.TMLSurface.Closed })

	time.Sleep(insertGuard)
	for _, l := range all() {
		if strings.Contains(l, `"tml-surface"`) {
			t.Fatalf("ASSERTION: a subscriber that named no types got a tml-surface event: %s", l)
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
