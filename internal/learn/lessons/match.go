package lessons

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Event is one event from internal/learn, in the shape a rule reads: the
// same object the page receives, {type, data, state}.
type Event struct {
	Type  string         `json:"type"`
	Data  map[string]any `json:"data,omitempty"`
	State map[string]any `json:"state,omitempty"`
}

// Ctx is what a step remembers across its events.
type Ctx struct {
	// LastAction is the registry action tuios ran since the last key, from
	// a key, a prefix chord or a menu row. "" when the last key ran none.
	LastAction string
	// State is the snapshot after the last event that carried one.
	State map[string]any
	// Mem is scratch space for the rules that count.
	Mem map[string]int
}

// NewCtx is an empty context with state carried over.
func NewCtx(state map[string]any) Ctx { return Ctx{State: state, Mem: map[string]int{}} }

// Rule decides when a step is done. Exactly one rule field is set:
//
//	{"on": "window.open"}                         an event of this type
//	{"on": "keys", "where": {"data.to.preset": "tmux"}}
//	{"changed": "mode", "to": "terminal"}         a state event whose data.to is this
//	{"opened": "help"}, {"closed": "help"}        an overlay opened or closed
//	{"ran": "ls"}                                 the shell ran a command it knows
//	{"ranLine": "^cat\\s+demo"}                   the shell ran a line that matches
//	{"agent": "working"}                          a pane's agent moved to this state
//	{"via": ["rotate_split"], "then": R}          R, caused by one of these actions
//	{"any": [R, ...]}                             one of these
//	{"seq": [R, ...]}                             each in turn, done at the last
//	{"times": 2, "of": R}                         R, this many times
//	{"zoomed": true}, {"moved": true}             a zoom, or a settled move
//	{"notified": "^Saved"}                        a dock note that matches
//	{"setting": "glyphs"}                         a look changed
//
// A via entry is an action name or {"prefix": "workspace_prefix_move_"}.
//
// A where key is a path: "data.x", "state.x", deeper ("data.to.preset"), or
// "window(data.to).agent", a field of the window in state.windowList whose
// id is at the inner path. Its value is a literal, or an object whose checks
// must all hold:
//
//	{"prefix": s}, {"suffix": s}    the string starts or ends with s
//	{"match": re}                   the string matches the RE2 pattern
//	{"truthy": b}                   the value is set (not "", 0, false or missing), or not
//	{"not": v}                      the value is not v
//	{"differs": path}               the value is not the one at path
//
// Patterns are RE2, a subset of JavaScript's, so the page reads them too.
type Rule struct {
	On      string         `json:"on,omitempty"`
	Where   map[string]any `json:"where,omitempty"`
	Changed string         `json:"changed,omitempty"`
	To      any            `json:"to,omitempty"`
	Opened  string         `json:"opened,omitempty"`
	Closed  string         `json:"closed,omitempty"`
	Ran     string         `json:"ran,omitempty"`
	RanLine string         `json:"ranLine,omitempty"`
	Agent   string         `json:"agent,omitempty"`
	Via     []ActionRef    `json:"via,omitempty"`
	Then    *Rule          `json:"then,omitempty"`
	Any     []*Rule        `json:"any,omitempty"`
	Seq     []*Rule        `json:"seq,omitempty"`
	Times   int            `json:"times,omitempty"`
	Of      *Rule          `json:"of,omitempty"`
	Zoomed  *bool          `json:"zoomed,omitempty"`
	Moved   bool           `json:"moved,omitempty"`
	// Notified matches dock text, which a copy change breaks without a
	// sound. Prefer an event.
	Notified string `json:"notified,omitempty"`
	Setting  string `json:"setting,omitempty"`

	re    *regexp.Regexp
	where []whereCheck
	// slot is where this rule keeps its count or place in Ctx.Mem. A rule
	// inside another that counts gets its own, so two never share one.
	slot string
}

// ActionRef is one entry of a via list: an action name, or every action
// whose name starts with Prefix.
type ActionRef struct {
	Name   string
	Prefix string
}

// UnmarshalJSON reads "name" or {"prefix": "..."}.
func (a *ActionRef) UnmarshalJSON(b []byte) error {
	if err := json.Unmarshal(b, &a.Name); err == nil {
		return nil
	}
	var p struct {
		Prefix string `json:"prefix"`
	}
	if err := json.Unmarshal(b, &p); err != nil || p.Prefix == "" {
		return fmt.Errorf(`a via entry is an action name or {"prefix": ...}`)
	}
	a.Prefix = p.Prefix
	return nil
}

// MarshalJSON writes the form UnmarshalJSON reads.
func (a ActionRef) MarshalJSON() ([]byte, error) {
	if a.Prefix != "" {
		return json.Marshal(map[string]string{"prefix": a.Prefix})
	}
	return json.Marshal(a.Name)
}

// Matches reports whether action is this entry.
func (a ActionRef) Matches(action string) bool {
	if a.Prefix != "" {
		return strings.HasPrefix(action, a.Prefix)
	}
	return action == a.Name
}

type whereCheck struct {
	path string
	want any // a literal, or nil when ops is set
	ops  *valueOps
}

type valueOps struct {
	prefix, suffix *string
	match          *regexp.Regexp
	truthy         *bool
	not            any
	hasNot         bool
	differs        string
}

// Compile checks the rule and readies it to match. Load does it for every
// rule in the lessons.
func (r *Rule) Compile() error { return r.compile() }

func (r *Rule) compile() error { return r.compileAt("r") }

func (r *Rule) compileAt(slot string) error {
	if r == nil {
		return fmt.Errorf("empty rule")
	}
	r.slot = slot
	n := 0
	for _, set := range []bool{
		r.On != "", r.Changed != "", r.Opened != "", r.Closed != "", r.Ran != "",
		r.RanLine != "", r.Agent != "", r.Via != nil, r.Any != nil, r.Seq != nil,
		r.Times > 0, r.Zoomed != nil, r.Moved, r.Notified != "", r.Setting != "",
	} {
		if set {
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("a rule sets %d kinds, want exactly 1", n)
	}
	if r.Where != nil && r.On == "" {
		return fmt.Errorf("where goes with on")
	}
	if (r.Via != nil) != (r.Then != nil) {
		return fmt.Errorf("via needs then")
	}
	if (r.Times > 0) != (r.Of != nil) {
		return fmt.Errorf("times needs of")
	}
	if r.To != nil && r.Changed == "" {
		return fmt.Errorf("to goes with changed")
	}
	for _, p := range []string{r.RanLine, r.Notified} {
		if p == "" {
			continue
		}
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("pattern %q: %w", p, err)
		}
		r.re = re
	}
	r.where = r.where[:0]
	for path, want := range r.Where {
		if err := checkPath(path); err != nil {
			return err
		}
		c := whereCheck{path: path, want: want}
		if m, ok := want.(map[string]any); ok {
			ops, err := compileOps(path, m)
			if err != nil {
				return err
			}
			c.want, c.ops = nil, ops
		}
		r.where = append(r.where, c)
	}
	subs := []*Rule{r.Then, r.Of}
	subs = append(subs, r.Any...)
	subs = append(subs, r.Seq...)
	for i, sub := range subs {
		if sub == nil {
			continue
		}
		if err := sub.compileAt(fmt.Sprintf("%s.%d", slot, i)); err != nil {
			return err
		}
	}
	return nil
}

func compileOps(path string, m map[string]any) (*valueOps, error) {
	ops := &valueOps{}
	for k, v := range m {
		s, isStr := v.(string)
		switch k {
		case "prefix", "suffix", "match", "differs":
			if !isStr {
				return nil, fmt.Errorf("where %s: %s wants a string", path, k)
			}
		}
		switch k {
		case "prefix":
			ops.prefix = &s
		case "suffix":
			ops.suffix = &s
		case "match":
			re, err := regexp.Compile(s)
			if err != nil {
				return nil, fmt.Errorf("where %s: pattern %q: %w", path, s, err)
			}
			ops.match = re
		case "differs":
			if err := checkPath(s); err != nil {
				return nil, err
			}
			ops.differs = s
		case "truthy":
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("where %s: truthy wants true or false", path)
			}
			ops.truthy = &b
		case "not":
			ops.not, ops.hasNot = v, true
		default:
			return nil, fmt.Errorf("where %s: unknown check %q", path, k)
		}
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("where %s: an empty check", path)
	}
	return ops, nil
}

func checkPath(path string) error {
	inner, _, isWindow := windowPath(path)
	if isWindow {
		path = inner
	}
	root, rest, _ := strings.Cut(path, ".")
	if (root != "data" && root != "state") || rest == "" {
		return fmt.Errorf("path %q: want data.x, state.x or window(data.x).y", path)
	}
	return nil
}

// Walk calls fn on the rule and on every rule inside it, for the lint.
func (r *Rule) Walk(fn func(*Rule)) {
	if r == nil {
		return
	}
	fn(r)
	r.Then.Walk(fn)
	r.Of.Walk(fn)
	for _, s := range r.Any {
		s.Walk(fn)
	}
	for _, s := range r.Seq {
		s.Walk(fn)
	}
}

// Match reports whether e completes the rule. A rule that counts keeps its
// place in ctx.Mem.
func (r *Rule) Match(e Event, ctx *Ctx) bool {
	switch {
	case r.On != "":
		return e.Type == r.On && r.whereHolds(e)
	case r.Changed != "":
		return e.Type == r.Changed && same(e.Data["to"], r.To)
	case r.Opened != "":
		return e.Type == "overlay.open" && e.Data["name"] == r.Opened
	case r.Closed != "":
		return e.Type == "overlay.close" && e.Data["name"] == r.Closed
	case r.Ran != "":
		return e.Type == "shell.command" && e.Data["command"] == r.Ran && !same(e.Data["exitCode"], 127)
	case r.RanLine != "":
		return e.Type == "shell.command" && r.re.MatchString(str(e.Data["line"]))
	case r.Agent != "":
		return e.Type == "agent" && e.Data["to"] == r.Agent
	case r.Via != nil:
		if !r.Then.Match(e, ctx) {
			return false
		}
		for _, a := range r.Via {
			if a.Matches(ctx.LastAction) {
				return true
			}
		}
		return false
	case r.Any != nil:
		for _, sub := range r.Any {
			if sub.Match(e, ctx) {
				return true
			}
		}
		return false
	case r.Times > 0:
		if !r.Of.Match(e, ctx) {
			return false
		}
		ctx.Mem[r.slot]++
		return ctx.Mem[r.slot] >= r.Times
	case r.Seq != nil:
		at := ctx.Mem[r.slot]
		if at >= len(r.Seq) || !r.Seq[at].Match(e, ctx) {
			return false
		}
		ctx.Mem[r.slot] = at + 1
		return at+1 >= len(r.Seq)
	case r.Zoomed != nil:
		z, _ := e.Data["zoomed"].(bool)
		return e.Type == "window.zoom" && z == *r.Zoomed
	case r.Moved:
		return e.Type == "window.move"
	case r.Notified != "":
		return e.Type == "notification" && r.re.MatchString(str(e.Data["message"]))
	case r.Setting != "":
		return e.Type == "setting" && e.Data["name"] == r.Setting
	}
	return false
}

func (r *Rule) whereHolds(e Event) bool {
	for _, c := range r.where {
		got := lookup(c.path, e)
		if c.ops == nil {
			if !same(got, c.want) {
				return false
			}
			continue
		}
		o := c.ops
		s, _ := got.(string)
		if o.prefix != nil && !strings.HasPrefix(s, *o.prefix) {
			return false
		}
		if o.suffix != nil && !strings.HasSuffix(s, *o.suffix) {
			return false
		}
		if o.match != nil && !o.match.MatchString(str(got)) {
			return false
		}
		if o.truthy != nil && truthy(got) != *o.truthy {
			return false
		}
		if o.hasNot && same(got, o.not) {
			return false
		}
		if o.differs != "" && same(got, lookup(o.differs, e)) {
			return false
		}
	}
	return true
}

// windowPath splits "window(data.to).agent" into "data.to" and "agent".
func windowPath(path string) (inner, field string, ok bool) {
	rest, found := strings.CutPrefix(path, "window(")
	if !found {
		return "", "", false
	}
	inner, field, ok = strings.Cut(rest, ").")
	return inner, field, ok && inner != "" && field != ""
}

func lookup(path string, e Event) any {
	if inner, field, ok := windowPath(path); ok {
		id := lookup(inner, e)
		list, _ := e.State["windowList"].([]any)
		for _, w := range list {
			if wm, ok := w.(map[string]any); ok && same(wm["id"], id) {
				return wm[field]
			}
		}
		return nil
	}
	root, rest, _ := strings.Cut(path, ".")
	var cur any
	switch root {
	case "data":
		cur = e.Data
	case "state":
		cur = e.State
	default:
		return nil
	}
	for key := range strings.SplitSeq(rest, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[key]
	}
	return cur
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

// truthy is JavaScript's Boolean(v) for the values an event carries.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	}
	if f, ok := number(v); ok {
		return f != 0
	}
	return true
}

// same compares a value from the app with one from a lesson file, where
// every number is a float64.
func same(a, b any) bool {
	if fa, ok := number(a); ok {
		fb, ok := number(b)
		return ok && fa == fb
	}
	switch a.(type) {
	case map[string]any, []any:
		return false
	}
	switch b.(type) {
	case map[string]any, []any:
		return false
	}
	return a == b
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}
