package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// MCP registration: the entry that makes a harness start tuios mcp.
//
// It is separate from the hooks. The hooks report the pane's state and are
// wanted on every machine with the harness; the MCP server adds tools to every
// session of the harness, which costs context, so it is only written when the
// install asks for it with --mcp. Uninstall removes it with the hooks.
//
// Each harness keeps its MCP servers in its own place and shape:
//
//   - Claude Code: mcpServers in ~/.claude.json (or $CLAUDE_CONFIG_DIR/.claude.json),
//     the user scope that claude mcp add --scope user writes.
//   - Gemini CLI: mcpServers in ~/.gemini/settings.json.
//   - opencode: mcp in ~/.config/opencode/opencode.json, a local server whose
//     command is one array.
//   - Codex: an [mcp_servers.tuios] table in ~/.codex/config.toml, between
//     marker comments, the way the Kimi hooks are written.
//
// tuios owns the one server named tuios. The entry it writes runs tuios mcp
// with --integration and a version, which is how a later install, uninstall
// or status tells it from an entry the user wrote under the same name; an
// entry named tuios without the marker is never overwritten or removed.

// MCPVersion is the version of the MCP registration this build writes.
const MCPVersion = 1

// MCPServerName is the name tuios registers its server under.
const MCPServerName = "tuios"

// mcpShape is how one harness lists its MCP servers.
type mcpShape int

const (
	mcpClaudeJSON   mcpShape = iota // "mcpServers": {"tuios": {"type": "stdio", "command", "args"}}
	mcpGeminiJSON                   // "mcpServers": {"tuios": {"command", "args"}}
	mcpOpenCodeJSON                 // "mcp": {"tuios": {"type": "local", "command": [...], "enabled": true}}
	mcpCodexTOML                    // [mcp_servers.tuios] command, args
)

// mcpTarget is where one harness reads its MCP servers.
type mcpTarget struct {
	shape mcpShape
	path  func(t *Target, env Env) string
}

var mcpTargets = map[string]mcpTarget{
	ClaudeCode: {shape: mcpClaudeJSON, path: func(_ *Target, env Env) string {
		if dir := strings.TrimSpace(env.env("CLAUDE_CONFIG_DIR")); dir != "" {
			return filepath.Join(env.dirFromEnv("CLAUDE_CONFIG_DIR"), ".claude.json")
		}
		return filepath.Join(env.Home, ".claude.json")
	}},
	GeminiCLI: {shape: mcpGeminiJSON, path: func(t *Target, env Env) string { return filepath.Join(t.ConfigDir(env), "settings.json") }},
	OpenCode:  {shape: mcpOpenCodeJSON, path: func(t *Target, env Env) string { return filepath.Join(t.ConfigDir(env), "opencode.json") }},
	Codex:     {shape: mcpCodexTOML, path: func(t *Target, env Env) string { return filepath.Join(t.ConfigDir(env), "config.toml") }},
}

// SupportsMCP reports whether tuios can register its MCP server with the
// harness.
func (t *Target) SupportsMCP() bool {
	_, ok := mcpTargets[t.ID]
	return ok
}

// MCPHarnessIDs lists the harnesses tuios can register its MCP server with.
func MCPHarnessIDs() []string {
	var out []string
	for _, t := range targets {
		if t.SupportsMCP() {
			out = append(out, t.ID)
		}
	}
	return out
}

// MCPPath is the file the harness reads its MCP servers from, "" for a harness
// tuios cannot register with.
func (t *Target) MCPPath(env Env) string {
	m, ok := mcpTargets[t.ID]
	if !ok {
		return ""
	}
	return m.path(t, env)
}

// MCPArgs are the arguments the registered server runs tuios with.
func MCPArgs(write bool) []string {
	args := []string{"mcp"}
	if write {
		args = append(args, "--write")
	}
	return append(args, managedMarker, strconv.Itoa(MCPVersion))
}

// isManagedMCPArgs reports whether args are ones tuios wrote: they run mcp
// and carry the version marker.
func isManagedMCPArgs(args []string) bool {
	return len(args) >= 3 && args[0] == "mcp" && slices.Contains(args, managedMarker)
}

// mcpEntry is the server entry for one JSON shape.
func mcpEntry(shape mcpShape, tuios string, write bool) map[string]any {
	args := MCPArgs(write)
	switch shape {
	case mcpClaudeJSON:
		return map[string]any{"type": "stdio", "command": tuios, "args": args}
	case mcpOpenCodeJSON:
		return map[string]any{"type": "local", "command": append([]string{tuios}, args...), "enabled": true}
	default:
		return map[string]any{"command": tuios, "args": args}
	}
}

// mcpKey is the object that holds the servers in a JSON shape.
func mcpKey(shape mcpShape) string {
	if shape == mcpOpenCodeJSON {
		return "mcp"
	}
	return "mcpServers"
}

// readJSONEntry reads the command and args of the tuios entry in a JSON shape.
func readJSONEntry(shape mcpShape, raw json.RawMessage) (command string, args []string, ok bool) {
	var e struct {
		Command json.RawMessage `json:"command"`
		Args    []string        `json:"args"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return "", nil, false
	}
	if shape == mcpOpenCodeJSON {
		var argv []string
		if json.Unmarshal(e.Command, &argv) != nil || len(argv) == 0 {
			return "", nil, false
		}
		return argv[0], argv[1:], true
	}
	if json.Unmarshal(e.Command, &command) != nil {
		return "", nil, false
	}
	return command, e.Args, true
}

// MCPStatus is whether a harness has tuios's MCP server registered.
type MCPStatus struct {
	Supported bool   `json:"supported"`
	Path      string `json:"path,omitempty"`
	Installed bool   `json:"installed"`
	// Current says the entry runs the command status was asked about, at
	// this build's version.
	Current bool `json:"current"`
	// Write says the entry lists the tools that type into panes.
	Write   bool `json:"write,omitempty"`
	Version int  `json:"version,omitempty"`
	// Foreign says a server named tuios is there that tuios did not write.
	Foreign bool `json:"foreign,omitempty"`
}

// mcpPlan works out the file's new content. install false removes tuios's
// entry.
func (t *Target) mcpPlan(env Env, tuios string, write, install bool) (path string, have, out []byte, changed bool, err error) {
	m, ok := mcpTargets[t.ID]
	if !ok {
		return "", nil, nil, false, fmt.Errorf("tuios cannot register an MCP server with %s. It can with %s", t.Name, strings.Join(MCPHarnessIDs(), ", "))
	}
	path = m.path(t, env)
	have, err = readOptional(path)
	if err != nil {
		return path, nil, nil, false, err
	}
	if !install && have == nil {
		return path, nil, nil, false, nil
	}
	if m.shape == mcpCodexTOML {
		out, err = editMCPTOML(have, tuios, write, install)
	} else {
		out, err = editMCPJSON(m.shape, have, tuios, write, install)
	}
	if err != nil {
		return path, have, nil, false, fmt.Errorf("%s: %w. It was left unchanged", path, err)
	}
	return path, have, out, string(out) != string(have), nil
}

// InstallMCP registers tuios mcp with the harness, with --write when write is
// set. Like Install it needs the harness to have run here, and writes nothing
// when the entry is already current.
func (t *Target) InstallMCP(env Env, tuios string, write bool) (Result, error) {
	res := Result{Harness: t.ID, Path: t.MCPPath(env)}
	if st, err := os.Stat(t.ConfigDir(env)); err != nil || !st.IsDir() {
		return res, fmt.Errorf("%w: %s. Install %s and run it once, then try again", ErrNoConfigDir, t.ConfigDir(env), t.Name)
	}
	path, have, out, changed, err := t.mcpPlan(env, tuios, write, true)
	if err != nil {
		return res, err
	}
	res.Path = path
	if !changed {
		return res, nil
	}
	if err := writeAtomic(path, out); err != nil {
		return res, err
	}
	res.Changed = true
	res.Paths = []string{path}
	if have != nil {
		res.Backup = path + BackupSuffix
	}
	if t.ID == OpenCode {
		if _, err := os.Stat(filepath.Join(t.ConfigDir(env), "opencode.jsonc")); err == nil {
			res.Notes = append(res.Notes, "opencode.jsonc is also there. opencode reads both; check that it does not define a server named tuios too.")
		}
	}
	return res, nil
}

// UninstallMCP removes the entry tuios wrote, and nothing else.
func (t *Target) UninstallMCP(env Env) (Result, error) {
	res := Result{Harness: t.ID, Path: t.MCPPath(env)}
	if !t.SupportsMCP() {
		return res, nil
	}
	path, _, out, changed, err := t.mcpPlan(env, "", false, false)
	if err != nil || !changed {
		return res, err
	}
	if err := writeAtomic(path, out); err != nil {
		return res, err
	}
	res.Changed = true
	res.Paths = []string{path}
	return res, nil
}

// MCPState reports what is registered. tuios is the command a current entry
// runs.
func (t *Target) MCPState(env Env, tuios string) MCPStatus {
	m, ok := mcpTargets[t.ID]
	if !ok {
		return MCPStatus{}
	}
	st := MCPStatus{Supported: true, Path: m.path(t, env)}
	have, err := readOptional(st.Path)
	if err != nil || have == nil {
		return st
	}
	var command string
	var args []string
	found := false
	if m.shape == mcpCodexTOML {
		_, block, err := splitBlock(string(have), mcpTOMLBegin, mcpTOMLEnd)
		if err == nil && block != "" {
			command, args, found = parseMCPTOMLBlock(block)
		}
		if !found && codexForeignTable(string(have)) {
			st.Foreign = true
		}
	} else {
		root, err := parseObject(have)
		if err != nil {
			return st
		}
		if servers, ok := root.get(mcpKey(m.shape)); ok {
			if obj, err := parseObject(servers); err == nil {
				if raw, ok := obj.get(MCPServerName); ok {
					command, args, found = readJSONEntry(m.shape, raw)
					if !found || !isManagedMCPArgs(args) {
						st.Foreign, found = true, false
					}
				}
			}
		}
	}
	if !found {
		return st
	}
	st.Installed = true
	st.Write = slices.Contains(args, "--write")
	if i := slices.Index(args, managedMarker); i >= 0 && i+1 < len(args) {
		st.Version, _ = strconv.Atoi(args[i+1])
	}
	st.Current = command == tuios && slices.Equal(args, MCPArgs(st.Write))
	return st
}

// editMCPJSON sets or removes the tuios server in a JSON settings file,
// keeping every other key as it was.
func editMCPJSON(shape mcpShape, have []byte, tuios string, write, install bool) ([]byte, error) {
	root, err := parseObject(have)
	if err != nil {
		return nil, err
	}
	key := mcpKey(shape)
	servers := newObject()
	if raw, ok := root.get(key); ok {
		if servers, err = parseObject(raw); err != nil {
			return nil, fmt.Errorf("%s is not an object: %w", key, err)
		}
	}
	if raw, ok := servers.get(MCPServerName); ok {
		if _, args, ok := readJSONEntry(shape, raw); !ok || !isManagedMCPArgs(args) {
			if !install {
				return have, nil
			}
			return nil, fmt.Errorf("its %s server %q was not written by tuios, so it is left alone", key, MCPServerName)
		}
	}
	if install {
		entry, err := marshalPlain(mcpEntry(shape, tuios, write))
		if err != nil {
			return nil, err
		}
		servers.set(MCPServerName, entry)
	} else {
		if _, ok := servers.get(MCPServerName); !ok {
			return have, nil
		}
		servers.del(MCPServerName)
	}
	if servers.empty() {
		root.del(key)
	} else {
		root.set(key, servers.compact())
	}
	out, err := root.render()
	if err != nil {
		return nil, err
	}
	if sameJSON(have, out) {
		return have, nil
	}
	return out, nil
}

const (
	mcpTOMLBegin = "# >>> tuios mcp: managed by tuios integration install --mcp, removed by uninstall"
	mcpTOMLEnd   = "# <<< tuios mcp"
)

// splitBlock is splitTOMLBlock for any pair of markers.
func splitBlock(text, begin, end string) (outside, block string, err error) {
	start := strings.Index(text, begin)
	if start < 0 {
		return text, "", nil
	}
	rel := strings.Index(text[start:], end)
	if rel < 0 {
		return "", "", errors.New("tuios's block has a start marker and no end marker")
	}
	stop := start + rel + len(end)
	if stop < len(text) && text[stop] == '\n' {
		stop++
	}
	block = text[start:stop]
	before := strings.TrimRight(text[:start], "\n")
	after := text[stop:]
	switch {
	case before == "":
		outside = after
	case after == "":
		outside = before + "\n"
	default:
		outside = before + "\n\n" + after
	}
	return outside, block, nil
}

// codexTableRe finds a [mcp_servers.tuios] table header, however quoted.
var codexTableRe = regexp.MustCompile(`(?m)^\s*\[\s*mcp_servers\s*\.\s*(?:tuios|"tuios"|'tuios')\s*\]`)

// codexForeignTable reports whether a tuios server table sits outside tuios's
// block.
func codexForeignTable(text string) bool {
	outside, _, err := splitBlock(text, mcpTOMLBegin, mcpTOMLEnd)
	if err != nil {
		return false
	}
	return codexTableRe.MatchString(outside)
}

func mcpTOMLBlock(tuios string, write bool) string {
	args := MCPArgs(write)
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = tomlString(a)
	}
	var b strings.Builder
	b.WriteString(mcpTOMLBegin + "\n")
	b.WriteString("[mcp_servers." + MCPServerName + "]\n")
	b.WriteString("command = " + tomlString(tuios) + "\n")
	b.WriteString("args = [" + strings.Join(quoted, ", ") + "]\n")
	b.WriteString(mcpTOMLEnd + "\n")
	return b.String()
}

var (
	tomlCommandRe = regexp.MustCompile(`(?m)^command = "((?:[^"\\]|\\.)*)"$`)
	tomlArgsRe    = regexp.MustCompile(`(?m)^args = \[(.*)\]$`)
	tomlItemRe    = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
)

// parseMCPTOMLBlock reads back the command and args of a block tuios wrote.
func parseMCPTOMLBlock(block string) (string, []string, bool) {
	cm := tomlCommandRe.FindStringSubmatch(block)
	am := tomlArgsRe.FindStringSubmatch(block)
	if cm == nil || am == nil {
		return "", nil, false
	}
	unquote := func(s string) string {
		v, err := strconv.Unquote(`"` + s + `"`)
		if err != nil {
			return s
		}
		return v
	}
	var args []string
	for _, m := range tomlItemRe.FindAllStringSubmatch(am[1], -1) {
		args = append(args, unquote(m[1]))
	}
	return unquote(cm[1]), args, true
}

// editMCPTOML sets or removes tuios's block in Codex's config.toml. Everything
// outside the markers is the user's and is kept byte for byte.
func editMCPTOML(have []byte, tuios string, write, install bool) ([]byte, error) {
	outside, _, err := splitBlock(string(have), mcpTOMLBegin, mcpTOMLEnd)
	if err != nil {
		return nil, err
	}
	if !install {
		if outside == string(have) {
			return have, nil
		}
		return []byte(outside), nil
	}
	if codexTableRe.MatchString(outside) {
		return nil, fmt.Errorf("it defines [mcp_servers.%s] itself, so tuios leaves it alone", MCPServerName)
	}
	trimmed := strings.TrimRight(outside, "\n")
	if trimmed != "" {
		trimmed += "\n\n"
	}
	return []byte(trimmed + mcpTOMLBlock(tuios, write)), nil
}
