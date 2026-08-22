// Command herdrconv converts a herdr agent-detection manifest
// (github.com/herdrdev/herdr, website/agent-detection/*.toml, Apache-2.0) into
// a tuios harness manifest draft.
//
//	go run ./internal/harness/herdrconv path/to/agent.toml > draft.toml
//
// The output is a draft, not a manifest to ship as-is. The [detect] block is a
// placeholder, because herdr keeps process detection in code rather than in
// these files, and every emitted rule needs review against the policy written
// down in manifests/claude-code.toml: a rule that matches too much is worse
// than one that matches nothing. What the converter guarantees is fidelity of
// the rules it does emit and a named reason for every rule it does not, so the
// review starts from an honest account instead of a silent subset.
//
// The mapping, and where it bends:
//
//   - blocked becomes needs_input; working and idle carry over.
//   - herdr matches substrings case-folded, so the draft sets fold_case.
//   - herdr's any/all/not lists hold nested predicate groups. tuios rules are
//     flat, so a rule with compound groups is expanded into several rules with
//     the same state and priority, which preserves the OR exactly. A second
//     any-of set inside one conjunction becomes a (?i) alternation pattern.
//   - a multi-string not group blocks in herdr only when the whole group is
//     present; flattened it blocks on any one string. Stricter, which errs
//     toward suppression, never toward a false positive.
//   - line_regex and regex both become tuios regex, which is (?m)-compiled, so
//     ^ and $ anchor lines either way. Patterns are rewritten from Rust regex
//     to RE2 (\u{...} escapes, \p{Alphabetic}) and must compile, or the rule
//     is dropped with the pattern named.
//   - per-rule regions collapse to one bottom-of-pane window: the manifest
//     lines is the largest window any kept rule asked for, and whole_recent
//     counts as eight. Narrower than herdr sees, which can only miss, and a
//     rule that asked for a region tuios cannot read (osc_title, osc_progress,
//     prompt-box geometry) is dropped with that reason.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type gate struct {
	Contains  []string `toml:"contains"`
	Regex     []string `toml:"regex"`
	LineRegex []string `toml:"line_regex"`
	All       []gate   `toml:"all"`
	Any       []gate   `toml:"any"`
	Not       []gate   `toml:"not"`
}

type rule struct {
	ID       string `toml:"id"`
	State    string `toml:"state"`
	Priority int    `toml:"priority"`
	Region   string `toml:"region"`
	gate
}

type manifest struct {
	ID      string `toml:"id"`
	Version string `toml:"version"`
	Rules   []rule `toml:"rules"`
}

// conj is one flat tuios rule in the making: a conjunction of predicates.
type conj struct {
	all      []string
	anySets  [][]string
	regex    []string
	notStr   []string
	notRegex []string
	notes    []string
}

var stateMap = map[string]string{"blocked": "needs_input", "working": "working", "idle": "idle"}

// wholeRecentLines approximates herdr's whole_recent region, which is however
// much recent screen herdr keeps. tuios reads a fixed tail; eight lines is the
// window the hand-written manifests settled on for prompt-shaped chrome.
const wholeRecentLines = 8

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: herdrconv <herdr-agent-manifest.toml>")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var m manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var out strings.Builder
	var kept, dropped int
	lines := 0
	var body strings.Builder
	for _, r := range m.Rules {
		conjs, window, reason := convertRule(r)
		if reason != "" {
			dropped++
			fmt.Fprintf(os.Stderr, "dropped %s (%s, priority %d): %s\n", r.ID, r.State, r.Priority, reason)
			continue
		}
		kept++
		lines = max(lines, window)
		state := stateMap[r.State]
		for i, c := range conjs {
			body.WriteString("\n")
			name := r.ID
			if len(conjs) > 1 {
				name = fmt.Sprintf("%s (variant %d of %d)", r.ID, i+1, len(conjs))
			}
			fmt.Fprintf(&body, "# herdr rule: %s\n", name)
			for _, n := range c.notes {
				fmt.Fprintf(&body, "# note: %s\n", n)
			}
			body.WriteString("[[screen.rule]]\n")
			fmt.Fprintf(&body, "state    = %q\n", state)
			fmt.Fprintf(&body, "priority = %d\n", r.Priority)
			emitList(&body, "all", c.all)
			var anySet []string
			if len(c.anySets) > 0 {
				anySet = c.anySets[0]
				// Further any-of sets cannot share the field; each becomes an
				// alternation. (?i) keeps the folding contains would have had.
				for _, extra := range c.anySets[1:] {
					c.regex = append(c.regex, "(?i)"+quoteAlternation(extra))
				}
			}
			emitList(&body, "any", anySet)
			emitList(&body, "not", c.notStr)
			emitList(&body, "regex", c.regex)
			emitList(&body, "not_regex", c.notRegex)
		}
	}

	fmt.Fprintf(&out, "# %s: converted from herdr's agent-detection manifest\n", m.ID)
	fmt.Fprintf(&out, "# (github.com/herdrdev/herdr website/agent-detection, manifest version %s, Apache-2.0).\n", m.Version)
	fmt.Fprintf(&out, "# Generated by go run ./internal/harness/herdrconv; review before shipping.\n\n")
	out.WriteString("schema_version = 1\n")
	fmt.Fprintf(&out, "id             = %q\n", m.ID)
	fmt.Fprintf(&out, "display_name   = %q\n", strings.ToUpper(m.ID[:1])+m.ID[1:])
	out.WriteString("priority       = 50\n\n")
	out.WriteString("# PLACEHOLDER: herdr detects processes in code, not in its manifest, so this\n")
	out.WriteString("# block is not converted data. Establish the real names before shipping.\n")
	out.WriteString("[detect]\n")
	fmt.Fprintf(&out, "comm = [%q]\n\n", m.ID)
	out.WriteString("[screen]\n")
	out.WriteString("enabled   = false\n")
	out.WriteString("fold_case = true\n")
	fmt.Fprintf(&out, "lines     = %d\n", max(lines, 1))
	out.WriteString(body.String())

	fmt.Print(out.String())
	fmt.Fprintf(os.Stderr, "%s: converted %d of %d rules\n", m.ID, kept, kept+dropped)
}

// convertRule turns one herdr rule into flat conjunctions, or names the reason
// it cannot. window is the bottom-of-pane lines the rule's region needs.
func convertRule(r rule) (conjs []conj, window int, dropReason string) {
	if stateMap[r.State] == "" {
		return nil, 0, fmt.Sprintf("state %q has no tuios equivalent", r.State)
	}
	window, ok := regionWindow(r.Region)
	if !ok {
		return nil, 0, fmt.Sprintf("region %q is not readable from a pane tail", r.Region)
	}
	conjs, err := expand(r.gate)
	if err != nil {
		return nil, 0, err.Error()
	}
	for _, c := range conjs {
		if len(c.all) == 0 && len(c.anySets) == 0 && len(c.regex) == 0 {
			return nil, 0, "no positive predicate survives conversion"
		}
	}
	return conjs, window, ""
}

func regionWindow(region string) (int, bool) {
	region = strings.TrimSpace(region)
	if region == "" || region == "whole_recent" {
		return wholeRecentLines, true
	}
	for _, prefix := range []string{"bottom_non_empty_lines(", "bottom_lines("} {
		if rest, ok := strings.CutPrefix(region, prefix); ok {
			if n, err := strconv.Atoi(strings.TrimSuffix(rest, ")")); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

// expand flattens a herdr predicate gate into a disjunction of flat
// conjunctions. Every disjunct becomes its own tuios rule, so the OR across
// them is exact rather than approximated.
func expand(g gate) ([]conj, error) {
	base := conj{all: g.Contains}
	for _, p := range append(append([]string{}, g.Regex...), g.LineRegex...) {
		rp, err := translatePattern(p)
		if err != nil {
			return nil, err
		}
		base.regex = append(base.regex, rp)
	}

	for _, n := range g.Not {
		if err := flattenNot(n, &base); err != nil {
			return nil, err
		}
	}

	disjuncts := []conj{base}
	for _, sub := range g.All {
		expanded, err := expand(sub)
		if err != nil {
			return nil, err
		}
		disjuncts = cross(disjuncts, expanded)
	}

	if len(g.Any) > 0 {
		// Single-substring alternatives share one any-of set; anything richer
		// is its own disjunct, ANDed with everything gathered so far.
		var singles []string
		var rich []conj
		for _, sub := range g.Any {
			expanded, err := expand(sub)
			if err != nil {
				return nil, err
			}
			if len(expanded) == 1 && isSingleSubstring(expanded[0]) {
				singles = append(singles, expanded[0].all[0])
				continue
			}
			rich = append(rich, expanded...)
		}
		var alts []conj
		if len(singles) > 0 {
			alts = append(alts, conj{anySets: [][]string{singles}})
		}
		alts = append(alts, rich...)
		disjuncts = cross(disjuncts, alts)
	}
	return disjuncts, nil
}

func isSingleSubstring(c conj) bool {
	return len(c.all) == 1 && len(c.anySets) == 0 && len(c.regex) == 0 &&
		len(c.notStr) == 0 && len(c.notRegex) == 0
}

// flattenNot folds one herdr not group into flat veto lists. A group of
// several substrings vetoes in herdr only when all are present; flat, any one
// vetoes. That is stricter, and stricter is the safe direction for a veto.
func flattenNot(n gate, into *conj) error {
	if len(n.All) > 0 || len(n.Any) > 0 || len(n.Not) > 0 {
		return fmt.Errorf("a not group nests further groups; flatten it by hand")
	}
	if len(n.Contains) > 1 {
		into.notes = append(into.notes,
			fmt.Sprintf("herdr vetoed only on %q together; any one vetoes here, which suppresses more", n.Contains))
	}
	into.notStr = append(into.notStr, n.Contains...)
	for _, p := range append(append([]string{}, n.Regex...), n.LineRegex...) {
		rp, err := translatePattern(p)
		if err != nil {
			return err
		}
		into.notRegex = append(into.notRegex, rp)
	}
	return nil
}

func cross(left, right []conj) []conj {
	if len(right) == 0 {
		return left
	}
	out := make([]conj, 0, len(left)*len(right))
	for _, l := range left {
		for _, r := range right {
			out = append(out, conj{
				all:      concat(l.all, r.all),
				anySets:  concat(l.anySets, r.anySets),
				regex:    concat(l.regex, r.regex),
				notStr:   concat(l.notStr, r.notStr),
				notRegex: concat(l.notRegex, r.notRegex),
				notes:    concat(l.notes, r.notes),
			})
		}
	}
	return out
}

func concat[T any](a, b []T) []T {
	if len(b) == 0 {
		return a
	}
	return append(append(make([]T, 0, len(a)+len(b)), a...), b...)
}

var (
	bracedEscape = regexp.MustCompile(`\\u\{([0-9a-fA-F]{1,6})\}`)
	bareEscape   = regexp.MustCompile(`\\u([0-9a-fA-F]{4})`)
)

// translatePattern rewrites a Rust-regex pattern into RE2 and proves it
// compiles the way the loader will compile it. An untranslatable pattern drops
// its whole rule: shipping the rule without a required pattern would loosen it.
func translatePattern(p string) (string, error) {
	q := bracedEscape.ReplaceAllString(p, `\x{$1}`)
	q = bareEscape.ReplaceAllString(q, `\x{$1}`)
	q = strings.ReplaceAll(q, `\p{Alphabetic}`, `\p{L}`)
	if _, err := regexp.Compile("(?m)" + q); err != nil {
		return "", fmt.Errorf("pattern %q does not translate to RE2: %v", p, err)
	}
	return q, nil
}

func quoteAlternation(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = regexp.QuoteMeta(s)
	}
	return strings.Join(quoted, "|")
}

func emitList(w *strings.Builder, key string, items []string) {
	if len(items) == 0 {
		return
	}
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = strconv.Quote(s)
	}
	if len(key) < 9 {
		key = (key + "         ")[:9]
	} else {
		key += " "
	}
	fmt.Fprintf(w, "%s= [%s]\n", key, strings.Join(quoted, ", "))
}
