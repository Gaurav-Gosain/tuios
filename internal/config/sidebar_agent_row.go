package config

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// The agent row's tokens and how each is drawn, from [appearance.sidebar.agent_row].
//
// An agent row used to be fixed Go: which facts it showed, in what order and
// in what ink were all decided here. herdr lets a person style each token of
// its sidebar by the token's value, with ordered text and numeric rules, and
// the maintainer named that as the thing to have. This is that surface, shaped
// so a person who has never seen it can read it:
//
//	[appearance.sidebar.agent_row]
//	tokens = ["session", "harness", "name", "elapsed", "message"]
//
//	[appearance.sidebar.agent_row.name]
//	fg = "text"
//	bold = false
//
//	[[appearance.sidebar.agent_row.elapsed.rule]]
//	gt = 30
//	fg = "warning"
//
// Every token starts with the rail's own style. A token's fg, bold and dim
// replace it. Its rules replace it again, first match wins, each rule with
// exactly one test: equals, contains, starts_with, gt or lt. Text tests read
// the token as drawn; gt and lt read it as a number, which for elapsed is the
// minutes since the state was entered. There is no regular expression here on
// purpose: a config file is not a place to hand someone a regex engine, and
// the five tests cover what a rule about one word or one number needs.
//
// # Fail safely
//
// The table is decoded as generic TOML and read here rather than into a typed
// struct, because a typed decode refuses the whole config file on one wrong
// type, and a rail that draws in the wrong colour is a smaller failure than a
// tuios that starts with every setting at its default. A value this reader
// does not understand is dropped and named in Problems, which the validator
// prints, and the rest of the table stands.

// SidebarAgentRowTokens is every token an agent row can carry, in the order a
// person is most likely to want them listed.
var SidebarAgentRowTokens = []string{"harness", "name", "state", "elapsed", "message", "session", "host"}

// SidebarAgentRowDefaultTokens is the row as it ships: what the rail drew before
// the table existed, in the order it drew it. state and host are left out so a
// rail with no config sees no change; both have a value on every row.
var SidebarAgentRowDefaultTokens = []string{"session", "harness", "name", "elapsed", "message"}

// SidebarTokenColors are the palette names a token's fg may take, beside a
// #rrggbb literal. They are the rail's own tiers and the four status inks.
var SidebarTokenColors = []string{"text", "dim", "muted", "accent", "warning", "error", "success", "info"}

// sidebarTokenTests are the tests a rule may carry, exactly one per rule.
var sidebarTokenTests = []string{"equals", "contains", "starts_with", "gt", "lt"}

// SidebarAgentRowMaxRules is how many rules one token may carry. Eight is
// more than a row of seven tokens needs and few enough to read at once.
const SidebarAgentRowMaxRules = 8

// SidebarTokenLook is what a token or a rule says about ink: a colour name or
// hex, and bold and dim as three-state so an absent key leaves the rail's own
// choice alone and an explicit false turns the rail's bold off.
type SidebarTokenLook struct {
	Fg   string
	Bold *bool
	Dim  *bool
}

// Overlay is this look with another's set fields written over it.
func (l SidebarTokenLook) Overlay(over SidebarTokenLook) SidebarTokenLook {
	if over.Fg != "" {
		l.Fg = over.Fg
	}
	if over.Bold != nil {
		l.Bold = over.Bold
	}
	if over.Dim != nil {
		l.Dim = over.Dim
	}
	return l
}

// Set reports whether the look says anything at all.
func (l SidebarTokenLook) Set() bool { return l.Fg != "" || l.Bold != nil || l.Dim != nil }

// SidebarTokenRule is one value rule: a test, its operand, and the look a
// match earns.
type SidebarTokenRule struct {
	Test       string
	Text       string
	Number     float64
	IgnoreCase bool
	Look       SidebarTokenLook
}

// Matches applies the rule to a token's drawn text and, for the numeric tests,
// its number. hasNumber is false for a token that is not a number, and such a
// token matches no numeric rule.
func (r SidebarTokenRule) Matches(text string, number float64, hasNumber bool) bool {
	switch r.Test {
	case "gt":
		return hasNumber && number > r.Number
	case "lt":
		return hasNumber && number < r.Number
	}
	want := r.Text
	if r.IgnoreCase {
		text, want = strings.ToLower(text), strings.ToLower(want)
	}
	switch r.Test {
	case "equals":
		return text == want
	case "contains":
		return strings.Contains(text, want)
	case "starts_with":
		return strings.HasPrefix(text, want)
	}
	return false
}

// SidebarTokenStyle is one token's base look and its ordered rules.
type SidebarTokenStyle struct {
	Base  SidebarTokenLook
	Rules []SidebarTokenRule
}

// Resolve is the look a token with this text and number gets: the base, with
// the first matching rule's look written over it.
func (s SidebarTokenStyle) Resolve(text string, number float64, hasNumber bool) SidebarTokenLook {
	look := s.Base
	for _, r := range s.Rules {
		if r.Matches(text, number, hasNumber) {
			return look.Overlay(r.Look)
		}
	}
	return look
}

// SidebarAgentRowSpec is the parsed table: the tokens in order and a style per
// token, plus what the reader could not use.
type SidebarAgentRowSpec struct {
	Tokens []string
	Styles map[string]SidebarTokenStyle
	// Problems is one line per value the reader dropped, for the validator.
	Problems []string
	// Fingerprint changes whenever the drawn result could, for a render cache
	// to key on.
	Fingerprint string
}

// Has reports whether the row carries a token at all.
func (s *SidebarAgentRowSpec) Has(token string) bool {
	return slices.Contains(s.Tokens, token)
}

// Style is the token's style, empty for a token the table does not mention.
func (s *SidebarAgentRowSpec) Style(token string) SidebarTokenStyle {
	if s == nil || s.Styles == nil {
		return SidebarTokenStyle{}
	}
	return s.Styles[token]
}

// RuleCount is how many rules the table carries over every token.
func (s *SidebarAgentRowSpec) RuleCount() int {
	n := 0
	for _, st := range s.Styles {
		n += len(st.Rules)
	}
	return n
}

// Custom reports whether the table changed anything from the shipped row.
func (s *SidebarAgentRowSpec) Custom() bool {
	if s == nil {
		return false
	}
	if !slices.Equal(s.Tokens, SidebarAgentRowDefaultTokens) {
		return true
	}
	for _, st := range s.Styles {
		if st.Base.Set() || len(st.Rules) > 0 {
			return true
		}
	}
	return false
}

// DefaultSidebarAgentRow is the row as it ships.
func DefaultSidebarAgentRow() SidebarAgentRowSpec {
	spec := SidebarAgentRowSpec{Tokens: slices.Clone(SidebarAgentRowDefaultTokens)}
	spec.Fingerprint = spec.fingerprint()
	return spec
}

// ParseSidebarAgentRow reads the generic table. A nil or empty table is the
// shipped row.
func ParseSidebarAgentRow(table map[string]any) SidebarAgentRowSpec {
	spec := DefaultSidebarAgentRow()
	if len(table) == 0 {
		return spec
	}
	problem := func(format string, args ...any) {
		spec.Problems = append(spec.Problems, fmt.Sprintf(format, args...))
	}
	for key, raw := range table {
		switch key {
		case "tokens":
			list, ok := stringList(raw)
			if !ok {
				problem("tokens must be a list of names in quotes, so the row keeps its default order")
				continue
			}
			var tokens []string
			for _, name := range list {
				name = strings.ToLower(strings.TrimSpace(name))
				switch {
				case !slices.Contains(SidebarAgentRowTokens, name):
					problem("there is no agent row token called %s. The names are %s",
						strconv.Quote(name), strings.Join(SidebarAgentRowTokens, ", "))
				case slices.Contains(tokens, name):
					problem("%s is listed twice in tokens, so the second one is ignored", name)
				default:
					tokens = append(tokens, name)
				}
			}
			if len(tokens) == 0 {
				problem("tokens names nothing the row can draw, so the row keeps its default order")
				continue
			}
			spec.Tokens = tokens
		default:
			if !slices.Contains(SidebarAgentRowTokens, key) {
				problem("there is no agent row token called %s. The names are %s",
					strconv.Quote(key), strings.Join(SidebarAgentRowTokens, ", "))
				continue
			}
			sub, ok := raw.(map[string]any)
			if !ok {
				problem("%s must be a table with fg, bold, dim and rule entries", key)
				continue
			}
			style := parseTokenStyle(key, sub, problem)
			if style.Base.Set() || len(style.Rules) > 0 {
				if spec.Styles == nil {
					spec.Styles = make(map[string]SidebarTokenStyle)
				}
				spec.Styles[key] = style
			}
		}
	}
	spec.Fingerprint = spec.fingerprint()
	return spec
}

// parseTokenStyle reads one token's table: its look, and its rule list.
func parseTokenStyle(token string, table map[string]any, problem func(string, ...any)) SidebarTokenStyle {
	var style SidebarTokenStyle
	style.Base = parseLook(token, table, problem)
	for key := range table {
		switch key {
		case "fg", "bold", "dim", "rule":
		default:
			problem("%s.%s is not a key the row reads. The keys are fg, bold, dim and rule", token, key)
		}
	}
	raw, ok := table["rule"]
	if !ok {
		return style
	}
	rules, ok := tableList(raw)
	if !ok {
		problem("%s.rule must be a list of [[...rule]] tables, so %s has no rules", token, token)
		return style
	}
	for i, rt := range rules {
		if len(style.Rules) >= SidebarAgentRowMaxRules {
			problem("%s has more than %d rules, so rule %d and the rest are ignored",
				token, SidebarAgentRowMaxRules, i+1)
			break
		}
		if rule, ok := parseRule(token, i+1, rt, problem); ok {
			style.Rules = append(style.Rules, rule)
		}
	}
	return style
}

// parseLook reads fg, bold and dim off a table.
func parseLook(where string, table map[string]any, problem func(string, ...any)) SidebarTokenLook {
	var look SidebarTokenLook
	if raw, ok := table["fg"]; ok {
		s, isString := raw.(string)
		s = strings.ToLower(strings.TrimSpace(s))
		switch {
		case !isString:
			problem("%s.fg must be a colour name or #rrggbb in quotes, so it is ignored", where)
		case slices.Contains(SidebarTokenColors, s), isHexColor(s):
			look.Fg = s
		default:
			problem("%s.fg is %s, which is not a colour the row knows. Use one of %s, or #rrggbb",
				where, strconv.Quote(s), strings.Join(SidebarTokenColors, ", "))
		}
	}
	for _, key := range []string{"bold", "dim"} {
		raw, ok := table[key]
		if !ok {
			continue
		}
		b, isBool := raw.(bool)
		if !isBool {
			problem("%s.%s must be true or false, so it is ignored", where, key)
			continue
		}
		v := b
		if key == "bold" {
			look.Bold = &v
		} else {
			look.Dim = &v
		}
	}
	return look
}

// parseRule reads one rule table: exactly one test, ignore_case, and a look.
func parseRule(token string, n int, table map[string]any, problem func(string, ...any)) (SidebarTokenRule, bool) {
	where := fmt.Sprintf("%s rule %d", token, n)
	var rule SidebarTokenRule
	var tests []string
	for _, key := range sidebarTokenTests {
		if _, ok := table[key]; ok {
			tests = append(tests, key)
		}
	}
	switch len(tests) {
	case 0:
		problem("%s has no test, so it is ignored. A rule needs exactly one of %s",
			where, strings.Join(sidebarTokenTests, ", "))
		return rule, false
	case 1:
	default:
		problem("%s has %s, so it is ignored. A rule needs exactly one test",
			where, strings.Join(tests, " and "))
		return rule, false
	}
	rule.Test = tests[0]
	raw := table[rule.Test]
	switch rule.Test {
	case "gt", "lt":
		f, ok := number(raw)
		if !ok {
			problem("%s: %s must be a number, so the rule is ignored", where, rule.Test)
			return rule, false
		}
		rule.Number = f
	default:
		s, ok := raw.(string)
		if !ok {
			problem("%s: %s must be text in quotes, so the rule is ignored", where, rule.Test)
			return rule, false
		}
		rule.Text = s
	}
	if raw, ok := table["ignore_case"]; ok {
		b, isBool := raw.(bool)
		if !isBool {
			problem("%s: ignore_case must be true or false, so it is ignored", where)
		}
		rule.IgnoreCase = isBool && b
	}
	for key := range table {
		switch key {
		case "fg", "bold", "dim", "ignore_case", "equals", "contains", "starts_with", "gt", "lt":
		default:
			problem("%s: %s is not a key a rule reads", where, key)
		}
	}
	rule.Look = parseLook(where, table, problem)
	if !rule.Look.Set() {
		problem("%s sets no fg, bold or dim, so it is ignored", where)
		return rule, false
	}
	return rule, true
}

// fingerprint writes the spec out in a fixed order, so two specs that draw the
// same rows fold to the same string.
func (s *SidebarAgentRowSpec) fingerprint() string {
	var b strings.Builder
	b.WriteString(strings.Join(s.Tokens, ","))
	names := make([]string, 0, len(s.Styles))
	for name := range s.Styles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		st := s.Styles[name]
		fmt.Fprintf(&b, ";%s=%s", name, lookString(st.Base))
		for _, r := range st.Rules {
			fmt.Fprintf(&b, "|%s:%s:%g:%t:%s", r.Test, r.Text, r.Number, r.IgnoreCase, lookString(r.Look))
		}
	}
	return b.String()
}

func lookString(l SidebarTokenLook) string {
	tri := func(p *bool) string {
		if p == nil {
			return "-"
		}
		return strconv.FormatBool(*p)
	}
	return l.Fg + "/" + tri(l.Bold) + "/" + tri(l.Dim)
}

// isHexColor reports whether s is #rgb or #rrggbb.
func isHexColor(s string) bool {
	if !strings.HasPrefix(s, "#") {
		return false
	}
	h := s[1:]
	if len(h) != 3 && len(h) != 6 {
		return false
	}
	_, err := strconv.ParseUint(h, 16, 32)
	return err == nil
}

// stringList reads a TOML array of strings.
func stringList(raw any) ([]string, bool) {
	switch v := raw.(type) {
	case []string:
		return v, true
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

// tableList reads a TOML array of tables, however the decoder spelled it.
func tableList(raw any) ([]map[string]any, bool) {
	switch v := raw.(type) {
	case []map[string]any:
		return v, true
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, e := range v {
			t, ok := e.(map[string]any)
			if !ok {
				return nil, false
			}
			out = append(out, t)
		}
		return out, true
	case map[string]any:
		// A single [token.rule] table rather than a [[token.rule]] entry: one
		// rule, written without the doubled brackets.
		return []map[string]any{v}, true
	}
	return nil, false
}

// number reads any numeric TOML value as a float.
func number(raw any) (float64, bool) {
	switch v := raw.(type) {
	case int64:
		return float64(v), true
	case int:
		return float64(v), true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		return v, true
	}
	return 0, false
}
