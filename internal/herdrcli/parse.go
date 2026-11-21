package herdrcli

import (
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Version is the herdr release whose CLI this package follows. It matches
// the API version the daemon reports (herdrTargetVersion in
// internal/session/herdr_api.go).
const Version = "0.9.5"

const protocolVersion = 22

// Output is what a call prints when it is answered.
type Output int

const (
	// OutResponse prints the response line: stdout on success, stderr on an
	// error.
	OutResponse Output = iota
	// OutOK prints nothing on success and the response line on stderr on an
	// error.
	OutOK
	// OutRead prints the text of a read on success.
	OutRead
	// OutAgentStart starts an agent and waits for it to be ready, then
	// prints the response line.
	OutAgentStart
	// OutLocal sends nothing. Text is the message of an unsupported error.
	OutLocal
	// OutText sends nothing. Text is printed on stdout and the exit code is 0.
	OutText
	// OutStatus pings the socket and prints herdr's status report. Text is
	// the scope: "", "server" or "client". Params["json"] asks for JSON.
	OutStatus
	// OutSessions pings the socket and prints herdr's session list: the one
	// tuios daemon behind the socket. Params["json"] asks for JSON.
	OutSessions
	// OutPluginConfigDir sends nothing. Text is a plugin id, and its config
	// folder is made and printed, as herdr plugin config-dir does.
	OutPluginConfigDir
)

// Call is one parsed herdr command: the request it sends and how its answer
// is printed.
type Call struct {
	ID     string
	Method string
	Params map[string]any
	Output Output
	// Text is the message of an OutLocal call, or what an OutText call
	// prints.
	Text string
	// Wait is true for a call that may wait as long as its own timeout says.
	Wait bool
	// Report is true for a pane report, which must never hold its reporter
	// up for long.
	Report bool
}

// UsageError is a command line herdr refuses before it sends anything: the
// message goes to stderr and the process exits with Code.
type UsageError struct {
	Msg  string
	Code int
}

//go:noinline
func usage(msg string) *UsageError { return &UsageError{Msg: msg, Code: 2} }

func missing(flag string) *UsageError { return usage("missing value for " + flag) }

func unknownOption(arg string) *UsageError { return usage("unknown option: " + arg) }

// Env reads one environment variable.
type Env func(string) string

// Parse reads a herdr command line, without the program name. cwd is the
// directory relative worktree paths are taken from.
func Parse(args []string, getenv Env, cwd string) (*Call, *UsageError) {
	if len(args) == 0 {
		return nil, usage("this herdr is tuios's front for herdr's command line, and it does not start herdr's app. Run tuios instead, or run herdr --help")
	}
	switch args[0] {
	case "--version", "-V":
		return &Call{Output: OutText, Text: "herdr " + Version + "+tuios"}, nil
	case "--help", "-h", "help":
		return &Call{Output: OutText, Text: topHelp}, nil
	}
	group, rest := args[0], args[1:]
	if strings.HasPrefix(group, "-") {
		name, _, _ := strings.Cut(group, "=")
		if !slices.Contains(launchOptions, name) {
			return nil, usage("unknown option: " + group + "\nrun 'herdr --help' for usage")
		}
		return local("cli", group+" is a herdr launch option. tuios's herdr front answers herdr's socket commands only"), nil
	}
	parse, ok := groups[group]
	if !ok {
		if g, ok := localGroups[group]; ok {
			return local("cli:"+group, g), nil
		}
		return nil, usage("unknown herdr command: " + group + ". Run herdr --help for the commands tuios answers")
	}
	if flags, _, _ := splitDashDash(rest); slices.Contains(flags, "--help") || slices.Contains(flags, "-h") {
		return &Call{Output: OutText, Text: groupHelp[group]}, nil
	}
	if group == "status" {
		return statusCall(rest)
	}
	if len(rest) == 0 {
		return nil, &UsageError{Msg: groupHelp[group], Code: 2}
	}
	switch rest[0] {
	case "help", "--help", "-h":
		return nil, &UsageError{Msg: groupHelp[group], Code: 0}
	}
	return parse(rest[0], rest[1:], getenv, cwd)
}

var launchOptions = []string{"--session", "--machine", "--remote", "--remote-keybindings", "--handoff", "--default-config", "--skill"}

func local(id, msg string) *Call {
	return &Call{ID: id, Output: OutLocal, Text: msg}
}

type groupParser func(sub string, args []string, getenv Env, cwd string) (*Call, *UsageError)

var groups = map[string]groupParser{
	"pane":         parsePane,
	"tab":          parseTab,
	"workspace":    parseWorkspace,
	"agent":        parseAgent,
	"worktree":     parseWorktree,
	"notification": parseNotification,
	"api":          parseAPI,
	"server":       parseServer,
	"terminal":     parseTerminal,
	"status":       parseStatus,
	"session":      parseSession,
	"plugin":       parsePlugin,
}

var localGroups = map[string]string{
	"completion":  "tuios's herdr front has no shell completions",
	"completions": "tuios's herdr front has no shell completions",
	"config":      "herdr config edits herdr's own config.toml. tuios does not read it",
	"channel":     "herdr channel picks herdr's update channel. tuios does not update herdr",
	"machine":     "herdr machine manages herdr's SSH machines. Use tuios hosts",
	"update":      "herdr update installs herdr. tuios does not update herdr",
	"integration": "herdr integration installs herdr's agent hooks. Use tuios integration",
}

func call(id, method string, params map[string]any) *Call {
	if params == nil {
		params = map[string]any{}
	}
	return &Call{ID: id, Method: method, Params: params}
}

func okCall(id, method string, params map[string]any) *Call {
	c := call(id, method, params)
	c.Output = OutOK
	return c
}

type valueFn = func(flag string) (string, *UsageError)

//go:noinline
func walk(args []string, fn func(arg string, value valueFn) *UsageError) *UsageError {
	for i := 0; i < len(args); i++ {
		took := false
		value := func(flag string) (string, *UsageError) {
			if i+1 >= len(args) {
				return "", missing(flag)
			}
			took = true
			return args[i+1], nil
		}
		if err := fn(args[i], value); err != nil {
			return err
		}
		if took {
			i++
		}
	}
	return nil
}

func expandEquals(args []string, valueFlags ...string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if flag, v, ok := strings.Cut(a, "="); ok && slices.Contains(valueFlags, flag) {
			out = append(out, flag, v)
			continue
		}
		out = append(out, a)
	}
	return out
}

func flagKey(flag string) string {
	return strings.ReplaceAll(strings.TrimPrefix(flag, "--"), "-", "_")
}

func envPane(getenv Env) any {
	if v := getenv("HERDR_PANE_ID"); strings.TrimSpace(v) != "" {
		return v
	}
	return nil
}

func setOpt(p map[string]any, key string, v any) {
	if v != nil {
		p[key] = v
	}
}

func parseUint(flag, v string, bits int) (uint64, *UsageError) {
	n, err := strconv.ParseUint(v, 10, bits)
	if err != nil {
		return 0, usage("invalid value for " + flag + ": " + v)
	}
	return n, nil
}

func parseFloat(what, v string) (float64, *UsageError) {
	f, err := strconv.ParseFloat(v, 32)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, usage("invalid " + what + ": " + v)
	}
	return f, nil
}

func paneDirection(v string) (string, *UsageError) {
	switch v {
	case "left", "right", "up", "down":
		return v, nil
	}
	return "", usage("invalid pane direction: " + v + " (expected left, right, up, or down)")
}

func splitDirection(v string) (string, *UsageError) {
	switch v {
	case "right", "down":
		return v, nil
	}
	return "", usage("invalid split direction: " + v)
}

func readSource(v string) (string, *UsageError) {
	switch v {
	case "visible", "recent", "detection":
		return v, nil
	case "recent-unwrapped", "recent_unwrapped":
		return "recent_unwrapped", nil
	}
	return "", usage("invalid read source: " + v)
}

func readFormat(v string) (string, *UsageError) {
	switch v {
	case "text", "ansi":
		return v, nil
	}
	return "", usage("invalid read format: " + v)
}

func agentStatus(v string) (string, *UsageError) {
	switch v {
	case "idle", "working", "blocked", "done", "unknown":
		return v, nil
	}
	return "", usage("invalid agent status: " + v + " (expected idle, working, blocked, done, or unknown)")
}

func paneAgentState(v string) (string, *UsageError) {
	switch v {
	case "idle", "working", "blocked", "unknown":
		return v, nil
	}
	return "", usage("invalid pane agent state: " + v + " (expected idle, working, blocked, or unknown)")
}

func envAssignment(v string) (string, string, *UsageError) {
	key, val, ok := strings.Cut(v, "=")
	if !ok {
		return "", "", usage("env must use KEY=VALUE")
	}
	if key == "" {
		return "", "", usage("env key must not be empty")
	}
	if strings.ContainsRune(v, 0) {
		return "", "", usage("env must not contain NUL bytes")
	}
	return key, val, nil
}

func tokenAssignment(v string) (string, string, *UsageError) {
	key, val, ok := strings.Cut(v, "=")
	if !ok {
		return "", "", usage("token must use NAME=VALUE")
	}
	if key == "" {
		return "", "", usage("token name must not be empty")
	}
	return key, val, nil
}

func absPath(v string, getenv Env, cwd string) string {
	if v == "~" || strings.HasPrefix(v, "~/") {
		if home := getenv("HOME"); home != "" {
			v = filepath.Join(home, strings.TrimPrefix(v, "~"))
		}
	}
	if !filepath.IsAbs(v) {
		v = filepath.Join(cwd, v)
	}
	return v
}

func splitDashDash(args []string) ([]string, []string, bool) {
	if i := slices.Index(args, "--"); i >= 0 {
		return args[:i], args[i+1:], true
	}
	return args, nil, false
}
