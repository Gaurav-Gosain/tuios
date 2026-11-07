package hints

import (
	"slices"
	"testing"
)

// ticketRule is the rule the issue asked for: a ticket ID opens its page.
var ticketRule = LinkRuleSpec{
	Regex: `\b([A-Z][A-Z0-9_]+-[1-9][0-9]*)\b`,
	URL:   "https://jira.example.com/browse/$1",
}

func mustRules(t *testing.T, specs ...LinkRuleSpec) *LinkRules {
	t.Helper()
	rules, errs := CompileLinkRules(specs)
	if len(errs) > 0 {
		t.Fatalf("CompileLinkRules: %v", errs)
	}
	return rules
}

func TestLinksBuildTheTargetFromTheMatch(t *testing.T) {
	rules := mustRules(t, ticketRule)
	line := "fixed in PROJ-1234 and ABC-7"
	got := Links(line, rules)
	want := []Link{
		{Start: 9, End: 18, Target: "https://jira.example.com/browse/PROJ-1234", Rule: true},
		{Start: 23, End: 28, Target: "https://jira.example.com/browse/ABC-7", Rule: true},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Links(%q) = %+v, want %+v", line, got, want)
	}
}

func TestLinksExpandWholeMatchAndNamedGroups(t *testing.T) {
	rules := mustRules(t,
		LinkRuleSpec{Regex: `#(?P<num>\d+)`, URL: "https://git.example.com/issues/${num}"},
		LinkRuleSpec{Regex: `RFC\d+`, URL: "https://www.rfc-editor.org/info/$0"},
	)
	got := Links("see #42 and RFC9110", rules)
	want := []string{"https://git.example.com/issues/42", "https://www.rfc-editor.org/info/RFC9110"}
	var targets []string
	for _, l := range got {
		targets = append(targets, l.Target)
	}
	if !slices.Equal(targets, want) {
		t.Fatalf("targets = %q, want %q", targets, want)
	}
}

// A real URL wins when a rule overlaps it, so the ID inside an address stays
// part of the address.
func TestLinksARealURLWinsOverARule(t *testing.T) {
	rules := mustRules(t, ticketRule)
	line := "open https://jira.example.com/browse/PROJ-1 now"
	got := Links(line, rules)
	if len(got) != 1 || got[0].Rule || got[0].Target != "https://jira.example.com/browse/PROJ-1" {
		t.Fatalf("Links(%q) = %+v, want only the URL", line, got)
	}
}

// An earlier rule wins over a later one that overlaps it.
func TestLinksAnEarlierRuleWins(t *testing.T) {
	rules := mustRules(t,
		LinkRuleSpec{Regex: `PROJ-\d+`, URL: "https://first.example/$0"},
		LinkRuleSpec{Regex: `[A-Z]+-\d+`, URL: "https://second.example/$0"},
	)
	got := Links("PROJ-9", rules)
	if len(got) != 1 || got[0].Target != "https://first.example/PROJ-9" {
		t.Fatalf("Links = %+v, want the first rule's target", got)
	}
}

// With no rules the detector is the URL detector, unchanged.
func TestLinksWithoutRulesAreTheURLs(t *testing.T) {
	line := "a https://a.example/x b PROJ-1 c http://b.example"
	for _, rules := range []*LinkRules{nil, {}} {
		got := Links(line, rules)
		var ranges [][2]int
		for _, l := range got {
			if l.Rule || l.Target != line[l.Start:l.End] {
				t.Fatalf("a link without rules has a target of its own: %+v", l)
			}
			ranges = append(ranges, [2]int{l.Start, l.End})
		}
		if want := URLs(line); !slices.Equal(ranges, want) {
			t.Fatalf("Links ranges = %v, want URLs %v", ranges, want)
		}
	}
}

// A target that a click would refuse never becomes a link.
func TestLinksRefuseTargetsAClickWouldRefuse(t *testing.T) {
	rules := mustRules(t,
		LinkRuleSpec{Regex: `X-\d+`, URL: "javascript:alert($0)"},
		LinkRuleSpec{Regex: `Y-\d+`, URL: "jira.example.com/$0"},
		LinkRuleSpec{Regex: `Z (\S+ \S+)`, URL: "https://example.com/$1"},
	)
	if got := Links("X-1 Y-2 Z a b", rules); len(got) != 0 {
		t.Fatalf("Links = %+v, want none", got)
	}
}

// A rule can never build an address a bare URL could not be: a group that
// carries a byte or rune urlStop or urlStopRune would end a URL at makes no
// link.
func TestLinksRefuseTargetsABareURLCouldNotHold(t *testing.T) {
	rules := mustRules(t, LinkRuleSpec{Regex: `T-(\S+)`, URL: "https://example.com/$1"})
	for _, text := range []string{
		"T-a\"b", "T-a'b", "T-a<b", "T-a>b", "T-a`b", "T-a|b", "T-a^b",
		"T-a{b", "T-a}b", `T-a\b`, "T-a\x7fb", "T-a。b", "T-a，b",
	} {
		if got := Links(text, rules); len(got) != 0 {
			t.Errorf("Links(%q) = %+v, want none", text, got)
		}
	}
	if got := Links("T-a/b?c=d", rules); len(got) != 1 || got[0].Target != "https://example.com/a/b?c=d" {
		t.Fatalf("a clean group made no link: %+v", got)
	}
}

func TestUnknownGroupsFindsGroupsTheRegexDoesNotHave(t *testing.T) {
	for _, c := range []struct {
		regex, url string
		want       []string
	}{
		{`(\d+)`, "https://x/$1", nil},
		{`(\d+)`, "https://x/${1}_y", nil},
		{`(\d+)`, "https://x/$1_y", []string{"1_y"}},
		{`(\d+)`, "https://x/$2", []string{"2"}},
		{`(?P<num>\d+)`, "https://x/${num}/$num", nil},
		{`(?P<num>\d+)`, "https://x/$nmu", []string{"nmu"}},
		{`\d+`, "https://x/$0?$$", nil},
		{`\d+`, "https://x/${", nil},
	} {
		got, err := UnknownGroups(LinkRuleSpec{Regex: c.regex, URL: c.url})
		if err != nil {
			t.Fatalf("%q %q: %v", c.regex, c.url, err)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("UnknownGroups(%q, %q) = %q, want %q", c.regex, c.url, got, c.want)
		}
	}
}

// The first-byte skip may only ever skip a line the regex could not match.
// Every line here is tried both ways.
func TestMayHoldLinkSkipsOnlyLinesThatCannotMatch(t *testing.T) {
	exprs := []string{
		`\b([A-Z][A-Z0-9_]+-[1-9][0-9]*)\b`,
		`#(?P<num>\d+)`,
		`(?i)proj-\d+`,
		`(?:JIRA|GH)-\d+`,
		`\d{3,}`,
		`x*y`,   // can start with x or y
		`a?b`,   // can start with a or b: unknown, must not skip
		`ü-\d+`, // non-ASCII first rune: unknown, must not skip
	}
	lines := []string{
		"", "nothing here", "see PROJ-12 now", "issue #42", "proj-7 lower", "JIRA-1 and GH-2",
		"12345", "only y", "only b", "ü-3", "MIXED case-5", "plain text 99",
	}
	for _, expr := range exprs {
		rules := mustRules(t, LinkRuleSpec{Regex: expr, URL: "https://x/$0"})
		for _, line := range lines {
			matches := len(Links(line, rules)) > 0
			if matches && !MayHoldLink([]byte(line), rules) {
				t.Errorf("rule %q: MayHoldLink skipped %q, which has a link", expr, line)
			}
		}
	}
	plain := mustRules(t, LinkRuleSpec{Regex: `[A-Z]+-\d+`, URL: "https://x/$0"})
	if MayHoldLink([]byte("all lower case 123"), plain) {
		t.Error("a line with no capital was not skipped")
	}
	if !MayHoldLink([]byte("a https://x link"), nil) {
		t.Error("a URL line was skipped without rules")
	}
	if MayHoldLink([]byte("PROJ-1"), nil) {
		t.Error("without rules only a scheme can make a line hold a link")
	}
}

func TestLinkAtFindsTheLinkUnderAnOffset(t *testing.T) {
	rules := mustRules(t, ticketRule)
	line := "ticket PROJ-1234 done"
	got, ok := LinkAt(line, 10, rules)
	if !ok || got.Target != "https://jira.example.com/browse/PROJ-1234" {
		t.Fatalf("LinkAt = %+v, %v", got, ok)
	}
	if _, ok := LinkAt(line, 2, rules); ok {
		t.Fatal("LinkAt found a link on plain text")
	}
	if _, ok := LinkAt(line, 10, nil); ok {
		t.Fatal("LinkAt without rules found a ticket ID")
	}
}

func TestCompileLinkRulesSkipsBrokenRules(t *testing.T) {
	rules, errs := CompileLinkRules([]LinkRuleSpec{
		{Regex: `(`, URL: "https://x/$0"},
		{Regex: `a*`, URL: "https://x/$0"},
		{Regex: `A-\d+`, URL: ""},
		{Regex: "", URL: "https://x/$0"},
		ticketRule,
	})
	if len(errs) != 4 {
		t.Fatalf("errs = %v, want 4", errs)
	}
	if rules.Empty() || len(Links("PROJ-1", rules)) != 1 {
		t.Fatal("the usable rule was dropped with the broken ones")
	}
	if rules, _ := CompileLinkRules([]LinkRuleSpec{{Regex: `(`}}); rules != nil {
		t.Fatal("no usable rule should compile to nil")
	}
}

// Hints mode labels a rule match as a URL that opens the target, and copies
// the text on screen.
func TestMatcherLabelsRuleMatchesAsURLs(t *testing.T) {
	m, _ := New(Names(), nil)
	m.WithLinkRules(mustRules(t, ticketRule))
	line := "ticket PROJ-1234 at https://example.com/x"
	got := m.Find(line)
	var ticket, url *Match
	for i := range got {
		switch line[got[i].Start:got[i].End] {
		case "PROJ-1234":
			ticket = &got[i]
		case "https://example.com/x":
			url = &got[i]
		}
	}
	if ticket == nil || ticket.Kind != URL || ticket.Target != "https://jira.example.com/browse/PROJ-1234" {
		t.Fatalf("ticket match = %+v in %+v", ticket, got)
	}
	if url == nil || url.Target != "" {
		t.Fatalf("url match = %+v in %+v", url, got)
	}
}

// Without rules, hints mode is what it was.
func TestMatcherWithoutRulesIsUnchanged(t *testing.T) {
	line := "ticket PROJ-1234 at https://example.com/x 1234abc"
	plain, _ := New(Names(), nil)
	empty, _ := New(Names(), nil)
	empty.WithLinkRules(nil)
	if a, b := plain.Find(line), empty.Find(line); !slices.Equal(a, b) {
		t.Fatalf("Find changed without rules: %+v vs %+v", a, b)
	}
}

func TestOpenableScheme(t *testing.T) {
	for _, u := range []string{"https://a", "HTTP://a", "mailto:x@y", "ftp://a", "ftps://a"} {
		if !OpenableScheme(u) {
			t.Errorf("OpenableScheme(%q) = false", u)
		}
	}
	for _, u := range []string{"", "a", "file:///etc", "javascript:x", "ssh://a", ":x"} {
		if OpenableScheme(u) {
			t.Errorf("OpenableScheme(%q) = true", u)
		}
	}
}
