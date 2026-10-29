package lint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/learn"
	"github.com/Gaurav-Gosain/tuios/internal/learn/lessons"
)

// TestLessonsMatchTheApp checks the lessons in internal/learn/lessons against
// the app they teach. A lesson is data the compiler never sees, so a renamed
// action, overlay or event would leave a step nobody can finish. This fails
// the gate in the change that renames it.
//
// It checks: every action a step waits on or a setup runs is a keybinding
// action; every event type, overlay name and setting name is one the page
// contract sends; every setup command is one internal/learn runs; every key
// chord parses; and the words follow the lesson style.
func TestLessonsMatchTheApp(t *testing.T) {
	f, err := lessons.Load()
	if err != nil {
		t.Fatalf("the lessons do not load: %v", err)
	}
	actions := knownActions()
	events := set(learn.EventTypes)
	overlays := set(learn.OverlayNames)
	settings := set(learn.SettingNames())
	commands := set(learn.Commands)
	groups := map[string]bool{}
	for _, g := range f.Groups {
		groups[g.ID] = true
	}

	for _, tr := range f.Tracks {
		at := func(step string) string {
			if step == "" {
				return tr.ID
			}
			return tr.ID + "/" + step
		}
		if !groups[tr.Group] {
			t.Errorf("%s: group %q is not in tracks.json", tr.ID, tr.Group)
		}
		for _, s := range tr.Surfaces {
			if s != lessons.SurfaceWeb && s != lessons.SurfaceTerminal {
				t.Errorf("%s: surface %q", tr.ID, s)
			}
		}
		for _, n := range tr.Next {
			if f.Find(n) == nil {
				t.Errorf("%s: next names track %q, which does not exist", tr.ID, n)
			}
		}
		if n := len(tr.Steps); n < 5 || n > 12 {
			t.Errorf("%s: %d steps, want 5 to 12: long enough to build a workflow, short enough to finish", tr.ID, n)
		}
		checkRule := func(where string, r *lessons.Rule) {
			r.Walk(func(r *lessons.Rule) {
				if r.On != "" && !events[r.On] {
					t.Errorf("%s: waits on event %q, which the page contract does not send", where, r.On)
				}
				if r.Changed != "" && !events[r.Changed] {
					t.Errorf("%s: changed %q is not an event type", where, r.Changed)
				}
				for _, name := range []string{r.Opened, r.Closed} {
					if name != "" && !overlays[name] {
						t.Errorf("%s: overlay %q is not one the page contract names", where, name)
					}
				}
				if r.Setting != "" && !settings[r.Setting] {
					t.Errorf("%s: setting %q is not one a setting event names", where, r.Setting)
				}
				for _, a := range r.Via {
					if a.Prefix != "" {
						if !hasPrefixed(actions, a.Prefix) {
							t.Errorf("%s: no action starts with %q", where, a.Prefix)
						}
					} else if _, ok := actions[a.Name]; !ok {
						t.Errorf("%s: via names action %q, which is not a keybinding action", where, a.Name)
					}
				}
				name, _ := r.Where["data.name"].(string)
				switch r.On {
				case learn.EventAction:
					if _, ok := actions[name]; name != "" && !ok {
						t.Errorf("%s: action %q is not a keybinding action", where, name)
					}
				case learn.EventOverlayOpen, learn.EventOverlayClose:
					if name != "" && !overlays[name] {
						t.Errorf("%s: overlay %q is not one the page contract names", where, name)
					}
				case learn.EventSetting:
					if name != "" && !settings[name] {
						t.Errorf("%s: setting %q is not one a setting event names", where, name)
					}
				}
			})
		}
		checkSetup := func(where string, list []lessons.Setup) {
			for _, s := range list {
				if s.Command != "" && !commands[s.Command] {
					t.Errorf("%s: setup command %q is not one internal/learn runs", where, s.Command)
				}
				if s.Command == "action" {
					if len(s.Args) == 0 {
						t.Errorf("%s: an action setup names no action", where)
					} else if _, ok := actions[s.Args[0]]; !ok {
						t.Errorf("%s: setup runs action %q, which is not a keybinding action", where, s.Args[0])
					}
				}
				if s.Until != nil {
					checkRule(where+" setup until", s.Until)
				}
			}
		}
		checkSetup(at(""), tr.Setup)
		for _, s := range tr.Steps {
			where := at(s.ID)
			checkSetup(where, s.Setup)
			if s.Done != nil {
				checkRule(where, s.Done)
			}
			for _, r := range s.After {
				checkRule(where+" after", r)
			}
			for _, k := range s.Keys {
				if k.Text == "" {
					if err := lessons.CheckChord(k.Chord); err != nil {
						t.Errorf("%s: %v", where, err)
					}
				}
			}
			if s.Needs != "" && s.Needs != "window" && s.Needs != "terminal" {
				t.Errorf("%s: needs %q, want window or terminal", where, s.Needs)
			}
			if n := len(strings.Fields(s.Title)); n > 6 {
				t.Errorf("%s: the title has %d words, want 6 or fewer", where, n)
			}
			if s.Web == nil && s.Terminal == nil {
				continue
			}
			for _, o := range []*lessons.Override{s.Web, s.Terminal} {
				if o != nil && o.Title == "" && o.Note == "" && o.Hint == "" && o.Waiting == "" {
					t.Errorf("%s: an empty surface override", where)
				}
			}
		}
	}

	// The words: no em or en dashes, and no hyphens standing in for them.
	dash := regexp.MustCompile(`[–—]| - | -- `)
	for _, tr := range f.Tracks {
		texts := []string{tr.Title, tr.Blurb, tr.Audience}
		for _, s := range tr.Steps {
			texts = append(texts, s.Title, s.Note, s.Hint, s.Learned, s.Waiting)
			if s.Explainer != nil {
				texts = append(texts, s.Explainer.Body)
			}
			for _, o := range []*lessons.Override{s.Web, s.Terminal} {
				if o != nil {
					texts = append(texts, o.Title, o.Note, o.Hint, o.Waiting)
				}
			}
		}
		for _, text := range texts {
			if dash.MatchString(text) {
				t.Errorf("%s: %q has a dash; write two sentences", tr.ID, text)
			}
		}
	}

	// The coach's nudges are the reasons the engine gives.
	for _, n := range []string{"idle", "wrongKeys", "wrongModeWindow", "wrongModeTerminal", "showMe", "altBlocked", "noWindow"} {
		if len(f.Coach.Nudges[n]) == 0 {
			t.Errorf("tracks.json: the coach has no lines for the %q nudge", n)
		}
	}
}

// TestLessonContractListsAreWhole checks that learn.EventTypes and
// learn.OverlayNames hold every event and overlay constant internal/learn
// declares, so the lesson lint above reads the whole contract.
func TestLessonContractListsAreWhole(t *testing.T) {
	root := repoRoot(t)
	consts := map[string]string{}
	fset := token.NewFileSet()
	paths, _ := filepath.Glob(filepath.Join(root, "internal/learn/*.go"))
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, id := range vs.Names {
				if i < len(vs.Values) {
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						v, _ := strconv.Unquote(lit.Value)
						consts[id.Name] = v
					}
				}
			}
			return true
		})
	}
	events, overlays := set(learn.EventTypes), set(learn.OverlayNames)
	for name, v := range consts {
		switch {
		case strings.HasPrefix(name, "EventLesson"):
		case strings.HasPrefix(name, "Event") && !events[v]:
			t.Errorf("learn.%s (%q) is missing from learn.EventTypes", name, v)
		case strings.HasPrefix(name, "Overlay") && !overlays[v]:
			t.Errorf("learn.%s (%q) is missing from learn.OverlayNames", name, v)
		}
	}
	if len(consts) == 0 {
		t.Fatal("read no constants from internal/learn")
	}
}

// knownActions is every keybinding action: the described ones, and every
// action the shipped keybindings bind, such as the prefix menus' numbered
// rows.
func knownActions() map[string]string {
	out := maps.Clone(config.ActionDescriptions)
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Struct:
			for i := range v.NumField() {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
			}
		case reflect.Map:
			if v.Type().Key().Kind() != reflect.String {
				return
			}
			for _, k := range v.MapKeys() {
				if _, ok := out[k.String()]; !ok {
					out[k.String()] = ""
				}
				walk(v.MapIndex(k))
			}
		}
	}
	walk(reflect.ValueOf(config.DefaultConfig().Keybindings))
	return out
}

func set(list []string) map[string]bool {
	out := make(map[string]bool, len(list))
	for _, s := range list {
		out[s] = true
	}
	return out
}

func hasPrefixed(m map[string]string, prefix string) bool {
	return slices.ContainsFunc(keys(m), func(k string) bool { return strings.HasPrefix(k, prefix) })
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
