package session

import (
	"path"
	"reflect"
	"strings"
	"testing"
)

const fuzzHome = "/home/fuzz"

func literal(v string) bool { return !strings.ContainsAny(v, `*?[\`) }

func FuzzSelector(f *testing.F) {
	for _, s := range []string{
		"harness:codex",
		"harness:claude state:idle,done session:api-fan-*",
		"needs:you",
		"cwd:~/src/tuios cwd:~ cwd:/ cwd:/tmp/",
		"host:local name:build group:fan/*",
		"task:t1,t2 stage:Doing,verify",
		"stage:plan2",
		"STATE:IDLE Harness:Codex",
		"name:[a-",
		"name:\\",
		"session:a,,b, ,c",
		":x",
		"x:",
		"cwd:///",
		"name:\xff\xfe",
		strings.Repeat("name:a ", 17),
		"name:" + strings.Repeat("a,", 17),
		strings.Repeat("x", 1025),
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, text string) {
		if (&Selector{}).Match(SelectorTarget{Name: "x"}) || (*Selector)(nil).Match(SelectorTarget{}) {
			t.Fatal("an empty selector matched")
		}
		sel, err := ParseSelector(text, fuzzHome)
		if err != nil {
			if err.Error() == "" {
				t.Fatalf("ParseSelector(%q) failed with an empty message", text)
			}
			return
		}

		if len(sel.String()) > selectorMaxLen {
			t.Fatalf("accepted a %d-byte selector", len(sel.String()))
		}
		if len(sel.terms) == 0 || len(sel.terms) > selectorMaxTerms {
			t.Fatalf("accepted %d terms from %q", len(sel.terms), text)
		}

		again, err := ParseSelector(sel.String(), fuzzHome)
		if err != nil {
			t.Fatalf("%q parsed, and its String %q does not: %v", text, sel.String(), err)
		}
		if !reflect.DeepEqual(again.terms, sel.terms) {
			t.Fatalf("%q and its String %q parse to different terms:\n%+v\n%+v",
				text, sel.String(), sel.terms, again.terms)
		}

		seen := map[string]bool{}
		once := true
		var target SelectorTarget
		for _, term := range sel.terms {
			if len(term.values) == 0 || len(term.values) > selectorMaxTerms {
				t.Fatalf("term %s accepted with %d values", term.key, len(term.values))
			}
			if seen[term.key] {
				once = false
			}
			seen[term.key] = true
			for _, v := range term.values {
				if term.key == SelectorState || term.key == SelectorStage || term.key == SelectorNeeds || term.key == SelectorCwd {
					continue
				}
				if _, err := path.Match(v, "a/b.c"); err != nil {
					t.Fatalf("value %q of %s was accepted as a glob and errors on a real name: %v",
						v, term.key, err)
				}
			}
			v := term.values[0]
			switch term.key {
			case SelectorHarness:
				target.Harness = v
			case SelectorState:
				target.State = v
			case SelectorNeeds:
				target.NeedsYou = true
			case SelectorSession:
				target.Session = v
			case SelectorGroup:
				target.Group = v
			case SelectorHost:
				target.Host = v
			case SelectorName:
				target.Name = v
			case SelectorTask:
				target.Tasks = []string{v}
			case SelectorStage:
				target.Stages = []string{v}
			case SelectorCwd:
				target.Cwd = v
				if v == "" {
					target.Cwd = "/"
				}
			}
			if term.key != SelectorState && term.key != SelectorStage && term.key != SelectorNeeds && term.key != SelectorCwd && !literal(v) {
				once = false
			}
		}
		if once && !sel.Match(target) {
			t.Fatalf("%q does not select the pane it names: %+v", text, target)
		}
	})
}
