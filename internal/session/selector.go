package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path"
	"slices"
	"sort"
	"strings"
)

// Selectors: one way to address many panes at once.
//
// Every verb addresses a window by id, index, prefix or name, which is right
// for one pane and wrong for "every codex pane on build" or "every agent of
// this fan-out that needs me". A selector is a short line of terms read the
// same way by every verb that takes one:
//
//	harness:codex state:idle,done session:api-fan-*
//
// Terms are separated by spaces and all of them must match (AND). A term is a
// key, a colon and one or more values separated by commas, any of which may
// match (OR). The keys:
//
//	harness:ID   the harness id, or a program name a manifest detects (claude)
//	state:S      the agent state, one of AgentStateNames
//	needs:you    a pane a person has to act on (needs_input or errored)
//	session:G    the session name, a glob
//	group:G      the worktree fan-out group (the branch stem), a glob
//	host:G       the machine, "local" for this one, a glob
//	name:G       the window's name, a glob
//	cwd:DIR      the working directory is DIR or under it; ~ is the home dir
//
// A glob is path.Match syntax: * and ? do not cross a slash, so group:fan/*
// matches every group under fan/.
//
// Reading verbs (list-agents, list-host-agents, list-attention, wait-for)
// take a selector as a filter. Writing verbs (send-agent-message, ask-agent)
// take one only with the confirm token the resolved set hashes to, so a
// selector never turns into a broadcast nobody looked at: the first call
// without a token sends nothing and answers with the targets and the token.
// See resolveSelection.

// Selector keys. They are wire values.
const (
	SelectorHarness = "harness"
	SelectorState   = "state"
	SelectorNeeds   = "needs"
	SelectorSession = "session"
	SelectorGroup   = "group"
	SelectorHost    = "host"
	SelectorName    = "name"
	SelectorCwd     = "cwd"
)

// SelectorKeys lists the keys a selector takes, in the order the docs give
// them.
var SelectorKeys = []string{SelectorHarness, SelectorState, SelectorNeeds, SelectorSession, SelectorGroup, SelectorHost, SelectorName, SelectorCwd}

// selectorMaxLen bounds a selector's text. A selector is typed by a person or
// written by an agent, and one longer than this is not a selector.
const selectorMaxLen = 1024

// selectorMaxTerms bounds the terms and the values of one term.
const selectorMaxTerms = 16

// Selector is a parsed selector. The zero value matches nothing; build one
// with ParseSelector.
type Selector struct {
	text  string
	terms []selectorTerm
}

// selectorTerm is one key and the values any of which may match.
type selectorTerm struct {
	key    string
	values []string
}

// SelectorTarget is what a selector reads from one pane or one Inbox item. A
// field left empty matches no term on that key, so a term the source cannot
// answer excludes the target rather than including it by accident.
type SelectorTarget struct {
	// Host is the machine, "local" (or empty) for this one.
	Host     string
	Session  string
	Name     string
	State    string
	Harness  string
	Cwd      string
	Group    string
	NeedsYou bool
}

// ParseSelector reads a selector. homeDir expands a leading ~ in a cwd term;
// pass "" to leave it as written.
func ParseSelector(text, homeDir string) (*Selector, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("the selector is empty: write at least one term, such as harness:codex")
	}
	if len(text) > selectorMaxLen {
		return nil, errors.New("the selector is longer than 1024 bytes")
	}
	fields := strings.Fields(text)
	if len(fields) > selectorMaxTerms {
		return nil, errors.New("a selector takes at most 16 terms")
	}
	sel := &Selector{text: strings.Join(fields, " ")}
	for _, f := range fields {
		key, raw, ok := strings.Cut(f, ":")
		if !ok || key == "" {
			return nil, errors.New("term " + echoName(f) + " is not key:value. The keys are " + strings.Join(SelectorKeys, ", "))
		}
		key = strings.ToLower(key)
		if !slices.Contains(SelectorKeys, key) {
			return nil, errors.New("term " + echoName(f) + ": " + echoName(key) + " is not a selector key. The keys are " + strings.Join(SelectorKeys, ", "))
		}
		var values []string
		for v := range strings.SplitSeq(raw, ",") {
			if v = strings.TrimSpace(v); v != "" {
				values = append(values, v)
			}
		}
		if len(values) == 0 {
			return nil, errors.New("term " + echoName(f) + " has no value")
		}
		if len(values) > selectorMaxTerms {
			return nil, errors.New("term " + echoName(f) + " has more than 16 values")
		}
		for i, v := range values {
			switch key {
			case SelectorState:
				v = strings.ToLower(v)
				if !slices.Contains(AgentStateNames, v) {
					return nil, errors.New("state " + echoName(v) + " is not an agent state. The states are " + strings.Join(AgentStateNames, ", "))
				}
			case SelectorNeeds:
				v = strings.ToLower(v)
				if v != "you" {
					return nil, errors.New("needs takes one value, you: needs:you")
				}
			case SelectorCwd:
				if homeDir != "" && (v == "~" || strings.HasPrefix(v, "~/")) {
					v = homeDir + v[1:]
				}
				if v != "/" {
					v = strings.TrimRight(v, "/")
				}
			case SelectorHarness:
				v = strings.ToLower(v)
				fallthrough
			default:
				if _, err := path.Match(v, ""); err != nil {
					return nil, errors.New("term " + echoName(f) + ": " + echoName(v) + " is not a valid glob")
				}
			}
			values[i] = v
		}
		sel.terms = append(sel.terms, selectorTerm{key: key, values: values})
	}
	return sel, nil
}

// String is the selector as parsed, with its terms separated by one space.
func (s *Selector) String() string {
	if s == nil {
		return ""
	}
	return s.text
}

// Has reports whether the selector has a term on key.
func (s *Selector) Has(key string) bool {
	if s == nil {
		return false
	}
	for _, t := range s.terms {
		if t.key == key {
			return true
		}
	}
	return false
}

// mapValues rewrites every value of the terms on key. The daemon uses it to
// turn a program name into the harness id it names, and this machine's own
// name into local.
func (s *Selector) mapValues(key string, f func(string) string) {
	if s == nil {
		return
	}
	for i := range s.terms {
		if s.terms[i].key != key {
			continue
		}
		for j, v := range s.terms[i].values {
			s.terms[i].values[j] = f(v)
		}
	}
}

// Match reports whether every term matches the target.
func (s *Selector) Match(t SelectorTarget) bool {
	if s == nil || len(s.terms) == 0 {
		return false
	}
	for _, term := range s.terms {
		if !term.match(t) {
			return false
		}
	}
	return true
}

func (term selectorTerm) match(t SelectorTarget) bool {
	for _, v := range term.values {
		if term.matchOne(v, t) {
			return true
		}
	}
	return false
}

func (term selectorTerm) matchOne(v string, t SelectorTarget) bool {
	glob := func(s string) bool {
		if s == "" {
			return false
		}
		ok, _ := path.Match(v, s)
		return ok
	}
	switch term.key {
	case SelectorHarness:
		// A bare word also names the harness it starts: claude is
		// claude-code and gemini is gemini-cli. The daemon maps a program
		// name through the manifests first (parseVerbSelector); this is what
		// a reader without them, the Inbox filter, relies on.
		id := strings.ToLower(t.Harness)
		return glob(id) || (id != "" && !strings.ContainsAny(v, "*?[") && strings.HasPrefix(id, v+"-"))
	case SelectorState:
		return t.State == v
	case SelectorNeeds:
		return t.NeedsYou
	case SelectorSession:
		return glob(t.Session)
	case SelectorGroup:
		return glob(t.Group)
	case SelectorHost:
		host := t.Host
		if host == "" {
			host = localAttentionHost
		}
		return glob(host)
	case SelectorName:
		return glob(t.Name)
	case SelectorCwd:
		if t.Cwd == "" {
			return false
		}
		cwd := t.Cwd
		if cwd != "/" {
			cwd = strings.TrimRight(cwd, "/")
		}
		return cwd == v || v == "/" || strings.HasPrefix(cwd, v+"/")
	}
	return false
}

// SelectionToken is the confirm token for a set of targets, each named by a
// key that identifies one pane for good (selectionKey), in any order. It is
// not a secret. It says the caller saw this exact set, so a write that names
// it goes to that set and no other: a pane that joined or left the selection
// between the look and the write changes the token, and the write is refused.
// list-agents hands out the same token for the same set, so a caller can look
// with list-agents and write with the token it printed.
func SelectionToken(keys []string) string {
	sorted := slices.Clone(keys)
	sort.Strings(sorted)
	h := sha256.New()
	for _, k := range sorted {
		h.Write([]byte(k))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
