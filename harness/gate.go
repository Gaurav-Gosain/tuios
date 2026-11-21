package harness

import (
	"fmt"
	"regexp"
	"strings"
)

// Gate is the predicate set a rule tests its region against.
//
// The flat lists are what most rules need: every string in All present, at
// least one in Any present, none in Not present, every pattern in Regex
// matching, and no pattern in NotRegex matching. An empty list is satisfied, so
// a gate with only Any is an "any of these".
//
// The three nested lists are for the rules the flat lists cannot say. A rule
// often wants "this heading, and one of these three footers, and one of these
// four option lines", which is two any-of sets inside one conjunction, and a
// flat rule carries one. AllOf holds gates that must every one match, AnyOf
// gates of which at least one must match, and NoneOf gates of which none may
// match. A nested gate has the same fields as the rule's own, so a group reads
// the same wherever it sits. herdr's manifests are written this way, and the
// nesting is what lets its rules be ported as they are rather than flattened
// into several rules that only approximate them.
//
// A rule's region and case folding apply to every gate inside it.
type Gate struct {
	All      []string `toml:"all"`
	Any      []string `toml:"any"`
	Not      []string `toml:"not"`
	Regex    []string `toml:"regex"`
	NotRegex []string `toml:"not_regex"`
	AllOf    []Gate   `toml:"all_of"`
	AnyOf    []Gate   `toml:"any_of"`
	NoneOf   []Gate   `toml:"none_of"`

	regex      []*regexp.Regexp
	notRegex   []*regexp.Regexp
	substrings bool
}

const (
	maxGateDepth    = 8
	maxGatesPerFile = 512
	maxPredicates   = 1024
)

type gateBudget struct {
	gates, predicates int
}

func (g *Gate) positive() bool {
	return len(g.All)+len(g.Any)+len(g.Regex)+len(g.AllOf)+len(g.AnyOf) > 0
}

func (g *Gate) empty() bool {
	return !g.positive() && len(g.Not)+len(g.NotRegex)+len(g.NoneOf) == 0
}

func (g *Gate) compile(foldCase bool, depth int, b *gateBudget) error {
	if depth > maxGateDepth {
		return errorf("gates nest deeper than %d", maxGateDepth)
	}
	if b.gates++; b.gates > maxGatesPerFile {
		return errorf("more than %d gates in one manifest", maxGatesPerFile)
	}
	if b.predicates += len(g.All) + len(g.Any) + len(g.Not) + len(g.Regex) + len(g.NotRegex); b.predicates > maxPredicates {
		return errorf("more than %d predicates in one manifest", maxPredicates)
	}
	for _, list := range [][]string{g.All, g.Any, g.Not} {
		for _, s := range list {
			if strings.IndexByte(s, 0) >= 0 {
				return errorf("substring %q holds a NUL, which tuios never reads", s)
			}
		}
	}
	if foldCase {
		for _, list := range [][]string{g.All, g.Any, g.Not} {
			for i, s := range list {
				list[i] = strings.ToLower(s)
			}
		}
	}
	var err error
	if g.regex, err = compilePatterns(g.Regex); err != nil {
		return err
	}
	if g.notRegex, err = compilePatterns(g.NotRegex); err != nil {
		return err
	}
	for _, set := range []struct {
		name  string
		gates []Gate
	}{{"all_of", g.AllOf}, {"any_of", g.AnyOf}, {"none_of", g.NoneOf}} {
		for i := range set.gates {
			nested := &set.gates[i]
			if set.name == "none_of" {
				if nested.empty() {
					return errorf("%s group %d is empty", set.name, i)
				}
			} else if !nested.positive() {
				return errorf("%s group %d names nothing that must be present", set.name, i)
			}
			if err := nested.compile(foldCase, depth+1, b); err != nil {
				return errorf("%s group %d: %w", set.name, i, err)
			}
			g.substrings = g.substrings || nested.substrings
		}
	}
	g.substrings = g.substrings || len(g.All)+len(g.Any)+len(g.Not) > 0
	return nil
}

func (g *Gate) provesShape() bool {
	if len(g.Regex) > 0 {
		return true
	}
	for i := range g.AllOf {
		if g.AllOf[i].provesShape() {
			return true
		}
	}
	if len(g.AnyOf) == 0 {
		return false
	}
	for i := range g.AnyOf {
		if !g.AnyOf[i].provesShape() {
			return false
		}
	}
	return true
}

func (g *Gate) positiveStrings(out []string) []string {
	out = append(out, g.All...)
	out = append(out, g.Any...)
	for i := range g.AllOf {
		out = g.AllOf[i].positiveStrings(out)
	}
	for i := range g.AnyOf {
		out = g.AnyOf[i].positiveStrings(out)
	}
	return out
}

func (g *Gate) nestedPositiveStrings() []string {
	var out []string
	for i := range g.AllOf {
		out = g.AllOf[i].positiveStrings(out)
	}
	for i := range g.AnyOf {
		out = g.AnyOf[i].positiveStrings(out)
	}
	return out
}

func (g *Gate) match(hay, folded string, rep *RuleReport, contains func(hay, needle string) bool) bool {
	ok := true
	fail := func() bool {
		ok = false
		return rep == nil
	}
	for _, s := range g.All {
		if contains(folded, s) {
			continue
		}
		if fail() {
			return false
		}
		rep.Missing = append(rep.Missing, s)
	}
	if len(g.Any) > 0 {
		found := false
		for _, s := range g.Any {
			if contains(folded, s) {
				found = true
				break
			}
		}
		if !found {
			if fail() {
				return false
			}
			rep.NoneOf = g.Any
		}
	}
	for _, s := range g.Not {
		if !contains(folded, s) {
			continue
		}
		if fail() {
			return false
		}
		rep.Blocked = append(rep.Blocked, s)
	}
	for i, re := range g.regex {
		if re.MatchString(hay) {
			continue
		}
		if fail() {
			return false
		}
		rep.MissingRegex = append(rep.MissingRegex, g.Regex[i])
	}
	for i, re := range g.notRegex {
		if !re.MatchString(hay) {
			continue
		}
		if fail() {
			return false
		}
		rep.BlockedRegex = append(rep.BlockedRegex, g.NotRegex[i])
	}
	for i := range g.AllOf {
		if g.AllOf[i].match(hay, folded, nil, contains) {
			continue
		}
		if fail() {
			return false
		}
		rep.Groups = append(rep.Groups, fmt.Sprintf("all_of[%d] did not match", i))
	}
	if len(g.AnyOf) > 0 {
		found := false
		for i := range g.AnyOf {
			if g.AnyOf[i].match(hay, folded, nil, contains) {
				found = true
				break
			}
		}
		if !found {
			if fail() {
				return false
			}
			rep.Groups = append(rep.Groups, "no any_of group matched")
		}
	}
	for i := range g.NoneOf {
		if !g.NoneOf[i].match(hay, folded, nil, contains) {
			continue
		}
		if fail() {
			return false
		}
		rep.Groups = append(rep.Groups, fmt.Sprintf("none_of[%d] matched", i))
	}
	return ok
}

func checkRule(rl *ScreenRule, hay, folded string, rep *RuleReport, contains func(hay, needle string) bool) bool {
	if !rl.positive() {
		if rep != nil {
			rep.Empty = true
		}
		return false
	}
	return rl.match(hay, folded, rep, contains)
}
