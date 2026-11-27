package transcriptview

import (
	"path"
	"strconv"
	"strings"
)

// Agent calls: a tool call that talks to another agent or to the person
// through tuios. A shell call that runs tuios send-agent-message, ask-agent,
// read-agent-messages, ask-human, start-agent, delegate or queue, or one of
// the tuios MCP tools of the same verbs, carries what it said and to whom, so
// a client can draw it as a message and not as a command line.
//
// A command line is read the way a shell and the command read it: shellWord
// splits the words, a list or a pipeline is cut into its simple commands, the
// wrappers CommandLabel knows (bash -lc, env, sudo, cd dir &&) come off, and
// the words after the tuios program are read with the command's own flags as
// pflag reads them. A command line this cannot read, such as one with an
// unknown flag or a subshell, gives no call, and the client draws the plain
// command card.

// The verbs of an agent call.
const (
	CallSend     = "send"
	CallAsk      = "ask"
	CallRead     = "read"
	CallAskHuman = "ask-human"
	CallStart    = "start"
	CallDelegate = "delegate"
	CallQueue    = "queue"
)

// callsMax bounds the calls one command line carries.
const callsMax = 4

// AgentCall is one call to another agent or to the person. Every field is
// what the command line or the tool's input said, cleaned like the rest of
// the entry; an empty field was not given.
type AgentCall struct {
	Verb string `json:"verb"`
	// Session and To name the other agent, as written: a window id, a
	// prefix of one, a name, or "human". Self says To is the caller's own
	// pane ($TUIOS_PANE_ID, or the MCP tool's default).
	Session string `json:"session,omitempty"`
	To      string `json:"to,omitempty"`
	Self    bool   `json:"self,omitempty"`
	// Select is a selector that names many panes, in place of To.
	Select  string `json:"select,omitempty"`
	Subject string `json:"subject,omitempty"`
	// Text is the message, the question, the task or the first prompt.
	Text    string   `json:"text,omitempty"`
	ReplyTo uint64   `json:"reply_to,omitempty"`
	Thread  uint64   `json:"thread,omitempty"`
	Options []string `json:"options,omitempty"`
	// Harness is the agent start-agent or delegate starts, and Name the
	// name it gives the new pane.
	Harness string `json:"harness,omitempty"`
	Name    string `json:"name,omitempty"`
	// Unread, Peek and Sent are read-agent-messages' filters.
	Unread bool `json:"unread,omitempty"`
	Peek   bool `json:"peek,omitempty"`
	Sent   bool `json:"sent,omitempty"`
	// JSON says the command printed JSON, as an MCP tool always does.
	JSON bool `json:"json,omitempty"`
}

// callSpec is a command's flags as pflag reads them: each long name and
// whether it takes a value, and the one-letter forms.
type callSpec struct {
	verb  string
	flags map[string]bool
	short map[byte]string
}

// TalkFlags is every flag the commands of an agent call take, by command,
// with whether the flag takes a value. A test in cmd/tuios holds it to the
// command tree, so a new flag cannot turn a card back into a command line.
func TalkFlags() map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(talkSpecs)+1)
	for name, sp := range talkSpecs {
		out[name] = sp.flags
	}
	out[""] = globalFlags
	return out
}

// TalkShorthands is the one-letter flags of each command of an agent call.
func TalkShorthands() map[string]map[byte]string {
	out := make(map[string]map[byte]string, len(talkSpecs))
	for name, sp := range talkSpecs {
		out[name] = sp.short
	}
	return out
}

// globalFlags are the root command's persistent flags, which come before or
// after the command's own.
var globalFlags = map[string]bool{"debug": false, "server": true, "cpuprofile": true, "pprof": true, "help": false}

// flagSet builds a flag map from names, a "=" after a name meaning it takes
// a value.
func flagSet(names ...string) map[string]bool {
	out := make(map[string]bool, len(names)+1)
	for _, n := range names {
		name, value := strings.CutSuffix(n, "=")
		out[name] = value
	}
	out["help"] = false
	return out
}

// sw are the -s and -w forms most of the commands share.
var sw = map[byte]string{'s': "session", 'w': "window", 'h': "help"}

var talkSpecs = map[string]callSpec{
	"send-agent-message": {CallSend, flagSet("select=", "yes", "confirm=", "session=", "window=", "from=", "subject=", "reply-to=", "attach=", "json"), sw},
	"ask-agent": {CallAsk, flagSet("select=", "yes", "confirm=", "session=", "window=", "from=", "ready-timeout=", "settle=",
		"timeout=", "lines=", "stall-timeout=", "force", "allow-blocked", "json"), sw},
	"read-agent-messages": {CallRead, flagSet("session=", "window=", "unread", "notices", "peek", "thread=", "limit=", "json", "sent", "archive"), sw},
	"ask-human": {CallAskHuman, flagSet("session=", "window=", "option=", "timeout=", "no-wait", "request-id=", "json"),
		map[byte]string{'s': "session", 'w': "window", 'o': "option", 'h': "help"}},
	"start-agent": {CallStart, flagSet("session=", "name=", "cwd=", "repo=", "clone", "workspace=", "focus", "prompt=", "parent=",
		"model=", "effort=", "permission-mode=", "task=", "protocol=", "ready-timeout=", "env=", "grants=", "preset=", "print-id",
		"no-wait", "json"), map[byte]string{'s': "session", 'h': "help"}},
	"delegate": {CallDelegate, flagSet("session=", "agent=", "role=", "isolation=", "name=", "cwd=", "parent=", "wait=",
		"ready-timeout=", "env=", "grants=", "json"), map[byte]string{'s': "session", 'h': "help"}},
	"queue": {CallQueue, flagSet("session=", "window=", "json"), sw},
}

// mcpTalk are the tuios MCP tools of an agent call, by the end of the
// tool's name: a harness puts its own prefix in front, such as Claude Code's
// mcp__tuios__.
var mcpTalk = map[string]string{
	"tuios_send_agent_message":  CallSend,
	"tuios_ask_agent":           CallAsk,
	"tuios_read_agent_messages": CallRead,
	"tuios_delegate":            CallDelegate,
}

// AgentCallsOf is the agent calls of a tool call: of its command line for a
// shell tool, of its input for a tuios MCP tool. It is nil for any other
// call, and for a command line it cannot read.
func AgentCallsOf(tool, command string, input map[string]any) []AgentCall {
	if ShellTool(tool) {
		if !strings.Contains(command, "tuios") {
			return nil
		}
		return shellCalls(command, 0)
	}
	low := strings.ToLower(tool)
	for name, verb := range mcpTalk {
		if low == name || strings.HasSuffix(low, "_"+name) || strings.HasSuffix(low, "."+name) || strings.HasSuffix(low, "/"+name) {
			if c, ok := mcpCall(verb, input); ok {
				return []AgentCall{c}
			}
		}
	}
	return nil
}

// mcpCall is an MCP tool's input as an agent call. The tools take the JSON
// verbs' arguments.
func mcpCall(verb string, in map[string]any) (AgentCall, bool) {
	str := func(k string) string {
		s, _ := in[k].(string)
		return s
	}
	num := func(k string) uint64 {
		switch v := in[k].(type) {
		case float64:
			if v > 0 {
				return uint64(v)
			}
		case string:
			n, _ := strconv.ParseUint(v, 10, 64)
			return n
		}
		return 0
	}
	flag := func(k string) bool {
		b, _ := in[k].(bool)
		return b
	}
	c := AgentCall{Verb: verb, Session: str("session"), Select: str("select"), JSON: true}
	switch verb {
	case CallSend:
		c.To, c.Subject, c.Text, c.ReplyTo = str("to"), str("subject"), str("text"), num("reply_to")
	case CallAsk:
		c.To, c.Text = str("window"), str("text")
	case CallRead:
		c.To, c.Unread, c.Peek, c.Thread = str("to"), flag("unread"), flag("peek"), num("thread")
		c.Self = c.To == ""
	case CallDelegate:
		c.Text, c.Harness, c.Name = str("prompt"), str("agent"), str("name")
	}
	if verb == CallSend && c.Text == "" {
		return c, false
	}
	return c, true
}

// shellCalls is the agent calls of a command line, at most callsMax. depth
// counts the shells it went into, such as bash -lc.
func shellCalls(cmd string, depth int) []AgentCall {
	var out []AgentCall
	for _, seg := range simpleCommands(cmd) {
		s := seg
		for range labelSteps {
			next, ok := unwrap(s)
			if !ok || strings.TrimSpace(next) == "" {
				break
			}
			s = next
		}
		if s != seg && depth < 3 && (strings.ContainsAny(s, "|&;\n")) {
			// A script a shell ran: its own list of commands.
			out = append(out, shellCalls(s, depth+1)...)
		} else if c, ok := talkCall(commandWords(s)); ok {
			out = append(out, c)
		}
		if len(out) >= callsMax {
			return out[:callsMax]
		}
	}
	return out
}

// simpleCommands cuts a command line at its list and pipe operators and at
// its unquoted line breaks. A part with a subshell or a group is left out,
// and so is a comment.
func simpleCommands(cmd string) []string {
	var out []string
	s := cmd
	start, bad := 0, false
	cut := func(end int) {
		if part := strings.TrimSpace(cmd[start:end]); part != "" && !bad {
			out = append(out, part)
		}
		bad = false
	}
	for {
		lead := len(s) - len(strings.TrimLeft(s, " \t\n"))
		if i := strings.IndexByte(s[:lead], '\n'); i >= 0 && !(i > 0 && s[i-1] == '\\') {
			// A line break ends a command, as ; does.
			at := len(cmd) - len(s) + i
			cut(at)
			start = at + 1
		}
		if lead < len(s) && s[lead] == '#' {
			// A comment runs to the end of the line.
			at := len(cmd) - len(s) + lead
			cut(at)
			nl := strings.IndexByte(s[lead:], '\n')
			if nl < 0 {
				return out
			}
			s = s[lead+nl:]
			start = len(cmd) - len(s)
			continue
		}
		_, raw, rest := shellWord(s)
		if raw == "" {
			break
		}
		at := len(cmd) - len(rest) - len(raw)
		switch raw {
		case "|", "||", "&&", ";", "&", ";;":
			cut(at)
			start = at + len(raw)
		case "(", ")":
			bad = true
		case ">", "<":
			// 2>&1: the & is the redirection's, not a list's.
			if _, r, after := shellWord(rest); r == "&" {
				rest = after
			}
		}
		s = rest
	}
	cut(len(cmd))
	return out
}

// word is one word of a simple command: its value and how it is written.
type word struct{ value, raw string }

// commandWords is a simple command's words, without its redirections. An
// unquoted command substitution makes it nil: what it prints is not known.
func commandWords(s string) []word {
	var out []word
	for {
		value, raw, rest := shellWord(s)
		if raw == "" {
			return out
		}
		switch {
		case raw == ">" || raw == ">>" || raw == "<" || raw == "<<":
			// A redirection and its file, and an fd number written
			// against it (2>&1).
			if n := len(out); n > 0 && digits(out[n-1].raw) && strings.HasSuffix(s[:len(s)-len(rest)-len(raw)], out[n-1].raw) {
				out = out[:n-1]
			}
			if _, r, after := shellWord(rest); r == "&" {
				rest = after
			}
			_, _, rest = shellWord(rest)
		case raw == "(" || raw == ")" || strings.HasSuffix(raw, "$") && strings.HasPrefix(strings.TrimLeft(rest, " "), "("):
			return nil
		case raw == "\\\n":
			// A line that goes on.
		default:
			out = append(out, word{value: heredocValue(value), raw: raw})
		}
		s = rest
	}
}

// digits reports whether s is only ASCII digits.
func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// heredocValue is the body of a "$(cat <<'EOF' ... EOF)" value, the way
// agents pass a long message, and v itself for any other value.
func heredocValue(v string) string {
	rest, ok := strings.CutPrefix(v, "$(cat <<")
	if !ok {
		return v
	}
	rest = strings.TrimPrefix(rest, "-")
	head, body, ok := strings.Cut(rest, "\n")
	if !ok {
		return v
	}
	delim := strings.Trim(strings.TrimSpace(head), `'"`)
	if delim == "" {
		return v
	}
	end := strings.LastIndex(body, "\n"+delim)
	if strings.HasPrefix(body, delim+"\n") || strings.TrimSpace(body) == delim {
		return ""
	}
	if end < 0 {
		return v
	}
	if tail := strings.TrimSpace(body[end+1+len(delim):]); tail != ")" {
		return v
	}
	return body[:end]
}

// talkCall reads a simple command that runs tuios and one of the commands of
// an agent call.
func talkCall(words []word) (AgentCall, bool) {
	if len(words) < 2 {
		return AgentCall{}, false
	}
	prog := path.Base(strings.ReplaceAll(words[0].value, `\`, "/"))
	if prog = strings.TrimSuffix(strings.ToLower(prog), ".exe"); prog != "tuios" {
		return AgentCall{}, false
	}
	// The root's persistent flags may come before the command.
	i := 1
	for i < len(words) && strings.HasPrefix(words[i].value, "--") {
		name, _, eq := strings.Cut(words[i].value[2:], "=")
		value, known := globalFlags[name]
		if !known {
			return AgentCall{}, false
		}
		i++
		if value && !eq {
			i++
		}
	}
	if i >= len(words) {
		return AgentCall{}, false
	}
	sp, ok := talkSpecs[words[i].value]
	if !ok {
		return AgentCall{}, false
	}
	vals, args, ok := parseFlags(sp, words[i+1:])
	if !ok {
		return AgentCall{}, false
	}
	first := func(k string) string {
		if v := vals[k]; len(v) > 0 {
			return v[len(v)-1]
		}
		return ""
	}
	on := func(k string) bool {
		v := first(k)
		return v != "" && v != "false" && v != "0"
	}
	num := func(k string) uint64 {
		n, _ := strconv.ParseUint(first(k), 10, 64)
		return n
	}
	c := AgentCall{Verb: sp.verb, Session: first("session"), To: first("window"), Select: first("select"), JSON: on("json")}
	switch sp.verb {
	case CallSend, CallAsk:
		if len(args) != 1 {
			return AgentCall{}, false
		}
		c.Text, c.Subject, c.ReplyTo = args[0], first("subject"), num("reply-to")
	case CallRead:
		c.Unread, c.Peek, c.Sent, c.Thread = on("unread"), on("peek"), on("sent"), num("thread")
		if len(args) != 0 {
			return AgentCall{}, false
		}
	case CallAskHuman:
		if len(args) > 1 {
			return AgentCall{}, false
		}
		if len(args) == 1 {
			c.Text = args[0]
		}
		c.Options = vals["option"]
	case CallStart:
		if len(args) > 0 {
			c.Harness = args[0]
		}
		c.Text, c.Name, c.To = first("prompt"), first("name"), ""
	case CallDelegate:
		if len(args) > 0 {
			c.Text = args[0]
		}
		c.Harness, c.Name = first("agent"), first("name")
		if c.Name == "" {
			// The child's window name is its role.
			c.Name = first("role")
		}
	case CallQueue:
		if len(args) == 0 || args[0] == "ls" || args[0] == "rm" {
			return AgentCall{}, false
		}
		c.Text = strings.Join(args, " ")
	}
	if selfRef(c.To) {
		c.To, c.Self = "", true
	} else if c.To == "" && sp.verb == CallRead {
		c.Self = true
	}
	if selfRef(c.Session) {
		c.Session = ""
	}
	return c, true
}

// selfRef reports whether a value is the caller's own pane or session, as
// agents write it.
func selfRef(v string) bool {
	switch v {
	case "$TUIOS_PANE_ID", "${TUIOS_PANE_ID}", "$TUIOS_SESSION", "${TUIOS_SESSION}":
		return true
	}
	return false
}

// parseFlags reads a command's words as pflag does: --name=value, --name
// value, -w value, -wvalue, a bool flag alone, and -- before the arguments.
// Flags may come after arguments. An unknown flag, or one without its value,
// fails the read.
func parseFlags(sp callSpec, words []word) (map[string][]string, []string, bool) {
	vals := map[string][]string{}
	var args []string
	for i := 0; i < len(words); i++ {
		w := words[i].value
		switch {
		case w == "--":
			for _, rest := range words[i+1:] {
				args = append(args, rest.value)
			}
			return vals, args, true
		case strings.HasPrefix(w, "--"):
			name, value, eq := strings.Cut(w[2:], "=")
			takes, known := sp.flags[name]
			if !known {
				if takes, known = globalFlags[name]; !known {
					return nil, nil, false
				}
			}
			if takes && !eq {
				if i++; i >= len(words) {
					return nil, nil, false
				}
				value = words[i].value
			} else if !takes && !eq {
				value = "true"
			}
			vals[name] = append(vals[name], value)
		case len(w) > 1 && w[0] == '-':
			for j := 1; j < len(w); j++ {
				name, known := sp.short[w[j]]
				if !known {
					return nil, nil, false
				}
				if !sp.flags[name] {
					vals[name] = append(vals[name], "true")
					continue
				}
				value := strings.TrimPrefix(w[j+1:], "=")
				if j+1 == len(w) {
					if i++; i >= len(words) {
						return nil, nil, false
					}
					value = words[i].value
				}
				vals[name] = append(vals[name], value)
				break
			}
		default:
			args = append(args, w)
		}
	}
	return vals, args, true
}
