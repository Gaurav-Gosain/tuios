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

// Selector keys. They are wire values.
const (
	SelectorHarness = "harness"
	SelectorState   = "state"
	// SelectorStateSilent is the state term value that picks the silent
	// panes. It is not an agent state: a silent pane's state is unknown.
	SelectorStateSilent = "silent"
	SelectorNeeds       = "needs"
	SelectorSession     = "session"
	SelectorGroup       = "group"
	SelectorHost        = "host"
	SelectorName        = "name"
	SelectorCwd         = "cwd"
	SelectorTask        = "task"
	SelectorStage       = "stage"
	SelectorParent      = "parent"
	SelectorRoot        = "root"
	SelectorRole        = "role"
)

// SelectorKeys lists the keys a selector takes, in the order the docs give
// them.
var SelectorKeys = []string{SelectorHarness, SelectorState, SelectorNeeds, SelectorSession, SelectorGroup, SelectorHost, SelectorName, SelectorCwd, SelectorTask, SelectorStage, SelectorParent, SelectorRoot, SelectorRole}

const selectorMaxLen = 1024

const selectorMaxTerms = 16

// Selector is a parsed selector. The zero value matches nothing; build one
// with ParseSelector.
type Selector struct {
	text  string
	terms []selectorTerm
}

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
	// Silent is a pane whose agent said working and then nothing for longer
	// than silent_after. Its State is unknown. See agent_silent.go.
	Silent bool
	// Tasks are the ids of the task cards the pane holds. Only a pane of
	// this machine fills it, so a task term matches nothing else.
	Tasks []string
	// Stages are the stages of those cards.
	Stages []string
	// Parent and Root are the window ids of the agent that started the pane
	// and of the first agent of its chain, empty for a pane no agent
	// started. Only a pane of this machine fills them.
	Parent string
	Root   string
	// Role is the role of a child agent delegate started, empty for every
	// other pane.
	Role string
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
				if !slices.Contains(AgentStateNames, v) && v != SelectorStateSilent {
					return nil, errors.New("state " + echoName(v) + " is not an agent state. The states are " + strings.Join(AgentStateNames, ", ") + ", and silent")
				}
			case SelectorRole:
				v = strings.ToLower(v)
				if !slices.Contains(delegateRoles, v) {
					return nil, errors.New("role " + echoName(v) + " is not a role. The roles are " + strings.Join(delegateRoles, ", "))
				}
			case SelectorStage:
				v = strings.ToLower(v)
				if !slices.Contains(TaskStages, v) {
					return nil, errors.New("stage " + echoName(v) + " is not a task stage. The stages are " + strings.Join(TaskStages, ", "))
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
		id := strings.ToLower(t.Harness)
		return glob(id) || (id != "" && !strings.ContainsAny(v, "*?[") && strings.HasPrefix(id, v+"-"))
	case SelectorState:
		return t.State == v || (t.Silent && (v == AgentStateWorking.Name() || v == SelectorStateSilent))
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
	case SelectorTask:
		return slices.ContainsFunc(t.Tasks, glob)
	case SelectorStage:
		return slices.Contains(t.Stages, v)
	case SelectorParent:
		return windowIDMatch(v, t.Parent)
	case SelectorRoot:
		return windowIDMatch(v, t.Root)
	case SelectorRole:
		return v == t.Role
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

func windowIDMatch(v, id string) bool {
	if id == "" {
		return false
	}
	if strings.ContainsAny(v, "*?[") {
		ok, _ := path.Match(v, id)
		return ok
	}
	return strings.HasPrefix(id, v)
}

// SelectionToken is the confirm token for a set of targets, each named by a
// key that identifies one pane for good (selectionKeys), in any order. It is
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
