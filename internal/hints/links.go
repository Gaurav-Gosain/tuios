package hints

import (
	"bytes"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// Link rules turn plain text that is not a URL, such as a ticket ID, into a
// link whose target is built from the match, the way WezTerm's
// hyperlink_rules do. They plug into the one detector the pointer, the OSC 8
// output and hints mode share (urls.go), so a rule match is hovered, clicked,
// written to the outer terminal and labelled exactly like a bare URL. What
// differs is that the text on screen and the address it opens are not the
// same string, so a detected link carries its Target.

// LinkRuleSpec is one rule as the config file writes it: a regular expression
// in Go syntax, and a URL in which $0 is the whole match and $1, $2, ... (or
// ${name}) are its groups.
type LinkRuleSpec struct {
	Regex string
	URL   string
}

// linkRule is one compiled rule.
type linkRule struct {
	re  *regexp.Regexp
	url string
	// first is the set of bytes a match can start with, or nil when that is
	// not known (a match can start with any byte, or with a rune past ASCII).
	// A line holding none of them cannot match, which lets the frame skip
	// most lines without running the regex.
	first *[256]bool
}

// firstBytes works out the bytes a match of expr can start with. It is
// conservative: nil means "any", and a set is only returned when every match
// must begin with one of its bytes.
func firstBytes(expr string) *[256]bool {
	re, err := syntax.Parse(expr, syntax.Perl)
	if err != nil {
		return nil
	}
	var set [256]bool
	if !addFirst(re.Simplify(), &set) {
		return nil
	}
	return &set
}

// addFirst adds to set the bytes a match of re can start with, and reports
// whether it could tell. A node that can match empty text, or start with a
// rune past ASCII, makes the answer unknown.
func addFirst(re *syntax.Regexp, set *[256]bool) bool {
	switch re.Op {
	case syntax.OpLiteral:
		if len(re.Rune) == 0 {
			return false
		}
		return addRune(re.Rune[0], re.Flags&syntax.FoldCase != 0, set)
	case syntax.OpCharClass:
		for i := 0; i+1 < len(re.Rune); i += 2 {
			lo, hi := re.Rune[i], re.Rune[i+1]
			if hi >= utf8.RuneSelf {
				return false
			}
			for r := lo; r <= hi; r++ {
				set[r] = true
			}
		}
		return len(re.Rune) > 0
	case syntax.OpCapture, syntax.OpPlus:
		return addFirst(re.Sub[0], set)
	case syntax.OpRepeat:
		return re.Min >= 1 && addFirst(re.Sub[0], set)
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if !addFirst(sub, set) {
				return false
			}
		}
		return true
	case syntax.OpConcat:
		// Zero-width assertions in front (\b, ^, $) do not consume a byte, so
		// the first byte comes from what follows them.
		for _, sub := range re.Sub {
			switch sub.Op {
			case syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText,
				syntax.OpWordBoundary, syntax.OpNoWordBoundary, syntax.OpEmptyMatch:
				continue
			}
			return addFirst(sub, set)
		}
		return false
	}
	return false
}

// addRune adds r (and its other case, under case folding) to set, and reports
// whether r is ASCII.
func addRune(r rune, fold bool, set *[256]bool) bool {
	if r >= utf8.RuneSelf {
		return false
	}
	set[r] = true
	if fold {
		if r >= 'a' && r <= 'z' {
			set[r-'a'+'A'] = true
		} else if r >= 'A' && r <= 'Z' {
			set[r-'A'+'a'] = true
		}
	}
	return true
}

// holdsAny reports whether line holds a byte of set.
func holdsAny(line []byte, set *[256]bool) bool {
	for _, c := range line {
		if set[c] {
			return true
		}
	}
	return false
}

// LinkRules is the compiled set of rules in force. A nil or empty LinkRules
// finds nothing, so a config without rules costs the detector nothing.
type LinkRules struct {
	rules []linkRule
}

// CompileLinkRule compiles one rule and reports why it cannot be used.
func CompileLinkRule(spec LinkRuleSpec) (*regexp.Regexp, error) {
	if strings.TrimSpace(spec.Regex) == "" {
		return nil, errfmt.Errorf("link rule has no regex")
	}
	if strings.TrimSpace(spec.URL) == "" {
		return nil, errfmt.Errorf("link rule %q has no url", spec.Regex)
	}
	re, err := regexp.Compile(spec.Regex)
	if err != nil {
		return nil, err
	}
	if re.MatchString("") {
		return nil, errfmt.Errorf("link rule %q matches empty text", spec.Regex)
	}
	return re, nil
}

// UnknownGroups returns the groups the url template of spec names that its
// regex does not have, in the order the template names them. Go's template
// syntax reads the longest run of letters, digits and underscores after a $,
// so "$1_y" names a group called "1_y", not group 1 and then "_y": a rule
// written that way would quietly expand to nothing. ${1} is the way to write
// it.
func UnknownGroups(spec LinkRuleSpec) ([]string, error) {
	re, err := CompileLinkRule(spec)
	if err != nil {
		return nil, err
	}
	var unknown []string
	for _, name := range templateGroups(spec.URL) {
		if !hasGroup(re, name) && !slices.Contains(unknown, name) {
			unknown = append(unknown, name)
		}
	}
	return unknown, nil
}

// templateGroups lists the group names a regexp Expand template refers to, as
// regexp.Expand reads them: $name or ${name}, where name is a run of letters,
// digits and underscores, and $$ is a literal dollar.
func templateGroups(tmpl string) []string {
	var names []string
	for {
		i := strings.IndexByte(tmpl, '$')
		if i < 0 {
			return names
		}
		tmpl = tmpl[i+1:]
		if tmpl == "" {
			return names
		}
		if tmpl[0] == '$' {
			tmpl = tmpl[1:]
			continue
		}
		brace := tmpl[0] == '{'
		rest := tmpl
		if brace {
			rest = tmpl[1:]
		}
		n := 0
		for n < len(rest) && groupNameByte(rest[n]) {
			n++
		}
		if n == 0 || (brace && (n >= len(rest) || rest[n] != '}')) {
			// Not a reference; Expand writes it out as it is.
			continue
		}
		names = append(names, rest[:n])
		tmpl = rest[n:]
	}
}

func groupNameByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// hasGroup reports whether name is a group of re, as Expand resolves it: a
// number is an index, anything else a group name.
func hasGroup(re *regexp.Regexp, name string) bool {
	if n, ok := groupIndex(name); ok {
		return n <= re.NumSubexp()
	}
	return re.SubexpIndex(name) >= 0
}

// groupIndex reads name as a group number, the way Expand does: all digits.
func groupIndex(name string) (int, bool) {
	n := 0
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		if n > 1e8 {
			return 0, true
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// CompileLinkRules compiles the rules in order. A rule that does not compile is
// skipped and reported; the rest still work. It returns nil when no rule is
// usable, which every caller reads as "no rules".
func CompileLinkRules(specs []LinkRuleSpec) (*LinkRules, []error) {
	var rules []linkRule
	var errs []error
	for _, spec := range specs {
		re, err := CompileLinkRule(spec)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		rules = append(rules, linkRule{re: re, url: spec.URL, first: firstBytes(spec.Regex)})
	}
	if len(rules) == 0 {
		return nil, errs
	}
	return &LinkRules{rules: rules}, errs
}

// Empty reports whether there are no rules to run.
func (r *LinkRules) Empty() bool { return r == nil || len(r.rules) == 0 }

// Link is one link found in a line of text. Start and End are the byte range
// shown on screen; Target is the address it opens. For a bare URL the two are
// the same text.
type Link struct {
	Start, End int
	Target     string
	// Rule is set when a link rule found it, rather than the URL detector.
	Rule bool
}

// linkOpenSchemes is the set of schemes a click may hand to the desktop. It is
// the one list tuios checks, both before it opens a link (internal/app) and
// before it accepts a target a link rule built, so a rule can never produce a
// link that a click would then refuse.
var linkOpenSchemes = []string{"http", "https", "mailto", "ftp", "ftps"}

// OpenableScheme reports whether rawURL has a scheme tuios hands to the
// desktop's own handler. An address with no scheme is refused.
func OpenableScheme(rawURL string) bool {
	scheme, _, ok := strings.Cut(rawURL, ":")
	if !ok || scheme == "" {
		return false
	}
	return slices.Contains(linkOpenSchemes, strings.ToLower(scheme))
}

// Links returns every link on line, sorted by position, none overlapping:
// each bare URL, and each rule match that does not overlap one. A real URL
// wins over a rule, and an earlier rule wins over a later one. With no rules
// this is URLs and nothing more.
func Links(line string, rules *LinkRules) []Link {
	urls := URLs(line)
	out := make([]Link, 0, len(urls))
	for _, r := range urls {
		out = append(out, Link{Start: r[0], End: r[1], Target: line[r[0]:r[1]]})
	}
	if rules.Empty() {
		return out
	}
	taken := slices.Clone(urls)
	for _, rule := range rules.rules {
		for _, loc := range rule.re.FindAllStringSubmatchIndex(line, -1) {
			s, e := loc[0], loc[1]
			if e <= s || overlaps(taken, s, e) {
				continue
			}
			target := string(rule.re.ExpandString(nil, rule.url, line, loc))
			if !linkTargetOK(target) {
				continue
			}
			taken = append(taken, [2]int{s, e})
			out = append(out, Link{Start: s, End: e, Target: target, Rule: true})
		}
	}
	slices.SortFunc(out, func(a, b Link) int { return a.Start - b.Start })
	return out
}

// LinkAt returns the link covering byte offset i of line, and whether there is
// one.
func LinkAt(line string, i int, rules *LinkRules) (Link, bool) {
	if i < 0 || i >= len(line) {
		return Link{}, false
	}
	if rules.Empty() {
		s, e, ok := URLAt(line, i)
		if !ok {
			return Link{}, false
		}
		return Link{Start: s, End: e, Target: line[s:e]}, true
	}
	for _, l := range Links(line, rules) {
		if l.Start > i {
			break
		}
		if i < l.End {
			return l, true
		}
	}
	return Link{}, false
}

// MayHoldLink reports whether line could hold a link, before it is made a
// string: a URL scheme, or a match of one of the rules. A false answer is
// certain, so a caller can skip the line; a true one sends it to Links.
func MayHoldLink(line []byte, rules *LinkRules) bool {
	if bytes.Contains(line, schemeSep) {
		return true
	}
	if rules.Empty() {
		return false
	}
	for _, rule := range rules.rules {
		if rule.first != nil && !holdsAny(line, rule.first) {
			continue
		}
		if rule.re.Match(line) {
			return true
		}
	}
	return false
}

// schemeSep is what every scheme the URL detector knows holds.
var schemeSep = []byte("://")

// RuleLinks returns only the rule matches on line, as Links settles them.
// Hints mode uses it: bare URLs reach hints through the url built-in.
func RuleLinks(line string, rules *LinkRules) []Link {
	if rules.Empty() {
		return nil
	}
	var out []Link
	for _, l := range Links(line, rules) {
		if l.Rule {
			out = append(out, l)
		}
	}
	return out
}

// linkTargetOK reports whether a target a rule built may become a link: an
// openable scheme, and nothing in it that would end a bare URL (urlStop and
// urlStopRune). The groups go into the template as they are, so without this a
// broad rule could build an address with quotes, backticks or angle brackets
// in it, which no bare URL can hold and which would reach the opener and the
// OSC 8 output.
func linkTargetOK(target string) bool {
	if !OpenableScheme(target) || !utf8.ValidString(target) {
		return false
	}
	for _, r := range target {
		if r < utf8.RuneSelf {
			if urlStop(byte(r)) {
				return false
			}
		} else if urlStopRune(r) {
			return false
		}
	}
	return true
}

func overlaps(taken [][2]int, s, e int) bool {
	for _, t := range taken {
		if s < t[1] && t[0] < e {
			return true
		}
	}
	return false
}
