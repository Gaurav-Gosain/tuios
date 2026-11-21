package transcriptview

import (
	"path"
	"strconv"
	"strings"
)

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

var globalFlags = map[string]bool{"debug": false, "server": true, "cpuprofile": true, "pprof": true, "help": false}

func flagSet(names ...string) map[string]bool {
	out := make(map[string]bool, len(names)+1)
	for _, n := range names {
		name, value := strings.CutSuffix(n, "=")
		out[name] = value
	}
	out["help"] = false
	return out
}

var sw = map[byte]string{'s': "session", 'w': "window", 'h': "help"}

var talkSpecs = map[string]callSpec{
	"send-agent-message": {CallSend, flagSet("select=", "yes", "confirm=", "session=", "window=", "from=", "subject=", "reply-to=", "attach=", "json"), sw},
	"ask-agent": {CallAsk, flagSet("select=", "yes", "confirm=", "session=", "window=", "from=", "ready-timeout=", "settle=",
		"timeout=", "lines=", "stall-timeout=", "force", "allow-blocked", "json"), sw},
	"read-agent-messages": {CallRead, flagSet("session=", "window=", "unread", "notices", "peek", "thread=", "limit=", "json", "sent"), sw},
	"ask-human": {CallAskHuman, flagSet("session=", "window=", "option=", "timeout=", "no-wait", "request-id=", "json"),
		map[byte]string{'s': "session", 'w': "window", 'o': "option", 'h': "help"}},
	"start-agent": {CallStart, flagSet("session=", "name=", "cwd=", "repo=", "clone", "workspace=", "focus", "prompt=", "parent=",
		"model=", "effort=", "permission-mode=", "task=", "protocol=", "ready-timeout=", "env=", "grants=", "preset=", "print-id",
		"no-wait", "json"), map[byte]string{'s': "session", 'h': "help"}},
	"delegate": {CallDelegate, flagSet("session=", "agent=", "role=", "isolation=", "name=", "cwd=", "parent=", "wait=",
		"ready-timeout=", "env=", "grants=", "json"), map[byte]string{'s': "session", 'h': "help"}},
	"queue": {CallQueue, flagSet("session=", "window=", "json"), sw},
}

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
			at := len(cmd) - len(s) + i
			cut(at)
			start = at + 1
		}
		if lead < len(s) && s[lead] == '#' {
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
			if _, r, after := shellWord(rest); r == "&" {
				rest = after
			}
		}
		s = rest
	}
	cut(len(cmd))
	return out
}

type word struct{ value, raw string }

func commandWords(s string) []word {
	var out []word
	for {
		value, raw, rest := shellWord(s)
		if raw == "" {
			return out
		}
		switch {
		case raw == ">" || raw == ">>" || raw == "<" || raw == "<<":
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
		default:
			out = append(out, word{value: heredocValue(value), raw: raw})
		}
		s = rest
	}
}

func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

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

func talkCall(words []word) (AgentCall, bool) {
	if len(words) < 2 {
		return AgentCall{}, false
	}
	prog := path.Base(strings.ReplaceAll(words[0].value, `\`, "/"))
	if prog = strings.TrimSuffix(strings.ToLower(prog), ".exe"); prog != "tuios" {
		return AgentCall{}, false
	}
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

func selfRef(v string) bool {
	switch v {
	case "$TUIOS_PANE_ID", "${TUIOS_PANE_ID}", "$TUIOS_SESSION", "${TUIOS_SESSION}":
		return true
	}
	return false
}

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
