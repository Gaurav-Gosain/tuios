// Command herdrconv converts a herdr agent-detection manifest
// (github.com/herdrdev/herdr, src/detect/manifests/*.toml, Apache-2.0) into a
// tuios harness manifest draft.
//
//	go run ./internal/harness/herdrconv path/to/agent.toml > draft.toml
//
// The output is a draft, not a manifest to ship as-is. The [detect] block is a
// placeholder, because herdr keeps process detection in code rather than in
// these files, and every emitted rule needs review against the policy written
// down in internal/harness/registry_test.go: a rule that matches too much is
// worse than one that matches nothing. What the converter guarantees is
// fidelity of the rules it does emit and a named reason for every rule it does
// not, so the review starts from an honest account instead of a silent subset.
//
// The mapping:
//
//   - blocked becomes needs_input; working and idle carry over. unknown, and
//     the skip_state_update rules that use it, have no tuios equivalent and
//     are dropped: tuios has no "leave the state alone" rule.
//   - herdr matches substrings case-folded, so the draft sets fold_case.
//   - herdr's gates map one to one: contains becomes all, all becomes all_of,
//     any becomes any_of, not becomes none_of. A group holding one substring
//     is written in the flat lists (any, not) instead, which reads the same.
//   - line_regex and regex both become tuios regex, which is (?m)-compiled, so
//     ^ and $ anchor lines either way. Patterns are rewritten from Rust regex
//     to RE2 (\u{...} escapes, \p{Alphabetic}) and must compile, or the rule
//     is dropped with the pattern named.
//   - regions keep their names: whole_recent is the tail, and
//     bottom_non_empty_lines(N), prompt_box_body, above_prompt_box,
//     last_non_empty_above_prompt_box and after_last_horizontal_rule read the
//     same part of it. bottom_lines(N) becomes bottom_non_empty_lines(N),
//     which reads at least as much. The manifest's lines is the largest window
//     any kept rule asks for, and whole_recent counts as eight. herdr reads the
//     whole screen for whole_recent, so a rule relying on text far up the
//     screen can miss here, which is the cheap direction.
//   - osc_title and osc_progress rules go to the [title] block, which reads
//     the pane's title and, with region = "osc_progress", its last OSC 9;4
//     report. A title substring matches whole tokens only in tuios; see
//     internal/harness/title.go.
//   - top_non_empty_lines(N) and Codex's prompt-marker regions
//     (after_last_prompt_marker and the like) have no tuios equivalent, and
//     their rules are dropped with that reason.
package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"
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

var stateMap = map[string]string{"blocked": "needs_input", "working": "working", "idle": "idle"}

// wholeRecentLines approximates herdr's whole_recent region, which is the
// whole visible screen. tuios reads a fixed tail; eight lines is the window the
// hand-written manifests settled on for prompt-shaped chrome.
const wholeRecentLines = 8

// tailRegions are herdr's screen regions tuios reads the same way, mapped to
// the name the draft writes.
var tailRegions = map[string]string{
	"":                                "",
	"whole_recent":                    "",
	"prompt_box_body":                 "prompt_box",
	"above_prompt_box":                "above_prompt_box",
	"last_non_empty_above_prompt_box": "last_non_empty_above_prompt_box",
	"after_last_horizontal_rule":      "after_last_horizontal_rule",
}

// result is a converted manifest: the draft, and one line per rule saying
// what happened to it.
type result struct {
	draft          string
	report         []string
	kept, dropped  int
	screen, titles int
}

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
	res, err := convert(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(res.draft)
	for _, line := range res.report {
		fmt.Fprintln(os.Stderr, line)
	}
}

// convert turns one herdr manifest into a tuios draft.
func convert(data []byte) (result, error) {
	var m manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return result{}, err
	}
	var res result
	var screen, title strings.Builder
	lines := 0
	for _, r := range m.Rules {
		block, region, window, reason := place(r)
		if reason == "" {
			reason = checkGate(r.gate)
		}
		if reason == "" && block == "screen" && r.State == "idle" && region != "prompt_box" && !provesShape(r.gate) {
			reason = "tuios needs an idle screen rule to read prompt_box or carry a pattern on every path; write one by hand"
		}
		if reason != "" {
			res.dropped++
			res.report = append(res.report, fmt.Sprintf("dropped %s (%s, priority %d): %s", r.ID, r.State, r.Priority, reason))
			continue
		}
		res.kept++
		w := &screen
		if block == "title" {
			w = &title
			res.titles++
		} else {
			res.screen++
			lines = max(lines, window)
		}
		fmt.Fprintf(w, "\n# herdr rule: %s\n", r.ID)
		fmt.Fprintf(w, "[[%s.rule]]\n", block)
		fmt.Fprintf(w, "state    = %q\n", stateMap[r.State])
		fmt.Fprintf(w, "priority = %d\n", r.Priority)
		if region != "" {
			fmt.Fprintf(w, "region   = %q\n", region)
		}
		writeGate(w, r.gate)
	}
	res.report = append(res.report, fmt.Sprintf("%s: converted %d of %d rules (%d screen, %d title)",
		m.ID, res.kept, res.kept+res.dropped, res.screen, res.titles))

	var out strings.Builder
	fmt.Fprintf(&out, "# %s: converted from herdr's agent-detection manifest\n", m.ID)
	fmt.Fprintf(&out, "# (github.com/herdrdev/herdr, src/detect/manifests, manifest version %s,\n", m.Version)
	out.WriteString("# Apache-2.0; see internal/harness/manifests/LICENSE-herdr).\n")
	out.WriteString("# Generated by go run ./internal/harness/herdrconv; review before shipping.\n\n")
	out.WriteString("schema_version = 1\n")
	fmt.Fprintf(&out, "id             = %q\n", m.ID)
	display := m.ID
	if display != "" {
		display = strings.ToUpper(display[:1]) + display[1:]
	}
	fmt.Fprintf(&out, "display_name   = %q\n", display)
	out.WriteString("priority       = 50\n\n")
	out.WriteString("# PLACEHOLDER: herdr detects processes in code, not in its manifest, so this\n")
	out.WriteString("# block is not converted data. Establish the real names before shipping.\n")
	out.WriteString("# The require block keeps a short placeholder name from loading on its own.\n")
	out.WriteString("[detect]\n")
	fmt.Fprintf(&out, "comm = [%q]\n", m.ID)
	out.WriteString("[detect.require]\n")
	fmt.Fprintf(&out, "exe_glob = [%q]\n\n", "**/"+m.ID+"/**")
	out.WriteString("[screen]\n")
	out.WriteString("enabled   = false\n")
	out.WriteString("fold_case = true\n")
	fmt.Fprintf(&out, "lines     = %d\n", max(lines, 1))
	out.WriteString(screen.String())
	if title.Len() > 0 {
		out.WriteString("\n[title]\n")
		out.WriteString("enabled   = false\n")
		out.WriteString("fold_case = true\n")
		out.WriteString(title.String())
	}
	res.draft = out.String()
	return res, nil
}

// place says which block a rule goes to, the region it reads there, and the
// tail window it needs, or why it cannot be carried.
func place(r rule) (block, region string, window int, dropReason string) {
	if stateMap[r.State] == "" {
		return "", "", 0, fmt.Sprintf("state %q has no tuios equivalent", r.State)
	}
	name := strings.TrimSpace(r.Region)
	switch name {
	case "osc_title":
		return "title", "", 0, ""
	case "osc_progress":
		return "title", "osc_progress", 0, ""
	}
	if mapped, ok := tailRegions[name]; ok {
		return "screen", mapped, wholeRecentLines, ""
	}
	for _, prefix := range []string{"bottom_non_empty_lines(", "bottom_lines("} {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			if n, err := strconv.Atoi(strings.TrimSuffix(rest, ")")); err == nil && n > 0 {
				return "screen", fmt.Sprintf("bottom_non_empty_lines(%d)", n), n, ""
			}
		}
	}
	return "", "", 0, fmt.Sprintf("region %q has no tuios equivalent", name)
}

// checkGate reports why a gate cannot be carried: an untranslatable pattern,
// or no positive predicate at the top.
func checkGate(g gate) string {
	if len(g.Contains)+len(g.Regex)+len(g.LineRegex)+len(g.All)+len(g.Any) == 0 {
		return "no positive predicate"
	}
	var walk func(g gate) string
	walk = func(g gate) string {
		for _, p := range slices.Concat(g.Regex, g.LineRegex) {
			if _, err := translatePattern(p); err != nil {
				return err.Error()
			}
		}
		for _, sub := range slices.Concat(g.All, g.Any, g.Not) {
			if reason := walk(sub); reason != "" {
				return reason
			}
		}
		return ""
	}
	return walk(g)
}

// provesShape mirrors the loader's idle rule check: a pattern on every path to
// a match.
func provesShape(g gate) bool {
	if len(g.Regex)+len(g.LineRegex) > 0 {
		return true
	}
	for _, sub := range g.All {
		if provesShape(sub) {
			return true
		}
	}
	if len(g.Any) == 0 {
		return false
	}
	for _, sub := range g.Any {
		if !provesShape(sub) {
			return false
		}
	}
	return true
}

// writeGate writes a rule's gate as top-level keys.
func writeGate(w *strings.Builder, g gate) {
	for _, kv := range gateFields(g) {
		key := kv[0]
		if len(key) < 9 {
			key = (key + "         ")[:9]
		} else {
			key += " "
		}
		fmt.Fprintf(w, "%s= %s\n", key, kv[1])
	}
}

// gateFields renders a gate as key and value pairs, the value already TOML.
func gateFields(g gate) [][2]string {
	var out [][2]string
	add := func(key string, items []string) {
		if len(items) > 0 {
			out = append(out, [2]string{key, tomlStrings(items)})
		}
	}
	add("all", g.Contains)

	// An any group of lone substrings is a flat any list; richer groups nest.
	var anySingles []string
	var anyRich []gate
	for _, sub := range g.Any {
		if s, ok := loneSubstring(sub); ok {
			anySingles = append(anySingles, s)
			continue
		}
		anyRich = append(anyRich, sub)
	}
	if len(anyRich) == 0 {
		add("any", anySingles)
	} else if len(anySingles) > 0 {
		anyRich = append([]gate{{Contains: nil, Any: singlesAsGates(anySingles)}}, anyRich...)
	}

	// A not group of one substring or one pattern is a flat veto.
	var notStr, notRegex []string
	var noneOf []gate
	for _, sub := range g.Not {
		if s, ok := loneSubstring(sub); ok {
			notStr = append(notStr, s)
			continue
		}
		if p, ok := lonePattern(sub); ok {
			notRegex = append(notRegex, p)
			continue
		}
		noneOf = append(noneOf, sub)
	}
	add("not", notStr)
	add("regex", translated(slices.Concat(g.Regex, g.LineRegex)))
	add("not_regex", translated(notRegex))
	if len(g.All) > 0 {
		out = append(out, [2]string{"all_of", inlineGates(g.All)})
	}
	if len(anyRich) > 0 {
		out = append(out, [2]string{"any_of", inlineGates(anyRich)})
	}
	if len(noneOf) > 0 {
		out = append(out, [2]string{"none_of", inlineGates(noneOf)})
	}
	return out
}

func singlesAsGates(items []string) []gate {
	out := make([]gate, len(items))
	for i, s := range items {
		out[i] = gate{Contains: []string{s}}
	}
	return out
}

func loneSubstring(g gate) (string, bool) {
	if len(g.Contains) == 1 && len(g.Regex)+len(g.LineRegex)+len(g.All)+len(g.Any)+len(g.Not) == 0 {
		return g.Contains[0], true
	}
	return "", false
}

func lonePattern(g gate) (string, bool) {
	patterns := slices.Concat(g.Regex, g.LineRegex)
	if len(patterns) == 1 && len(g.Contains)+len(g.All)+len(g.Any)+len(g.Not) == 0 {
		return patterns[0], true
	}
	return "", false
}

// inlineGates renders nested gates as a TOML array of inline tables.
func inlineGates(gates []gate) string {
	parts := make([]string, len(gates))
	for i, g := range gates {
		fields := gateFields(g)
		kv := make([]string, len(fields))
		for j, f := range fields {
			kv[j] = f[0] + " = " + f[1]
		}
		parts[i] = "{ " + strings.Join(kv, ", ") + " }"
	}
	return "[ " + strings.Join(parts, ", ") + " ]"
}

func translated(patterns []string) []string {
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		q, err := translatePattern(p)
		if err != nil {
			continue // checkGate refused the rule already
		}
		out = append(out, q)
	}
	return out
}

// tomlStrings renders strings as a TOML array. Literal strings keep patterns
// readable; a string holding a single quote or a control character falls back
// to a basic string.
func tomlStrings(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		if strings.ContainsAny(s, "'\n\r\t") {
			quoted[i] = strconv.Quote(s)
		} else {
			quoted[i] = "'" + s + "'"
		}
	}
	return "[" + strings.Join(quoted, ", ") + "]"
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
