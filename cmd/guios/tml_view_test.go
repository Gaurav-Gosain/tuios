package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/mygo/ui"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"tuios.dev/loom"
	"tuios.dev/tml"
)

// TestTMLDrawsWhatLoomDraws is a cross-implementation test: guios draws a
// TML document with its own widgets, and loom draws it into cells for the
// TUI. Both read the same tree through tuios.dev/tml, so every word loom
// shows for a document must show in guios too: a binding evaluated
// differently, an each repeated a different number of times, an if or a
// role read the other way, or an element guios skips, leaves a word out.
//
// The documents cover the elements of part 1 (layout, text and status).
func TestTMLDrawsWhatLoomDraws(t *testing.T) {
	docs := map[string]string{
		"build": `<tml version="1" title="Build">
  <state>{"pct": 40, "step": "Compiling", "ok": 412, "bad": 4}</state>
  <card title="Build" meta="main">
    <text>Step: <b>{state.step}</b> at <c role="accent">{state.pct}%</c></text>
    <progress value="{state.pct / 100}" label="Progress"/>
    <row gap="3">
      <stat label="passed" value="{state.ok}" role="success"/>
      <stat label="failed" value="{state.bad}" role="error"/>
    </row>
  </card>
</tml>`,
		"list": `<tml version="1" title="Agents">
  <state>{"agents": [{"name": "api", "state": "working"}, {"name": "docs", "state": "done"}, {"name": "tests", "state": "errored"}], "hide": false}</state>
  <col gap="1">
    <heading>Agents</heading>
    <status each="state.agents" as="a" state="{a.state}" label="{a.name}"/>
    <text if="{state.hide}">hidden words</text>
    <badge role="warning">three running</badge>
  </col>
</tml>`,
		"kv": `<tml version="1" title="Release">
  <state>{"tag": "v0.12.2", "date": "2026-11-09"}</state>
  <kv>
    <item k="Tag" v="{state.tag}"/>
    <item k="Date" v="{state.date}"/>
  </kv>
  <code lang="sh">git tag {state.tag}</code>
</tml>`,
	}
	// Words: runs of letters and digits, as loom's cells hold them with its
	// own marks (box edges, bars, glyphs) around them.
	word := regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9.%:-]*`)
	for name, src := range docs {
		t.Run(name, func(t *testing.T) {
			doc, _, err := tml.Check([]byte(src), nil)
			if err != nil {
				t.Fatalf("the document does not check: %v", err)
			}
			s, err := loom.New(doc, nil)
			if err != nil {
				t.Fatal(err)
			}
			cells := s.Render(100, 30).String()
			got := strings.Join(guiosTexts(t, doc), " ")
			for _, w := range word.FindAllString(cells, -1) {
				if !strings.Contains(got, strings.TrimRight(w, ".:")) {
					t.Errorf("ASSERTION: loom shows %q, guios does not.\nloom:\n%s\nguios: %s", w, cells, got)
				}
			}
			if strings.Contains(got, "hidden words") {
				t.Errorf("ASSERTION: guios drew a node whose if is false")
			}
		})
	}
}

// TestTMLPatchReachesTheDrawing: a state patch, as a tml-surface event
// carries it, changes what guios draws on the next frame.
func TestTMLPatchReachesTheDrawing(t *testing.T) {
	doc, _, err := tml.Check([]byte(`<tml version="1"><state>{"n": 1}</state><text>count {state.n}</text></tml>`), nil)
	if err != nil {
		t.Fatal(err)
	}
	a, v := tmlApp(t, doc)
	tester := ui.NewTester(func(c *ui.Context) {
		a.paneTML(c, paletteFor(c), &proto.Pane{Surface: "s1"})
	}, 600, 400)
	tester.Frame()
	if !tester.HasText("count 1") {
		t.Fatalf("the first frame does not show count 1: %v", tester.Texts())
	}
	a.tmlEvent(&proto.DaemonEvent{Type: "tml-surface", TML: &proto.TMLChange{
		Surface: "s1", Version: v.version + 1, Ops: []byte(`[{"op":"replace","path":"/n","value":7}]`),
	}})
	tester.Frame()
	if !tester.HasText("count 7") {
		t.Fatalf("ASSERTION: after the patch guios shows %v, want count 7", tester.Texts())
	}
}

// tmlApp is an App that holds doc as surface s1, as tml-get would leave it.
func tmlApp(t *testing.T, doc *tml.Node) (*App, *tmlView) {
	t.Helper()
	store, err := tml.StoreFor(doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	v := &tmlView{surface: "s1", doc: doc, store: store, view: loom.NewView(), version: 1, tmpl: map[string]*tml.Template{}}
	return &App{tmls: map[string]*tmlView{"s1": v}}, v
}

// guiosTexts draws doc as a TML pane in the headless tester and returns
// the texts it shows.
func guiosTexts(t *testing.T, doc *tml.Node) []string {
	t.Helper()
	a, _ := tmlApp(t, doc)
	tester := ui.NewTester(func(c *ui.Context) {
		a.paneTML(c, paletteFor(c), &proto.Pane{Surface: "s1"})
	}, 900, 700)
	tester.Frame()
	tester.Frame()
	return tester.Texts()
}
