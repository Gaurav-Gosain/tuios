package session

import (
	"path"
	"reflect"
	"strings"
	"testing"
)

// Fuzzing the selector parser. A selector is typed by a person or written by
// an agent, and the writing verbs echo it back with a confirm token, so what
// the parser accepts has to mean the same thing when it comes back, and has to
// select the pane it plainly names.
//
// The ways this could fail, written down before the target:
//
//  1. ParseSelector accepts a text whose String does not parse back to the
//     same terms, so a selector echoed with its token selects a different set
//     on the second call.
//  2. A selector that names a pane's values literally does not select that
//     pane, because the parser normalised the value (case, a trailing slash,
//     a ~) and the matcher did not normalise the pane the same way.
//  3. A value the parser accepted as a glob errors inside path.Match against a
//     real name, and the term silently matches nothing.
//  4. The parser accepts more than its documented bounds: 1024 bytes, 16
//     terms, 16 values a term.
//  5. Match on an empty or nil selector selects something.
//  6. A panic on hostile text: invalid UTF-8, a lone colon, commas only.

const fuzzHome = "/home/fuzz"

// literal reports whether a glob value is also a plain string, so a pane
// holding exactly that string must match it.
func literal(v string) bool { return !strings.ContainsAny(v, `*?[\`) }

func FuzzSelector(f *testing.F) {
	for _, s := range []string{
		"harness:codex",
		"harness:claude state:idle,done session:api-fan-*",
		"needs:you",
		"cwd:~/src/tuios cwd:~ cwd:/ cwd:/tmp/",
		"host:local name:build group:fan/*",
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
				if term.key == SelectorState || term.key == SelectorNeeds || term.key == SelectorCwd {
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
			case SelectorCwd:
				// cwd:/// parses to "", which the matcher reads as the
				// root: every absolute directory is under it. A pane has
				// no empty directory to hold, so "/" stands in.
				target.Cwd = v
				if v == "" {
					target.Cwd = "/"
				}
			}
			if term.key != SelectorState && term.key != SelectorNeeds && term.key != SelectorCwd && !literal(v) {
				once = false
			}
		}
		// A pane holding exactly the first value of every term, where each
		// key appears once and no value is a pattern, is the pane the selector
		// names. A cwd term matches its own directory.
		if once && !sel.Match(target) {
			t.Fatalf("%q does not select the pane it names: %+v", text, target)
		}
	})
}
