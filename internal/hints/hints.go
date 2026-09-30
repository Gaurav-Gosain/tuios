// Package hints finds the things on a terminal screen a person is likely to
// want to copy (URLs, paths, hashes, addresses and so on) and names each one
// with a short label to type. It is the text half of hints mode: it knows
// nothing about panes, cells or keys, only about lines of text and the
// alphabet the labels are drawn from.
package hints

import (
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// The built-in pattern names. They are what a config file writes in
// hints.builtins, so they are part of the config format and do not change.
const (
	URL    = "url"
	Path   = "path"
	Diff   = "diff"
	SHA    = "sha"
	IP     = "ip"
	UUID   = "uuid"
	Color  = "color"
	Hex    = "hex"
	Number = "number"
	Email  = "email"
	ID     = "id"
)

// Custom is the kind of a match found by a pattern from the config.
const Custom = "custom"

// Names lists every built-in pattern, in the order the docs describe them.
func Names() []string {
	return []string{URL, Path, Diff, SHA, IP, UUID, Color, Hex, Number, Email, ID}
}

// Match is one thing found on a line. Start and End are the byte range to
// highlight and copy. The range a pattern claimed on the line can be wider
// (a diff header claims the "a/" in front of the path it copies), and that
// wider range is what stops two patterns from matching the same text.
type Match struct {
	Start, End int
	Kind       string
}

// pattern is one compiled rule.
type pattern struct {
	kind string
	re   *regexp.Regexp
	// group is the index of the submatch to copy, or 0 for the whole match.
	group int
	// line, when set, must match the whole line for this pattern to be tried.
	// The diff patterns use it, so "a/b" in ordinary prose is not read as the
	// "a/" side of a diff.
	line *regexp.Regexp
	// valid, when set, is asked about each candidate. It sees the whole line
	// and the candidate's range, so it can look at what is on either side.
	valid func(line string, start, end int) bool
	// trim, when set, may shorten a candidate from the right. A URL at the
	// end of a sentence gives its full stop back here.
	trim func(s string) string
}

// Matcher finds matches on lines of text. A nil Matcher finds nothing.
type Matcher struct {
	custom  []pattern
	builtin []pattern
}

// ParseBuiltins reads the hints.builtins value: "all" (or empty), "none", or
// a comma-separated list of names. It returns the enabled names and any name
// it did not recognise.
func ParseBuiltins(spec string) (enabled, unknown []string) {
	spec = strings.TrimSpace(strings.ToLower(spec))
	switch spec {
	case "", "all":
		return Names(), nil
	case "none":
		return nil, nil
	}
	known := Names()
	for part := range strings.SplitSeq(spec, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if !slices.Contains(known, name) {
			unknown = append(unknown, name)
			continue
		}
		if !slices.Contains(enabled, name) {
			enabled = append(enabled, name)
		}
	}
	return enabled, unknown
}

// CompilePattern compiles one user pattern and reports why it cannot be used.
// A group named "match" narrows what is copied to that group, the way
// tmux-fingers does it.
func CompilePattern(expr string) (*regexp.Regexp, int, error) {
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, 0, err
	}
	if re.MatchString("") {
		return nil, 0, fmt.Errorf("pattern %q matches empty text", expr)
	}
	return re, max(re.SubexpIndex("match"), 0), nil
}

// New builds a matcher from the enabled built-in names and the user's own
// patterns. A user pattern that does not compile is skipped and reported; the
// rest still work.
func New(builtins []string, custom []string) (*Matcher, []error) {
	m := &Matcher{}
	var errs []error
	for _, expr := range custom {
		re, group, err := CompilePattern(expr)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		m.custom = append(m.custom, pattern{kind: Custom, re: re, group: group})
	}
	for _, p := range builtinPatterns() {
		if slices.Contains(builtins, p.kind) {
			m.builtin = append(m.builtin, p)
		}
	}
	return m, errs
}

// candidate is a match before overlaps are settled.
type candidate struct {
	claimStart, claimEnd int // the range the pattern claimed on the line
	start, end           int // the range to copy
	kind                 string
	rank                 int // position of the pattern in its list
}

// Find returns the matches on one line, sorted by position, none overlapping.
//
// The user's patterns are settled first, so a pattern somebody wrote for
// their own work wins over a built-in that happens to cover the same text.
// Among the built-ins the longest claim wins, then the pattern listed first:
// a URL beats the path inside it, and a diff header beats the plain path it
// contains.
func (m *Matcher) Find(line string) []Match {
	if m == nil || line == "" {
		return nil
	}
	var taken []candidate
	taken = settle(taken, collect(m.custom, line))
	taken = settle(taken, collect(m.builtin, line))
	slices.SortFunc(taken, func(a, b candidate) int { return a.start - b.start })
	out := make([]Match, 0, len(taken))
	for _, c := range taken {
		out = append(out, Match{Start: c.start, End: c.end, Kind: c.kind})
	}
	return out
}

// collect runs every pattern over the line.
func collect(patterns []pattern, line string) []candidate {
	var out []candidate
	for rank, p := range patterns {
		if p.line != nil && !p.line.MatchString(line) {
			continue
		}
		for _, loc := range p.re.FindAllStringSubmatchIndex(line, -1) {
			cs, ce := loc[0], loc[1]
			s, e := cs, ce
			if p.group > 0 {
				s, e = loc[2*p.group], loc[2*p.group+1]
				if s < 0 {
					continue
				}
			}
			if p.trim != nil {
				e = s + len(p.trim(line[s:e]))
				ce = max(ce, e)
				if p.group == 0 {
					ce = e
				}
			}
			if e <= s {
				continue
			}
			if p.valid != nil && !p.valid(line, s, e) {
				continue
			}
			out = append(out, candidate{claimStart: cs, claimEnd: ce, start: s, end: e, kind: p.kind, rank: rank})
		}
	}
	return out
}

// settle adds the candidates that do not overlap anything already taken,
// longest claim first.
func settle(taken, cands []candidate) []candidate {
	slices.SortStableFunc(cands, func(a, b candidate) int {
		if la, lb := a.claimEnd-a.claimStart, b.claimEnd-b.claimStart; la != lb {
			return lb - la
		}
		if a.rank != b.rank {
			return a.rank - b.rank
		}
		return a.claimStart - b.claimStart
	})
	for _, c := range cands {
		clash := false
		for _, t := range taken {
			if c.claimStart < t.claimEnd && t.claimStart < c.claimEnd {
				clash = true
				break
			}
		}
		if !clash {
			taken = append(taken, c)
		}
	}
	return taken
}

// wordByte reports whether c continues a word, for the patterns that must
// not start or end in the middle of one.
func wordByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// isolated reports whether nothing that continues a token sits right before
// start or right after end. extra lists more bytes that count as continuing.
func isolated(line string, start, end int, extra string) bool {
	if start > 0 {
		if c := line[start-1]; wordByte(c) || strings.IndexByte(extra, c) >= 0 {
			return false
		}
	}
	if end < len(line) {
		if c := line[end]; wordByte(c) || strings.IndexByte(extra, c) >= 0 {
			return false
		}
	}
	return true
}

// trimURL gives trailing sentence punctuation back to the sentence and drops
// a closing bracket that was never opened inside the URL, so "(see
// https://x.org/a)." ends where a reader says it ends.
func trimURL(s string) string {
	for len(s) > 0 {
		last := s[len(s)-1]
		if strings.IndexByte(".,;:!?'\"", last) >= 0 {
			s = s[:len(s)-1]
			continue
		}
		var open byte
		switch last {
		case ')':
			open = '('
		case ']':
			open = '['
		}
		if open != 0 && strings.Count(s, string(open)) < strings.Count(s, string(last)) {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}

// trimPath drops the punctuation that ends a sentence rather than a path.
func trimPath(s string) string {
	for len(s) > 1 && strings.IndexByte(".,;:", s[len(s)-1]) >= 0 {
		if strings.HasSuffix(s, "/..") || strings.HasSuffix(s, "/.") || s == ".." {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}

// hasDigitAndLetter reports whether s has at least one decimal digit and at
// least one letter. A hash of seven or more characters almost always has
// both, and an English word never has a digit.
func hasDigitAndLetter(s string) bool {
	digit, letter := false, false
	for i := range len(s) {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			digit = true
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			letter = true
		}
	}
	return digit && letter
}

// kubeKinds are the resource names kubectl prints in front of a slash.
const kubeKinds = `pods?|po|deployments?|deploy|services?|svc|nodes?|no|jobs?|cronjobs?|cj|` +
	`statefulsets?|sts|daemonsets?|ds|replicasets?|rs|configmaps?|cm|secrets?|` +
	`ingress(?:es)?|ing|namespaces?|ns|persistentvolumeclaims?|pvc|persistentvolumes?|pv|` +
	`serviceaccounts?|sa|endpoints|ep|events?|ev|horizontalpodautoscalers?|hpa|` +
	`roles?|rolebindings?|clusterroles?|clusterrolebindings?|networkpolicies|netpol`

// pathSeg is one component of a path: no spaces, no colon, no quote.
//
// \w in Go is ASCII only, so a letter class stands in for it: a path or an
// address with an accented or non-Latin name, or a letter written with a
// combining mark, keeps the whole name rather than stopping at the first
// such letter.
const pathSeg = `[\p{L}\p{M}\p{N}_.@+~%=-]+`

// wordChars is \w with every script's letters, marks and digits.
const wordChars = `\p{L}\p{M}\p{N}_`

// builtinPatterns is every built-in rule. The order is the tie-break when two
// claims are the same length, so the more specific kind comes first.
//
// The rules compile on first use. Compiled at package init they cost every
// tuios process about 0.8 ms and 1,600 allocations, a third of all package
// init, and only a process that opens hints mode reads them.
var builtinPatterns = sync.OnceValue(func() []pattern {
	return []pattern{
		{
			kind: URL,
			re:   regexp.MustCompile(`(?:https?|ftps?|file|ssh|git)://[^\s<>"'` + "`" + `|^{}\\]+|\bgit@[\w.-]+:[\w./~-]+`),
			trim: trimURL,
			valid: func(line string, s, e int) bool {
				// A scheme needs something after it.
				return !strings.HasSuffix(line[s:e], "://")
			},
		},
		{
			// The two sides of a diff header, copied without their a/ and b/.
			kind:  Diff,
			re:    regexp.MustCompile(`(?:^|\s)[ab]/(\S+)`),
			line:  regexp.MustCompile(`^(?:diff --git |--- |\+\+\+ )`),
			group: 1,
		},
		{
			// A path git status prints after "modified:" and the like.
			kind:  Diff,
			re:    regexp.MustCompile(`:\s+(?:\S+ -> )?(\S+)\s*$`),
			line:  regexp.MustCompile(`^\s*(?:modified|deleted|new file|renamed|copied|typechange|both modified|both added|both deleted|added by us|added by them|deleted by us|deleted by them):\s`),
			group: 1,
		},
		{
			kind: UUID,
			re:   regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`),
		},
		{
			kind: Email,
			re:   regexp.MustCompile(`[` + wordChars + `.+-]+@[` + wordChars + `-]+(?:\.[` + wordChars + `-]+)+`),
		},
		{
			// A Kubernetes resource as kubectl names it: kind/name, with an
			// optional API group on the kind (deployment.apps/web).
			kind: ID,
			re:   regexp.MustCompile(`\b(?:` + kubeKinds + `)(?:\.[a-z0-9.-]+)?/[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?\b`),
		},
		{
			// A pod name: the deployment, the replica set's hash and the pod's
			// own five characters.
			kind: ID,
			re:   regexp.MustCompile(`\b[a-z0-9]+(?:-[a-z0-9]+)*-[a-z0-9]{8,10}-[a-z0-9]{5}\b`),
			valid: func(line string, s, e int) bool {
				// The two hashes almost always hold a digit. Words joined with
				// hyphens never do, and that is what keeps prose out.
				tail := line[s:e]
				i := strings.LastIndexByte(tail, '-')
				j := strings.LastIndexByte(tail[:i], '-')
				return strings.ContainsAny(tail[j+1:], "0123456789")
			},
		},
		{
			// An image or layer digest, as docker and podman print it.
			kind: ID,
			re:   regexp.MustCompile(`\bsha256:[0-9a-f]{12,64}\b`),
		},
		{
			kind: IP,
			re:   regexp.MustCompile(`(?:[0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}(?:%[\w.]+)?`),
			valid: func(line string, s, e int) bool {
				text := line[s:e]
				if !strings.ContainsAny(text, "0123456789abcdefABCDEF") || !isolated(line, s, e, ":.") {
					return false
				}
				addr, err := netip.ParseAddr(text)
				return err == nil && addr.Is6()
			},
		},
		{
			kind: IP,
			re:   regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?:/\d{1,2}|:\d{1,5})?\b`),
			valid: func(line string, s, e int) bool {
				host := line[s:e]
				if i := strings.IndexAny(host, "/:"); i >= 0 {
					host = host[:i]
				}
				addr, err := netip.ParseAddr(host)
				return err == nil && addr.Is4() && isolated(line, s, e, ".")
			},
		},
		{
			kind: Path,
			re: regexp.MustCompile(`(?:(?:~|\.\.?)?(?:/` + pathSeg + `)+|` + pathSeg + `(?:/` + pathSeg + `)+)/?` +
				`(?::\d+(?::\d+)?)?`),
			trim: trimPath,
			valid: func(line string, s, e int) bool {
				text := line[s:e]
				// A lone slash, or slashes and dots only, is not a path worth a label.
				if strings.Trim(text, "/.~") == "" {
					return false
				}
				// "//" is a URL's, a comment's, or nothing.
				if strings.HasPrefix(text, "//") {
					return false
				}
				return true
			},
		},
		{
			kind: SHA,
			re:   regexp.MustCompile(`\b[0-9a-f]{7,64}\b`),
			valid: func(line string, s, e int) bool {
				n := e - s
				return (n <= 40 || n == 64) && hasDigitAndLetter(line[s:e])
			},
		},
		{
			kind: Color,
			re:   regexp.MustCompile(`#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3})\b`),
			valid: func(line string, s, e int) bool {
				if s > 0 && wordByte(line[s-1]) {
					return false
				}
				// "#123" is an issue far more often than a colour, so the short
				// form needs a letter to count.
				return e-s != 4 || strings.ContainsAny(line[s+1:e], "abcdefABCDEF")
			},
		},
		{
			kind: Hex,
			re:   regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`),
		},
		{
			kind: Number,
			re:   regexp.MustCompile(`\b\d{4,}\b`),
		},
	}
})
