package config

import (
	"errors"
	"strings"
	"testing"
)

func TestUserActionArgsResolve(t *testing.T) {
	a := UserAction{
		Name: "deploy",
		Run:  []string{"deploy", "{env}", "{replicas}", "{dry}"},
		Args: []ActionArg{{Name: "replicas", Type: ArgNumber, Default: int64(2)}, {Name: "dry", Type: ArgBool, Default: false}},
		Ask:  []ActionAsk{{Name: "env", Kind: "pick", Items: []string{"stg=Staging"}}},
	}
	if p := argsProblem(a); p != "" {
		t.Fatalf("a valid action: %s", p)
	}
	asked := 0
	ask := func(s ActionAsk) (string, error) { asked++; return "stg", nil }
	got, err := a.ResolveArgs(map[string]string{"dry": "1"}, ask)
	if err != nil || got["env"] != "stg" || got["replicas"] != "2" || got["dry"] != "true" || asked != 1 {
		t.Fatalf("ResolveArgs = %v, %v (asked %d), want env stg, replicas 2, dry true", got, err, asked)
	}
	if _, err := a.ResolveArgs(map[string]string{"env": "x", "replicas": "many"}, ask); err == nil || !strings.Contains(err.Error(), "number") {
		t.Errorf("a number that is not one: %v", err)
	}
	if _, err := a.ResolveArgs(map[string]string{"env": "x", "colour": "red"}, ask); err == nil {
		t.Error("an argument the action does not have was taken")
	}
	if _, err := a.ResolveArgs(nil, nil); err == nil || !strings.Contains(err.Error(), "env") {
		t.Errorf("no ask and no value for env: %v", err)
	}
	ended := &ErrActionAskEnded{Name: "env", Status: "cancelled"}
	if _, err := a.ResolveArgs(nil, func(ActionAsk) (string, error) { return "", ended }); !errors.Is(err, ended) {
		t.Errorf("a cancelled ask: %v", err)
	}
}

func TestUserActionArgsProblems(t *testing.T) {
	run := []string{"x", "{a}"}
	for _, tc := range []struct {
		a    UserAction
		want string
	}{
		{UserAction{Action: "new_window", Args: []ActionArg{{Name: "a"}}}, "only for run"},
		{UserAction{Run: run, Args: []ActionArg{{Name: "A"}}}, "not valid"},
		{UserAction{Run: run, Args: []ActionArg{{Name: "session"}}}, "not valid"},
		{UserAction{Run: run, Args: []ActionArg{{Name: "a"}, {Name: "a"}}}, "not valid"},
		{UserAction{Run: run, Args: []ActionArg{{Name: "a", Type: "date"}}}, "not valid"},
		{UserAction{Run: run, Args: []ActionArg{{Name: "a", Type: ArgNumber, Default: "x"}}}, "not valid"},
		{UserAction{Run: run, Ask: []ActionAsk{{Name: "a", Kind: "form"}}}, "not valid"},
		{UserAction{Run: run, Ask: []ActionAsk{{Name: "a", Kind: "pick"}}}, "not valid"},
		{UserAction{Run: run, Ask: []ActionAsk{{Name: "a", Items: []string{"x"}}}}, "not valid"},
	} {
		if p := argsProblem(tc.a); !strings.Contains(p, tc.want) {
			t.Errorf("%+v: problem %q, want one with %q", tc.a, p, tc.want)
		}
	}
	if p := runPlaceholderProblem([]string{"x", "{a}"}, []string{"a"}); p != "" {
		t.Errorf("a declared placeholder: %s", p)
	}
	if p := runPlaceholderProblem([]string{"x", "{b}"}, []string{"a"}); p == "" {
		t.Error("an undeclared placeholder was taken")
	}
}
