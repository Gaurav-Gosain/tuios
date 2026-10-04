package lessons

import (
	"fmt"
	"regexp"
	"strings"
)

// Event is one event from internal/learn, in the shape the matchers read. It
// is the same object the browser page receives: {type, data, state}.
type Event struct {
	Type  string
	Data  map[string]any
	State map[string]any
}

// Ctx is the memory a step keeps across events. It mirrors MatchContext in
// the page's engine.
type Ctx struct {
	// LastAction is the registry action the last key ran, "" if none.
	LastAction string
	// State is the snapshot after the last state event.
	State map[string]any
	// Mem is per-step scratch space for matchers that count.
	Mem map[string]int
}

// Matcher decides when a step is done. Exactly one of the rule fields is set.
// Each rule is the JSON form of the function of the same name in the page's
// lib/learn/matchers.ts:
//
//	{"on": "window.open"}                          on("window.open")
//	{"on": "action", "where": {"data.name": {"prefix": "preselect_"}}}
//	                                               on("action", e => name.startsWith(...))
//	{"changed": "mode", "to": "terminal"}          changed("mode", "terminal")
//	{"opened": "help"} / {"closed": "help"}        opened / closed
//	{"ran": "neofetch"}                            ran("neofetch")
//	{"ranLine": "^cat\\s+demo"}                   ranLine(/^cat\s+demo/)
//	{"agent": "working"}                           agent("working")
//	{"via": ["rotate_split"], "then": M}           via([...], M)
//	{"any": [M, ...]} / {"seq": [M, ...]}          any / seq
//	{"times": 2, "of": M}                          times(2, M)
//	{"zoomed": true} / {"moved": true}             zoomed(true) / moved()
//	{"notified": "^Yanked"}                        notified(/^Yanked/)
//	{"setting": "glyphs"}                          setting("glyphs")
//
// A where value is a literal to compare, {"prefix": s}, or {"truthy": b}.
// Its path is "data.x", "state.x", or "window(data.to).agent", a field of
// the window in state.windowList whose id is data.to.
type Matcher struct {
	// On: an event of this type, optionally with Where checks on it.
	On    string         `json:"on,omitempty"`
	Where map[string]any `json:"where,omitempty"`
	// Changed: a state event of this type whose data.to equals To.
	Changed string `json:"changed,omitempty"`
	To      any    `json:"to,omitempty"`
	// Opened and Closed: an overlay by name.
	Opened string `json:"opened,omitempty"`
	Closed string `json:"closed,omitempty"`
	// Ran: the fake shell finished this command and knew it.
	Ran string `json:"ran,omitempty"`
	// RanLine: the fake shell finished a line matching this pattern.
	RanLine string `json:"ranLine,omitempty"`
	// Agent: a pane's agent moved to this state.
	Agent string `json:"agent,omitempty"`
	// Via and Then: Then, caused by one of these registry actions.
	Via  []string `json:"via,omitempty"`
	Then *Matcher `json:"then,omitempty"`
	// Any: one of these.
	Any []*Matcher `json:"any,omitempty"`
	// Times and Of: Of, this many times over the step.
	Times int      `json:"times,omitempty"`
	Of    *Matcher `json:"of,omitempty"`
	// Seq: each in turn, done when the last matches.
	Seq []*Matcher `json:"seq,omitempty"`
	// Zoomed: the focused window zoomed in (true) or out (false).
	Zoomed *bool `json:"zoomed,omitempty"`
	// Moved: a window moved or changed size, once settled.
	Moved bool `json:"moved,omitempty"`
	// Notified: a dock note whose text matches this pattern.
	Notified string `json:"notified,omitempty"`
	// Setting: a look the settings page changed, such as "glyphs".
	Setting string `json:"setting,omitempty"`

	re *regexp.Regexp
}

func (m *Matcher) check() error {
	if m == nil {
		return fmt.Errorf("empty matcher")
	}
	n := 0
	for _, set := range []bool{
		m.On != "", m.Changed != "", m.Opened != "", m.Closed != "", m.Ran != "",
		m.RanLine != "", m.Agent != "", m.Via != nil, m.Any != nil, m.Times > 0,
		m.Seq != nil, m.Zoomed != nil, m.Moved, m.Notified != "", m.Setting != "",
	} {
		if set {
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("matcher sets %d rules, want exactly 1", n)
	}
	for _, p := range []string{m.RanLine, m.Notified} {
		if p != "" {
			re, err := regexp.Compile(p)
			if err != nil {
				return err
			}
			m.re = re
		}
	}
	for path, want := range m.Where {
		if _, ok := want.(map[string]any); ok {
			w := want.(map[string]any)
			if _, p := w["prefix"]; !p {
				if _, t := w["truthy"]; !t {
					return fmt.Errorf("where %s: want a value, {prefix} or {truthy}", path)
				}
			}
		}
	}
	for _, sub := range append(append([]*Matcher{m.Then, m.Of}, m.Any...), m.Seq...) {
		if sub == nil {
			continue
		}
		if err := sub.check(); err != nil {
			return err
		}
	}
	if (m.Via != nil) != (m.Then != nil) {
		return fmt.Errorf("via needs then")
	}
	if (m.Times > 0) != (m.Of != nil) {
		return fmt.Errorf("times needs of")
	}
	return nil
}

// Match reports whether e completes the matcher. It may write to ctx.Mem.
func (m *Matcher) Match(e Event, ctx *Ctx) bool {
	switch {
	case m.On != "":
		return e.Type == m.On && where(m.Where, e)
	case m.Changed != "":
		return e.Type == m.Changed && same(e.Data["to"], m.To)
	case m.Opened != "":
		return e.Type == "overlay.open" && e.Data["name"] == m.Opened
	case m.Closed != "":
		return e.Type == "overlay.close" && e.Data["name"] == m.Closed
	case m.Ran != "":
		return e.Type == "shell.command" && e.Data["command"] == m.Ran && !same(e.Data["exitCode"], 127)
	case m.RanLine != "":
		line, _ := e.Data["line"].(string)
		return e.Type == "shell.command" && m.re.MatchString(line)
	case m.Agent != "":
		return e.Type == "agent" && e.Data["to"] == m.Agent
	case m.Via != nil:
		if !m.Then.Match(e, ctx) {
			return false
		}
		for _, a := range m.Via {
			if a == ctx.LastAction {
				return true
			}
		}
		return false
	case m.Any != nil:
		for _, sub := range m.Any {
			if sub.Match(e, ctx) {
				return true
			}
		}
		return false
	case m.Times > 0:
		if !m.Of.Match(e, ctx) {
			return false
		}
		ctx.Mem["count"]++
		return ctx.Mem["count"] >= m.Times
	case m.Seq != nil:
		at := ctx.Mem["seq"]
		if at >= len(m.Seq) || !m.Seq[at].Match(e, ctx) {
			return false
		}
		ctx.Mem["seq"] = at + 1
		return ctx.Mem["seq"] >= len(m.Seq)
	case m.Zoomed != nil:
		z, _ := e.Data["zoomed"].(bool)
		return e.Type == "window.zoom" && z == *m.Zoomed
	case m.Moved:
		return e.Type == "window.move"
	case m.Notified != "":
		msg, _ := e.Data["message"].(string)
		return e.Type == "notification" && m.re.MatchString(msg)
	case m.Setting != "":
		return e.Type == "setting" && e.Data["name"] == m.Setting
	}
	return false
}

// where checks each path against its wanted value. A path is "data.x" or
// "state.x", or "window(data.to).agent": a field of the window in
// state.windowList whose id is the value at the inner path.
func where(w map[string]any, e Event) bool {
	for path, want := range w {
		got := lookup(path, e)
		switch v := want.(type) {
		case map[string]any:
			if p, ok := v["prefix"].(string); ok {
				s, _ := got.(string)
				if !strings.HasPrefix(s, p) {
					return false
				}
			}
			if t, ok := v["truthy"].(bool); ok && truthy(got) != t {
				return false
			}
		default:
			if !same(got, want) {
				return false
			}
		}
	}
	return true
}

func lookup(path string, e Event) any {
	if inner, field, ok := strings.Cut(strings.TrimPrefix(path, "window("), ")."); ok && strings.HasPrefix(path, "window(") {
		id := lookup(inner, e)
		list, _ := e.State["windowList"].([]any)
		for _, w := range list {
			if wm, ok := w.(map[string]any); ok && same(wm["id"], id) {
				return wm[field]
			}
		}
		return nil
	}
	root, key, _ := strings.Cut(path, ".")
	switch root {
	case "data":
		return e.Data[key]
	case "state":
		return e.State[key]
	}
	return nil
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case float64:
		return x != 0
	}
	return true
}

// same compares a Go value from the app with a JSON value from the file,
// where every number is a float64.
func same(a, b any) bool {
	if fa, ok := number(a); ok {
		fb, ok := number(b)
		return ok && fa == fb
	}
	return a == b
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}
