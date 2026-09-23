package tmuxcompat

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseGlobal(t *testing.T) {
	cases := []struct {
		args     []string
		socket   string
		name     string
		version  bool
		rest     []string
		failWith string
	}{
		{args: []string{"-S", "/s", "list-panes"}, socket: "/s", rest: []string{"list-panes"}},
		{args: []string{"-S/s", "list-panes"}, socket: "/s", rest: []string{"list-panes"}},
		{args: []string{"-Lswarm", "has", "-t", "x"}, name: "swarm", rest: []string{"has", "-t", "x"}},
		{args: []string{"-2uS", "/s", "ls"}, socket: "/s", rest: []string{"ls"}},
		{args: []string{"-V"}, version: true, rest: []string{}},
		{args: []string{"-f", "/dev/null", "--", "-weird"}, rest: []string{"-weird"}},
		{args: []string{"-CC"}, failWith: "control mode"},
		{args: []string{"-Q"}, failWith: "unknown option"},
		{args: []string{"-S"}, failWith: "requires an argument"},
	}
	for _, c := range cases {
		g, rest, err := ParseGlobal(c.args)
		if c.failWith != "" {
			if err == nil || !strings.Contains(err.Error(), c.failWith) {
				t.Errorf("ParseGlobal(%v) err = %v, want %q", c.args, err, c.failWith)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseGlobal(%v): %v", c.args, err)
			continue
		}
		if g.Socket != c.socket || g.Name != c.name || g.Version != c.version || !reflect.DeepEqual(rest, c.rest) {
			t.Errorf("ParseGlobal(%v) = %+v %v", c.args, g, rest)
		}
	}
}

func TestSplitCommands(t *testing.T) {
	cases := []struct {
		in   []string
		want [][]string
	}{
		{[]string{"a", "x", ";", "b"}, [][]string{{"a", "x"}, {"b"}}},
		{[]string{"a", "x;", "b"}, [][]string{{"a", "x"}, {"b"}}},
		{[]string{"send", "echo a\\;"}, [][]string{{"send", "echo a;"}}},
		{[]string{";", "a", ";"}, [][]string{{"a"}}},
	}
	for _, c := range cases {
		if got := SplitCommands(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitCommands(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseFlags(t *testing.T) {
	sp := spec{bools: "dhP", values: "tFl"}
	p, err := parseFlags("split-window", sp, []string{"-dP", "-t%1", "-F", "#{pane_id}", "-l", "70%", "--", "-x", "y"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Has('d') || !p.Has('P') || p.Has('h') {
		t.Errorf("bools = %v", p.flags)
	}
	if v, _ := p.Value('t'); v != "%1" {
		t.Errorf("-t = %q", v)
	}
	if v, _ := p.Value('F'); v != "#{pane_id}" {
		t.Errorf("-F = %q", v)
	}
	if !reflect.DeepEqual(p.Args, []string{"-x", "y"}) {
		t.Errorf("args = %q", p.Args)
	}
	p, err = parseFlags("x", sp, []string{"-d", "cmd", "-h"})
	if err != nil || !reflect.DeepEqual(p.Args, []string{"cmd", "-h"}) || p.Has('h') {
		t.Errorf("flags after the first argument were read: %v %v", p.Args, err)
	}
	if _, err := parseFlags("x", sp, []string{"-Z"}); err == nil {
		t.Error("an unknown flag was accepted")
	}
	if _, err := parseFlags("x", sp, []string{"-t"}); err == nil {
		t.Error("a value flag with no value was accepted")
	}
}

func TestExpand(t *testing.T) {
	vars := map[string]string{"pane_id": "%3", "pane_active": "1", "window_index": "2", "session_name": "work", "empty": "", "zero": "0"}
	cases := []struct {
		in, want string
		missing  []string
	}{
		{"#{pane_id}", "%3", nil},
		{"#D #I #S", "%3 2 work", nil},
		{"## #{pane_id}", "# %3", nil},
		{"#{?pane_active,yes,no}", "yes", nil},
		{"#{?zero,yes,no}", "no", nil},
		{"#{?empty,yes,}", "", nil},
		{"#{?pane_active,#{pane_id},x}", "%3", nil},
		{"#{?pane_active,a#,b,c}", "a,b", nil},
		{"#{==:#{window_index},2}", "1", nil},
		{"#{!=:#{window_index},2}", "0", nil},
		{"#[fg=red]#{pane_id}#[default]", "#[fg=red]%3#[default]", nil},
		{"#{nope}-#{pane_id}", "-%3", []string{"nope"}},
		{"#{t:window_activity}", "", []string{"t:window_activity"}},
		{"#{unclosed", "#{unclosed", nil},
	}
	for _, c := range cases {
		got, missing := Expand(c.in, vars)
		if got != c.want || !reflect.DeepEqual(missing, c.missing) {
			t.Errorf("Expand(%q) = %q %v, want %q %v", c.in, got, missing, c.want, c.missing)
		}
	}
}
