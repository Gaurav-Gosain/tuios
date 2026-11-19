package config

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// Arguments and ask steps of a user action.
//
//	[[actions]]
//	name = "deploy"
//	run = ["./deploy.sh", "{env}", "--replicas", "{replicas}"]
//	args = [{ name = "replicas", type = "number", default = 2 }]
//	ask = [{ name = "env", kind = "pick", title = "Deploy to?", items = ["staging", "prod=Production"] }]
//
// An argument has a name, a type (string, number or bool) and an optional
// default. An ask step asks the person for a value in the palette (tuios
// prompt) when nobody gave it: kind input shows a text field, and kind pick
// shows the items, each "id" or "id=title". Each name is a placeholder of
// run. A value comes from the caller (tuios actions run NAME key=value),
// else from the ask step, else from the default. An argument with none of
// the three stops the action before it runs.

// The types an argument can have.
const (
	ArgString = "string"
	ArgNumber = "number"
	ArgBool   = "bool"
)

// ActionArg is one typed argument of an action.
type ActionArg struct {
	Name    string `toml:"name"`
	Type    string `toml:"type,omitempty"`
	Default any    `toml:"default,omitempty"`
}

// ActionAsk is one ask step: it asks the person for the value of Name.
type ActionAsk struct {
	Name        string   `toml:"name"`
	Kind        string   `toml:"kind,omitempty"`
	Title       string   `toml:"title,omitempty"`
	Placeholder string   `toml:"placeholder,omitempty"`
	Items       []string `toml:"items,omitempty"`
	AllowOther  bool     `toml:"allow_other,omitempty"`
}

// argNameRE is a name an argument or an ask step may have.
var argNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// ArgType is the type of the value name: its argument's type, else string.
func (a UserAction) ArgType(name string) string {
	for _, g := range a.Args {
		if g.Name == name && g.Type != "" {
			return g.Type
		}
	}
	return ArgString
}

// ArgNames are the names of the action's arguments and ask steps, in the
// order they are asked and given.
func (a UserAction) ArgNames() []string {
	var out []string
	for _, g := range a.Args {
		out = append(out, g.Name)
	}
	for _, s := range a.Ask {
		if !slices.Contains(out, s.Name) {
			out = append(out, s.Name)
		}
	}
	return out
}

// CheckArg checks value against the type of name, and returns it as run
// takes it: a number as written, a bool as true or false.
func (a UserAction) CheckArg(name, value string) (string, error) {
	switch a.ArgType(name) {
	case ArgNumber:
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return "", errfmt.Errorf("the value of %s must be a number, not %q", name, value)
		}
	case ArgBool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return "", errfmt.Errorf("the value of %s must be true or false, not %q", name, value)
		}
		value = strconv.FormatBool(b)
	}
	return value, nil
}

// AsksFor reports whether a step of a asks for a value given has none for.
func (a UserAction) AsksFor(given map[string]string) bool {
	for _, s := range a.Ask {
		if _, ok := given[s.Name]; !ok {
			return true
		}
	}
	return false
}

// SplitAskItem is an item of a pick step as its id and its title.
func SplitAskItem(item string) (id, title string) {
	id, title, ok := strings.Cut(item, "=")
	if !ok {
		title = id
	}
	return id, title
}

// argsProblem is what is wrong with the arguments and ask steps of a, or "".
func argsProblem(a UserAction) string {
	if (len(a.Args) > 0 || len(a.Ask) > 0) && len(a.Run) == 0 {
		return "The values args and ask are only for run. Remove them."
	}
	var names []string
	for _, g := range a.Args {
		ok := argNameOK(g.Name, names) && (g.Type == "" || g.Type == ArgString || g.Type == ArgNumber || g.Type == ArgBool)
		if ok && g.Default != nil {
			_, err := a.CheckArg(g.Name, ArgDefault(g))
			ok = err == nil
		}
		if !ok {
			return "The argument " + strconv.Quote(g.Name) + " is not valid. Give each argument its own name of lowercase letters, digits and underscore, a type of string, number or bool, and a default of that type."
		}
		names = append(names, g.Name)
	}
	names = names[:0]
	for _, s := range a.Ask {
		pick := s.Kind == "pick"
		if !argNameOK(s.Name, names) || !pick && s.Kind != "" && s.Kind != "input" || pick == (len(s.Items) == 0) || !pick && s.AllowOther {
			return "The ask step " + strconv.Quote(s.Name) + " is not valid. Give each step its own name, and a kind of input or pick. A pick needs items, and only a pick takes items and allow_other."
		}
		names = append(names, s.Name)
	}
	return ""
}

// argNameOK reports whether name is a valid argument or step name that seen
// does not hold yet.
func argNameOK(name string, seen []string) bool {
	return argNameRE.MatchString(name) && !slices.Contains(UserActionPlaceholders, name) && !slices.Contains(seen, name)
}

// ArgDefault is the default of g as text, "" when it has none.
func ArgDefault(g ActionArg) string {
	switch v := g.Default.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(v, 10)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

// ErrActionAskEnded is an ask step that got no answer. Status is the
// prompt's status: cancelled, timeout, no_client or busy.
type ErrActionAskEnded struct{ Name, Status string }

func (e *ErrActionAskEnded) Error() string {
	return "the question for " + e.Name + " got no answer (" + strings.ReplaceAll(e.Status, "_", " ") + ")"
}

// ResolveArgs gives every argument and ask step of a a value: the one in
// given, else ask's answer to the step, else the default. ask returns the
// answer, or an error (an *ErrActionAskEnded when the person gave none). A
// given name the action does not have is an error, so a typo never runs an
// action with a default the caller did not mean.
func (a UserAction) ResolveArgs(given map[string]string, ask func(ActionAsk) (string, error)) (map[string]string, error) {
	names := a.ArgNames()
	out := map[string]string{}
	for k, v := range given {
		if !slices.Contains(names, k) {
			return nil, errfmt.Errorf("the action %s has no argument %s", a.Name, k)
		}
		checked, err := a.CheckArg(k, v)
		if err != nil {
			return nil, err
		}
		out[k] = checked
	}
	for _, s := range a.Ask {
		if _, ok := out[s.Name]; ok || ask == nil {
			continue
		}
		v, err := ask(s)
		if err != nil {
			return nil, err
		}
		if out[s.Name], err = a.CheckArg(s.Name, v); err != nil {
			return nil, err
		}
	}
	for _, g := range a.Args {
		if _, ok := out[g.Name]; !ok && g.Default != nil {
			out[g.Name] = ArgDefault(g)
		}
	}
	for _, n := range names {
		if _, ok := out[n]; !ok {
			return nil, errfmt.Errorf("the action %s needs a value for %s. Give %s=VALUE", a.Name, n, n)
		}
	}
	return out, nil
}
