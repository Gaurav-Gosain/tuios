package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
)

// Status line feed: the model, context use and cost a harness shows the
// person, written to the pane's agent metadata.
//
// Claude Code runs one status line command with a JSON object on stdin every
// time the conversation changes (https://code.claude.com/docs/en/statusline).
// tuios can own that one slot, statusLine in the same settings.json the hooks
// live in, with a command that runs "tuios agent-statusline claude-code". It is
// opt in, through integration install --statusline, because the slot is the
// person's: a status line they wrote is never replaced. They can keep it by
// installing with --then and their command, which the wrapper runs with the
// same stdin, printing its output unchanged. Uninstall puts their command back.
//
// The opencode plugin feeds the same wrapper from its message events, since
// opencode has no status line command (see assets/opencode).
//
// Every field of every payload is optional. A field that is missing, null or
// of another type is left out, and a payload with none of them writes nothing.

// StatusLineVersion is the version of the statusLine entry this build writes.
const StatusLineVersion = 1

// statusLineVerb is the subcommand the managed entry runs.
const statusLineVerb = "agent-statusline"

// StatusLineMaxPayload caps what the wrapper reads from stdin (the cap
// agent-deck uses for the same payload).
const StatusLineMaxPayload = 1 << 20

// Meta keys the status line feed writes, and the source it writes them under.
const (
	MetaModel   = "model"
	MetaContext = "context"
	MetaCost    = "cost"
	MetaPlan    = "plan"
	// StatusLineSource is the set-agent-meta source of the wrapper's keys.
	StatusLineSource = "statusline"
)

// StatusLineHarnesses lists the harnesses the wrapper reads a payload for.
func StatusLineHarnesses() []string { return []string{ClaudeCode, OpenCode, Kilo} }

// StatusLineValues are what one payload says, formatted for the rail. An
// empty field is unstated.
type StatusLineValues struct {
	Model   string `json:"model,omitempty"`
	Context string `json:"context,omitempty"`
	Cost    string `json:"cost,omitempty"`
	// Session is the harness's conversation id, when the payload names it.
	// It is not written anywhere; the wrapper uses it to tell a new
	// conversation from the last one.
	Session string `json:"session,omitempty"`
}

// Tokens are the values as set-agent-meta tokens, only the stated ones.
func (v StatusLineValues) Tokens() map[string]string {
	out := map[string]string{}
	for k, val := range map[string]string{MetaModel: v.Model, MetaContext: v.Context, MetaCost: v.Cost} {
		if val != "" {
			out[k] = val
		}
	}
	return out
}

// Empty reports whether the payload stated nothing the rail can show.
func (v StatusLineValues) Empty() bool { return v.Model == "" && v.Context == "" && v.Cost == "" }

// ParseStatusLine reads one status line payload for harness.
//
// Claude Code: model.display_name, else model.id; context_window.used_percentage;
// cost.total_cost_usd; session_id. opencode and Kilo (from tuios's own plugin):
// modelID, cost, session_id.
func ParseStatusLine(harness string, payload []byte) (StatusLineValues, error) {
	id, ok := Canonical(harness)
	if !ok || !slices.Contains(StatusLineHarnesses(), id) {
		return StatusLineValues{}, fmt.Errorf("no status line feed for %s. Available: %s", harness, strings.Join(StatusLineHarnesses(), ", "))
	}
	trimmed := strings.TrimSpace(string(payload))
	if !strings.HasPrefix(trimmed, "{") {
		return StatusLineValues{}, errors.New("payload is not a JSON object")
	}
	payloadMap := map[string]any{}
	if err := json.Unmarshal([]byte(trimmed), &payloadMap); err != nil {
		return StatusLineValues{}, fmt.Errorf("payload is not a JSON object: %w", err)
	}
	p := fields(payloadMap)
	var v StatusLineValues
	v.Session = p.str("session_id")
	switch id {
	case ClaudeCode:
		m := p.obj("model")
		v.Model = m.first("display_name", "id")
		if pct, ok := p.obj("context_window").num("used_percentage"); ok {
			v.Context = FormatPercent(pct)
		}
		if usd, ok := p.obj("cost").num("total_cost_usd"); ok {
			v.Cost = FormatCost(usd, "USD")
		}
	default:
		v.Model = p.str("modelID")
		if usd, ok := p.num("cost"); ok {
			v.Cost = FormatCost(usd, "USD")
		}
	}
	return v, nil
}

// num reads a finite number.
func (f fields) num(key string) (float64, bool) {
	v, ok := f[key].(float64)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// FormatPercent is a context use as the rail shows it: whole percent, 0 to
// 100.
func FormatPercent(pct float64) string {
	pct = math.Max(0, math.Min(100, pct))
	return strconv.Itoa(int(math.Round(pct))) + "%"
}

// FormatCost is a cost as the rail shows it: dollars with two decimals, or the
// amount and the currency code for any other currency. A negative cost is
// unstated and gives "".
func FormatCost(amount float64, currency string) string {
	if amount < 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return ""
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" || currency == "USD" {
		return "$" + strconv.FormatFloat(amount, 'f', 2, 64)
	}
	return strconv.FormatFloat(amount, 'f', 2, 64) + " " + currency
}

// StatusLineForeign reports whether TUIOS_AGENT names a harness other than
// harness, the same rule the hooks apply: a status line then belongs to an
// agent nested inside the pane's own, and is not reported.
func StatusLineForeign(harness string, getenv func(string) string) (string, bool) {
	if getenv == nil {
		return "", false
	}
	id, _ := Canonical(harness)
	hint := strings.TrimSpace(getenv(AgentHintEnv))
	if hint == "" {
		return "", false
	}
	owner, known := hintOwner(hint)
	if !known {
		return "", false
	}
	// Kilo is an opencode fork that shares its plugin.
	if owner == id || (owner == OpenCode && id == Kilo) || (owner == Kilo && id == OpenCode) {
		return "", false
	}
	return owner, true
}

// StatusLineCommand is the command the managed statusLine entry runs. then,
// when set, is the person's own status line command the wrapper chains to.
func StatusLineCommand(tuios, then string) string {
	cmd := shellWord(tuios) + " " + statusLineVerb + " " + ClaudeCode + " " + managedMarker + " " + strconv.Itoa(StatusLineVersion)
	if then != "" {
		cmd += " --then " + quoteWord(then)
	}
	return cmd
}

// quoteWord quotes s as one POSIX shell word, and leaves a plain word alone.
// It uses single quotes on Windows too: Claude Code runs its status line
// through a POSIX shell there as well, where a double-quoted word would still
// have its $ expanded.
func quoteWord(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`&|;<>()*?[]{}!#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// managedStatusLine reads a statusLine command tuios wrote: the program it
// runs, the version, and the chained command. ok is false for anything else.
func managedStatusLine(cmd string) (program string, version int, then string, ok bool) {
	words, err := splitWords(cmd)
	if err != nil || len(words) < 5 {
		return "", 0, "", false
	}
	if words[1] != statusLineVerb || words[2] != ClaudeCode || words[3] != managedMarker {
		return "", 0, "", false
	}
	version, err = strconv.Atoi(words[4])
	if err != nil {
		return "", 0, "", false
	}
	rest := words[5:]
	switch {
	case len(rest) == 0:
	case len(rest) == 2 && rest[0] == "--then":
		then = rest[1]
	default:
		return "", 0, "", false
	}
	return words[0], version, then, true
}

// splitWords splits a command line the way a POSIX shell splits words:
// single quotes, double quotes with backslash escapes, and backslashes. It
// expands nothing. It is enough to read back what shellWord wrote.
func splitWords(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case c == '\'':
			inWord = true
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("unterminated single quote")
			}
			cur.WriteString(s[i+1 : i+1+end])
			i += end + 1
		case c == '"':
			inWord = true
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`\n", s[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, errors.New("unterminated double quote")
			}
		case c == '\\' && i+1 < len(s):
			inWord = true
			i++
			cur.WriteByte(s[i])
		default:
			inWord = true
			cur.WriteByte(c)
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// SupportsStatusLine reports whether tuios can install a status line feed for
// the harness: only Claude Code has a status line command.
func (t *Target) SupportsStatusLine() bool { return t.ID == ClaudeCode }

// StatusLineStatus is what holds the harness's status line slot.
type StatusLineStatus struct {
	Supported bool `json:"supported"`
	// Installed says tuios's wrapper holds the slot.
	Installed bool `json:"installed"`
	// Current says it runs the command status was asked about, at this
	// build's version.
	Current bool `json:"current"`
	Version int  `json:"version,omitempty"`
	// Then is the person's own command the wrapper chains to.
	Then string `json:"then,omitempty"`
	// Foreign says a status line tuios did not write holds the slot, and
	// Command is what it runs, when it is a command.
	Foreign bool   `json:"foreign,omitempty"`
	Command string `json:"command,omitempty"`
}

// StatusLineOwnedError is install refusing a status line the person owns. It
// names their command, so the caller can print the --then form that keeps it.
type StatusLineOwnedError struct {
	Path    string
	Command string
}

func (e *StatusLineOwnedError) Error() string {
	if e.Command == "" {
		return e.Path + " has a status line tuios did not write, and it is not a command tuios can chain to, so it is left alone"
	}
	return e.Path + " has a status line of your own, so it is left alone. To keep it and feed tuios too, install with --then and your command"
}

// statusLineSlot reads the statusLine entry of a settings document.
type statusLineSlot struct {
	present bool
	obj     *orderedObject
	command string
	isCmd   bool
}

func readStatusLineSlot(root *orderedObject) statusLineSlot {
	raw, ok := root.get("statusLine")
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return statusLineSlot{}
	}
	obj, err := parseObject(raw)
	if err != nil {
		// Present, but not an object: it is not ours, and not a command.
		return statusLineSlot{present: true}
	}
	s := statusLineSlot{present: true, obj: obj}
	var typ, cmd string
	if v, ok := obj.get("type"); ok {
		_ = json.Unmarshal(v, &typ)
	}
	if v, ok := obj.get("command"); ok {
		_ = json.Unmarshal(v, &cmd)
	}
	s.command = cmd
	s.isCmd = (typ == "" || typ == "command") && cmd != ""
	return s
}

// editStatusLine installs or removes the managed statusLine entry. It keeps
// every other key of the settings file, and every key of the entry but its
// command, byte for byte.
func editStatusLine(path string, have []byte, tuios, then string, install bool) ([]byte, error) {
	root, err := parseObject(have)
	if err != nil {
		return nil, err
	}
	slot := readStatusLineSlot(root)
	var managedThen string
	managed := false
	if slot.isCmd {
		if _, _, t, ok := managedStatusLine(slot.command); ok {
			managed, managedThen = true, t
		}
	}
	if !install {
		if !managed {
			return have, nil
		}
		if managedThen == "" {
			root.del("statusLine")
		} else {
			cmd, _ := marshalPlain(managedThen)
			slot.obj.set("command", cmd)
			root.set("statusLine", slot.obj.compact())
		}
		return renderIfChanged(root, have)
	}
	switch {
	case !slot.present:
		slot.obj = newObject()
		typ, _ := marshalPlain("command")
		slot.obj.set("type", typ)
	case managed:
		// A chain installed before stays unless a new one is named: it is
		// the person's command.
		if then == "" {
			then = managedThen
		}
	case slot.isCmd && then != "" && then == slot.command:
		// The person asked to chain to the command already there.
	default:
		cmd := ""
		if slot.isCmd {
			cmd = slot.command
		}
		return nil, &StatusLineOwnedError{Path: path, Command: cmd}
	}
	cmd, _ := marshalPlain(StatusLineCommand(tuios, then))
	slot.obj.set("command", cmd)
	root.set("statusLine", slot.obj.compact())
	return renderIfChanged(root, have)
}

func renderIfChanged(root *orderedObject, have []byte) ([]byte, error) {
	out, err := root.render()
	if err != nil {
		return nil, err
	}
	if sameJSON(have, out) {
		return have, nil
	}
	return out, nil
}

// InstallStatusLine points the harness's status line at the tuios wrapper,
// chaining to then when it is set. It refuses, with a StatusLineOwnedError, a
// slot holding a status line the person wrote, unless then is that command.
func (t *Target) InstallStatusLine(env Env, tuios, then string) (Result, error) {
	res := Result{Harness: t.ID, Path: t.Path(env)}
	if !t.SupportsStatusLine() {
		return res, fmt.Errorf("%s has no status line command tuios can feed from. Only Claude Code does", t.Name)
	}
	if st, err := os.Stat(t.ConfigDir(env)); err != nil || !st.IsDir() {
		return res, fmt.Errorf("%w: %s. Install %s and run it once, then try again", ErrNoConfigDir, t.ConfigDir(env), t.Name)
	}
	return t.writeStatusLine(env, &res, tuios, then, true)
}

// UninstallStatusLine removes the wrapper, putting back the command it chained
// to. A status line tuios did not write is left alone.
func (t *Target) UninstallStatusLine(env Env) (Result, error) {
	res := Result{Harness: t.ID, Path: t.Path(env)}
	if !t.SupportsStatusLine() {
		return res, nil
	}
	return t.writeStatusLine(env, &res, "", "", false)
}

func (t *Target) writeStatusLine(env Env, res *Result, tuios, then string, install bool) (Result, error) {
	path := t.Path(env)
	have, err := readOptional(path)
	if err != nil {
		return *res, err
	}
	if !install && have == nil {
		return *res, nil
	}
	out, err := editStatusLine(path, have, tuios, then, install)
	if err != nil {
		if _, ok := errors.AsType[*StatusLineOwnedError](err); ok {
			return *res, err
		}
		return *res, fmt.Errorf("%s: %w. It was left unchanged", path, err)
	}
	if string(out) == string(have) {
		return *res, nil
	}
	if err := writeAtomic(path, out); err != nil {
		return *res, err
	}
	res.Changed = true
	res.Paths = []string{path}
	if have != nil {
		res.Backup = path + BackupSuffix
	}
	return *res, nil
}

// StatusLineState reports what holds the status line slot. tuios is the
// command a current entry runs.
func (t *Target) StatusLineState(env Env, tuios string) StatusLineStatus {
	if !t.SupportsStatusLine() {
		return StatusLineStatus{}
	}
	st := StatusLineStatus{Supported: true}
	have, err := readOptional(t.Path(env))
	if err != nil || have == nil {
		return st
	}
	root, err := parseObject(have)
	if err != nil {
		return st
	}
	slot := readStatusLineSlot(root)
	if !slot.present {
		return st
	}
	if slot.isCmd {
		if program, version, then, ok := managedStatusLine(slot.command); ok {
			st.Installed, st.Version, st.Then = true, version, then
			st.Current = program == tuios && version == StatusLineVersion
			return st
		}
		st.Command = slot.command
	}
	st.Foreign = true
	return st
}
