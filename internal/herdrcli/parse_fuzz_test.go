package herdrcli

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// FuzzParse feeds herdr command lines to the parser. Parse reads argv a
// plugin builds from its own input (a pane id, a URL, a label), so it must
// hold for any argv. The ways it could fail, written down first:
//
//   - a panic: an index past the end of args, a flag whose value is the last
//     argument, a nil map written to;
//   - a request the socket cannot read: params that do not marshal to JSON
//     (a NaN or an infinite --ratio), or a method name that is not one;
//   - an answer with no way out: a call that is neither a request, a local
//     error nor text, or a usage error whose exit code is not herdr's 0 or 2.
//
// The seeds are the command lines the plugins in the survey run.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"pane split --pane w1:p1 --direction right --focus --right-click pane",
		"pane split --pane w1:p1 --direction down --focus --ratio 0.4 --right-click pane",
		"pane swap --pane w1:p2 --direction left",
		"pane run w1:p2 'terminal-browser' open x --split-dir=right",
		"pane neighbor --pane w1:p1 --direction right",
		"pane process-info --pane w1:p1",
		"pane edges --current",
		"pane focus --direction left",
		"pane zoom --on",
		"pane zoom w1:p1 --toggle --off",
		"pane list --workspace w1",
		"pane get w1:p1",
		"pane read w1:p1 --source=recent --lines 20 --raw",
		"pane wait-output w1:p1 --regex a.b --timeout 100",
		"pane send-text w1:p1 hello world",
		"pane send-keys w1:p1 Enter C-c",
		"pane report-agent w1:p1 --source s --agent a --state working --seq 3 -- crush --resume",
		"pane report-metadata w1:p1 --source s --token a=b --clear-token c --ttl-ms 10",
		"pane move w1:p1 --tab w1:t2 --split right",
		"pane input --current --right-click herdr",
		"tab focus w1:t1",
		"tab create --workspace w1 --label x --env A=B --focus",
		"workspace focus w1",
		"workspace close w1 --group",
		"agent start helper --kind claude --pane w1:p1 --timeout 20000 -- --model x",
		"agent prompt helper hi --wait --until idle --timeout 5",
		"agent read helper --format ansi",
		"worktree create --cwd ./repo --branch b --path ~/x",
		"worktree open --branch b",
		"notification show Done --body ok --sound done",
		"api snapshot",
		"server reload-config",
		"plugin list",
		"--version",
		"pane",
		"pane split --direction",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	method := regexp.MustCompile(`^[a-z_]+(\.[a-z_]+)+$`)
	f.Fuzz(func(t *testing.T, line string) {
		args := strings.Split(line, " ")
		env := func(k string) string {
			if k == "HERDR_PANE_ID" {
				return "w1:p1"
			}
			return ""
		}
		c, uerr := Parse(args, env, "/work")
		if uerr != nil {
			if c != nil {
				t.Fatalf("%q: both a call and a usage error", line)
			}
			if uerr.Code != 0 && uerr.Code != 2 {
				t.Fatalf("%q: exit code %d", line, uerr.Code)
			}
			return
		}
		if c == nil {
			t.Fatalf("%q: no call and no error", line)
		}
		switch c.Output {
		case OutText, OutLocal:
			if c.Text == "" {
				t.Fatalf("%q: an answer with no text", line)
			}
			return
		}
		if !method.MatchString(c.Method) || c.ID == "" {
			t.Fatalf("%q: method %q, id %q", line, c.Method, c.ID)
		}
		if _, err := json.Marshal(map[string]any{"id": c.ID, "method": c.Method, "params": c.Params}); err != nil {
			t.Fatalf("%q: params do not marshal: %v", line, err)
		}
	})
}
