package main

import (
	"encoding/json"
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
		"data": `<tml version="1" title="Tests">
  <state>{"tab": "fail", "rows": [{"pkg": "session", "ms": 2210}, {"pkg": "app", "ms": 840}, {"pkg": "vt", "ms": 120}],
    "tree": [{"label": "internal", "children": [{"label": "session"}, {"label": "app"}]}, {"label": "vt"}], "names": ["alpha", "beta"]}</state>
  <col gap="1">
    <tabs value="state.tab">
      <tab id="fail" title="Failures">
        <table rows="state.rows" sort="ms:desc">
          <column field="pkg" title="Package"/>
          <column field="ms" title="Time" format="value / 1000"/>
        </table>
      </tab>
      <tab id="other" title="Other"><text>other tab words</text></tab>
    </tabs>
    <tree items="state.tree" open="1"/>
    <list items="state.names" as="name"><text>name {name}</text></list>
    <toast role="success">Saved</toast>
    <split ratio="0.4"><text>left side</text><text>right side</text></split>
    <modal title="Confirm" open="true"><text>modal body</text></modal>
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
			// Case is the drawing's: loom writes a table's headers in
			// capitals, as a terminal has no bold to set them apart.
			lower := strings.ToLower(got)
			for _, w := range word.FindAllString(cells, -1) {
				if !strings.Contains(lower, strings.ToLower(strings.TrimRight(w, ".:"))) {
					t.Errorf("ASSERTION: loom shows %q, guios does not.\nloom:\n%s\nguios: %s", w, cells, got)
				}
			}
			if strings.Contains(got, "other tab words") {
				t.Errorf("ASSERTION: guios drew a tab that is not open")
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

// TestTMLInputsSendWhatLoomRuns: an input runs its actions with loom's
// RunActions, and what changed goes to the daemon in one tml-input, as the
// TUI sends it: the state ops, the emits, a close, and a pane() action only
// after the person said yes.
func TestTMLInputsSendWhatLoomRuns(t *testing.T) {
	cases := []struct {
		name, doc string
		do        func(t *testing.T, tester *ui.Tester)
		want      []string
	}{
		{
			name: "button",
			doc:  `<tml version="1"><state>{"n": 1}</state><button on-press="set('n', state.n + 1); emit('bumped', state.n)">Bump</button></tml>`,
			do:   func(t *testing.T, tt *ui.Tester) { click(t, tt, "Bump") },
			want: []string{`"ops":[{"op":"add","path":"/n","value":2}]`, `"events":[{"name":"bumped","value":2}]`},
		},
		{
			name: "checkbox",
			doc:  `<tml version="1"><state>{"on": false}</state><checkbox name="watch" bind="state.on" label="Watch" on-change="emit('watch', event.value)"/></tml>`,
			do:   func(t *testing.T, tt *ui.Tester) { click(t, tt, "Watch") },
			want: []string{`"ops":[{"op":"add","path":"/on","value":true}]`, `"events":[{"name":"watch","value":true}]`},
		},
		{
			name: "form",
			doc:  `<tml version="1"><form on-submit="emit('ask', form.q); close(form.q)"><input name="q" placeholder="Question"/><button submit="true">Send</button></form></tml>`,
			do: func(t *testing.T, tt *ui.Tester) {
				click(t, tt, "Question")
				tt.Type("why")
				tt.Frame()
				click(t, tt, "Send")
			},
			want: []string{`"events":[{"name":"ask","value":"why"}]`, `"close":true`, `"value":"why"`},
		},
		{
			name: "pane action waits for a yes",
			doc:  `<tml version="1"><button on-press="pane('new-window', {'command': ['go', 'test', './...']})">Test</button></tml>`,
			do: func(t *testing.T, tt *ui.Tester) {
				click(t, tt, "Test")
				if !tt.HasText("This surface wants to open a pane that runs: go test ./...") {
					t.Fatalf("ASSERTION: no question before the pane action: %v", tt.Texts())
				}
				click(t, tt, "Allow")
			},
			want: []string{`"actions":[{"params":{"command":["go","test","./..."]},"verb":"new-window"}]`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, _, err := tml.Check([]byte(tc.doc), nil)
			if err != nil {
				t.Fatalf("the document does not check: %v", err)
			}
			a, _ := tmlApp(t, doc)
			var sent []string
			a.tmlSent = func(p map[string]any) {
				b, _ := jsonMarshal(p)
				sent = append(sent, string(b))
			}
			tester := ui.NewTester(func(c *ui.Context) {
				a.paneTML(c, paletteFor(c), &proto.Pane{Surface: "s1"})
			}, 700, 500)
			tester.Frame()
			tc.do(t, tester)
			tester.Frame()
			all := strings.Join(sent, "\n")
			for _, w := range tc.want {
				if !strings.Contains(all, w) {
					t.Errorf("ASSERTION: the tml-input lacks %s. It sent:\n%s", w, all)
				}
			}
		})
	}
}

func click(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	tt.Frame()
	if err := tt.Click(text); err != nil {
		t.Fatalf("click %q: %v (texts %v)", text, err, tt.Texts())
	}
	tt.Frame()
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// TestTMLDataViewsActAsLoom: a header click sorts a table, a tab click opens
// the tab and sends its value, and a list pick runs on-select, each as in
// loom.
func TestTMLDataViewsActAsLoom(t *testing.T) {
	doc, _, err := tml.Check([]byte(`<tml version="1">
  <state>{"tab": "a", "rows": [{"pkg": "b-pkg", "ms": 1}, {"pkg": "a-pkg", "ms": 2}], "names": ["one", "two"]}</state>
  <col gap="1">
    <tabs value="state.tab">
      <tab id="a" title="First"><table rows="state.rows"><column field="pkg" title="Package"/><column field="ms" title="Time"/></table></tab>
      <tab id="b" title="Second"><text>second open</text></tab>
    </tabs>
    <list items="state.names" as="name" select="single" on-select="emit('picked', event.value)"><text>pick {name}</text></list>
  </col>
</tml>`), nil)
	if err != nil {
		t.Fatalf("the document does not check: %v", err)
	}
	a, v := tmlApp(t, doc)
	var sent []string
	a.tmlSent = func(p map[string]any) {
		b, _ := jsonMarshal(p)
		sent = append(sent, string(b))
	}
	tester := ui.NewTester(func(c *ui.Context) {
		a.paneTML(c, paletteFor(c), &proto.Pane{Surface: "s1"})
	}, 800, 600)
	tester.Frame()
	order := func() string {
		var out []string
		for _, s := range tester.Texts() {
			if strings.HasSuffix(s, "-pkg") {
				out = append(out, s)
			}
		}
		return strings.Join(out, ",")
	}
	if got := order(); got != "b-pkg,a-pkg" {
		t.Fatalf("the rows start as %s, want the state's order b-pkg,a-pkg", got)
	}
	click(t, tester, "Package")
	if got := order(); got != "a-pkg,b-pkg" {
		t.Errorf("ASSERTION: after a click on the header the rows are %s, want a-pkg,b-pkg", got)
	}
	click(t, tester, "Package ↑")
	if got := order(); got != "b-pkg,a-pkg" {
		t.Errorf("ASSERTION: after a second click the rows are %s, want b-pkg,a-pkg", got)
	}
	if v.view.Sort["/children/1/children/0/children/0/children/0"].Field == "" && len(v.view.Sort) == 0 {
		t.Errorf("ASSERTION: the sort is not in the view state")
	}
	click(t, tester, "Second")
	if !tester.HasText("second open") {
		t.Errorf("ASSERTION: the second tab did not open: %v", tester.Texts())
	}
	click(t, tester, "pick two")
	all := strings.Join(sent, "\n")
	for _, w := range []string{`"ops":[{"op":"add","path":"/tab","value":"b"}]`, `"events":[{"name":"picked","value":"two"}]`} {
		if !strings.Contains(all, w) {
			t.Errorf("ASSERTION: no tml-input with %s. It sent:\n%s", w, all)
		}
	}
}
