package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// fileFormat is how tuios's part of one file is written, found and removed.
// Every harness keeps its hooks somewhere different, in a different shape, and
// each shape is one of these. apply and state never touch the disk: the target
// reads the file, hands its bytes over, and writes what comes back, so every
// format gets the same atomic write, backup and symlink handling.
type fileFormat interface {
	// apply returns the file with tuios's part as this build installs it
	// (install) or with it removed. changed says the content differs from
	// have; remove says the file should be deleted rather than written. have
	// is nil when the file does not exist.
	apply(t *Target, have []byte, tuios string, install bool) (out []byte, changed, remove bool, err error)
	// state reads what is installed: whether any of tuios's part is there,
	// whether it is exactly what tuios would install, and the integration
	// version it carries (0 when it names none).
	state(t *Target, have []byte, tuios string) (installed, current bool, version int, err error)
	// owned reports whether tuios owns the whole file, so there is no user
	// content in it to back up.
	owned() bool
}

// maxVersion is the highest integration version data names, 0 when none.
func maxVersion(data []byte) int {
	best := 0
	for _, m := range versionRe.FindAllStringSubmatch(string(data), -1) {
		var v int
		_, _ = fmt.Sscanf(m[1]+m[2], "%d", &v)
		if v > best {
			best = v
		}
	}
	return best
}

// nestedHooks is Claude Code's hooks shape, which Codex, Gemini CLI, Qwen
// Code, Qoder CLI, Droid and Devin CLI share: a "hooks" object keyed by event,
// each an array of matcher groups holding command hooks.
type nestedHooks struct {
	matcher string
}

func (f nestedHooks) apply(t *Target, have []byte, tuios string, install bool) ([]byte, bool, bool, error) {
	if !install && have == nil {
		return nil, false, false, nil
	}
	cmd := ""
	if install {
		cmd = HookCommand(tuios, t.ID, t.Version)
	}
	out, changed, err := editHooks(have, t.Events, cmd, install, f.matcher)
	return out, changed, false, err
}

func (nestedHooks) state(t *Target, have []byte, tuios string) (bool, bool, int, error) {
	if have == nil {
		return false, false, 0, nil
	}
	entries, err := findManaged(have)
	if err != nil {
		return false, false, 0, err
	}
	version := 0
	for _, e := range entries {
		if v := parseVersion(e.Command); v > version {
			version = v
		}
	}
	return len(entries) > 0, managedCurrent(entries, t.Events, HookCommand(tuios, t.ID, t.Version)), version, nil
}

func (nestedHooks) owned() bool { return false }

// flatHooks is the shape where each event's array holds the hook objects
// themselves, with no matcher group around them: Crush's crush.json and
// Cursor's hooks.json. entry builds the object for one event. version1 adds a
// top-level "version": 1 to a file that has none, which Cursor's format needs.
type flatHooks struct {
	entry    func(command string, ev HookEvent) map[string]any
	version1 bool
}

// flatManaged lists the managed commands in a flat hooks document.
func flatManaged(root *orderedObject) ([]managedEntry, error) {
	raw, ok := root.get("hooks")
	if !ok {
		return nil, nil
	}
	hooks, err := parseObject(raw)
	if err != nil {
		return nil, fmt.Errorf("hooks is not an object: %w", err)
	}
	var out []managedEntry
	for _, event := range hooks.keys {
		items, err := hookGroups(event, hooks.vals[event])
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			var h struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(item, &h) == nil && isManagedCommand(h.Command) {
				out = append(out, managedEntry{Event: event, Command: h.Command})
			}
		}
	}
	return out, nil
}

func (f flatHooks) apply(t *Target, have []byte, tuios string, install bool) ([]byte, bool, bool, error) {
	if !install && have == nil {
		return nil, false, false, nil
	}
	root, err := parseObject(have)
	if err != nil {
		return nil, false, false, err
	}
	hooks := newObject()
	hadHooks := false
	if raw, ok := root.get("hooks"); ok {
		hadHooks = true
		if hooks, err = parseObject(raw); err != nil {
			return nil, false, false, fmt.Errorf("hooks is not an object: %w", err)
		}
	}
	removedAny := false
	for _, event := range append([]string(nil), hooks.keys...) {
		items, err := hookGroups(event, hooks.vals[event])
		if err != nil {
			return nil, false, false, err
		}
		kept := items[:0:0]
		for _, item := range items {
			var h struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(item, &h) == nil && isManagedCommand(h.Command) {
				removedAny = true
				continue
			}
			kept = append(kept, item)
		}
		if len(kept) == len(items) {
			continue
		}
		if len(kept) == 0 {
			hooks.del(event)
			continue
		}
		hooks.set(event, joinArray(kept))
	}
	if install {
		cmd := HookCommand(tuios, t.ID, t.Version)
		for _, ev := range t.Events {
			entry, err := marshalPlain(f.entry(cmd, ev))
			if err != nil {
				return nil, false, false, err
			}
			var items []json.RawMessage
			if raw, ok := hooks.get(ev.Name); ok {
				if items, err = hookGroups(ev.Name, raw); err != nil {
					return nil, false, false, err
				}
			}
			hooks.set(ev.Name, joinArray(append(items, entry)))
		}
		if f.version1 {
			if _, ok := root.get("version"); !ok {
				root.set("version", json.RawMessage("1"))
			}
		}
	}
	switch {
	case !hooks.empty():
		root.set("hooks", hooks.compact())
	case hadHooks && removedAny:
		root.del("hooks")
	}
	out, err := root.render()
	if err != nil {
		return nil, false, false, err
	}
	return out, !sameJSON(have, out), false, nil
}

func (flatHooks) state(t *Target, have []byte, tuios string) (bool, bool, int, error) {
	if have == nil {
		return false, false, 0, nil
	}
	root, err := parseObject(have)
	if err != nil {
		return false, false, 0, err
	}
	entries, err := flatManaged(root)
	if err != nil {
		return false, false, 0, err
	}
	version := 0
	for _, e := range entries {
		if v := parseVersion(e.Command); v > version {
			version = v
		}
	}
	return len(entries) > 0, managedCurrent(entries, t.Events, HookCommand(tuios, t.ID, t.Version)), version, nil
}

func (flatHooks) owned() bool { return false }

// ownedFile is a file tuios writes whole: a plugin, an extension, or a hook
// file in a directory the harness merges. A file at the path that tuios did
// not write is refused and never removed.
type ownedFile struct {
	render func(t *Target, tuios string) []byte
}

// isManagedFile reports whether a file is one tuios wrote: a plugin carries
// TUIOS_INTEGRATION_ID, and a JSON hook file, which has nowhere for a comment,
// carries the managed command.
func isManagedFile(data []byte) bool {
	if bytes.Contains(data, []byte("TUIOS_INTEGRATION_ID=")) {
		return true
	}
	for _, m := range managedCommandRe.FindAll(data, -1) {
		if isManagedCommand(string(m)) {
			return true
		}
	}
	return false
}

// managedCommandRe finds an agent-hook command with its version marker.
var managedCommandRe = regexp.MustCompile(` agent-hook [a-z-]+ ` + managedMarker + ` \d+`)

func (f ownedFile) apply(t *Target, have []byte, tuios string, install bool) ([]byte, bool, bool, error) {
	if have != nil && !isManagedFile(have) {
		if install {
			return nil, false, false, errNotOurs
		}
		return nil, false, false, nil
	}
	if !install {
		return nil, have != nil, have != nil, nil
	}
	want := f.render(t, tuios)
	return want, !bytes.Equal(have, want), false, nil
}

func (f ownedFile) state(t *Target, have []byte, tuios string) (bool, bool, int, error) {
	if have == nil || !isManagedFile(have) {
		return false, false, 0, nil
	}
	return true, bytes.Equal(have, f.render(t, tuios)), maxVersion(have), nil
}

func (ownedFile) owned() bool { return true }

// errNotOurs is a file at a path tuios owns that tuios did not write.
var errNotOurs = errors.New("exists and was not written by tuios, so it is left alone")

// namedBlock is a JSON object keyed by hook name, where tuios owns one key:
// Antigravity CLI's hooks.json. The block maps each event to a flat list of
// command handlers.
type namedBlock struct {
	key string
}

func (f namedBlock) block(t *Target, tuios string) (json.RawMessage, error) {
	cmd := HookCommand(tuios, t.ID, t.Version)
	b := newObject()
	for _, ev := range t.Events {
		h, err := marshalPlain([]map[string]any{{"type": "command", "command": cmd, "timeout": ev.Timeout}})
		if err != nil {
			return nil, err
		}
		b.set(ev.Name, h)
	}
	return b.compact(), nil
}

func (f namedBlock) apply(t *Target, have []byte, tuios string, install bool) ([]byte, bool, bool, error) {
	if !install && have == nil {
		return nil, false, false, nil
	}
	root, err := parseObject(have)
	if err != nil {
		return nil, false, false, err
	}
	cur, ok := root.get(f.key)
	if ok && !isManagedFile(cur) {
		if !install {
			return nil, false, false, nil
		}
		return nil, false, false, fmt.Errorf("its %q entry was not written by tuios, so it is left alone", f.key)
	}
	if install {
		want, err := f.block(t, tuios)
		if err != nil {
			return nil, false, false, err
		}
		root.set(f.key, want)
	} else {
		root.del(f.key)
	}
	out, err := root.render()
	if err != nil {
		return nil, false, false, err
	}
	return out, !sameJSON(have, out), false, nil
}

func (f namedBlock) state(t *Target, have []byte, tuios string) (bool, bool, int, error) {
	if have == nil {
		return false, false, 0, nil
	}
	root, err := parseObject(have)
	if err != nil {
		return false, false, 0, err
	}
	cur, ok := root.get(f.key)
	if !ok || !isManagedFile(cur) {
		return false, false, 0, nil
	}
	want, err := f.block(t, tuios)
	if err != nil {
		return false, false, 0, err
	}
	return true, sameJSON(cur, want), maxVersion(cur), nil
}

func (namedBlock) owned() bool { return false }

// tomlBlock is a run of [[hooks]] tables tuios appends to a TOML config
// between two marker comments: Kimi Code CLI's config.toml. Everything outside
// the markers is the user's and is kept byte for byte.
type tomlBlock struct{}

const (
	tomlBlockBegin = "# >>> tuios integration: managed by tuios integration install, removed by uninstall"
	tomlBlockEnd   = "# <<< tuios integration"
)

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func (tomlBlock) block(t *Target, tuios string) string {
	cmd := HookCommand(tuios, t.ID, t.Version)
	var b strings.Builder
	b.WriteString(tomlBlockBegin + "\n")
	for _, ev := range t.Events {
		fmt.Fprintf(&b, "[[hooks]]\nevent = %s\ncommand = %s\ntimeout = %d\n\n", tomlString(ev.Name), tomlString(cmd), ev.Timeout)
	}
	b.WriteString(tomlBlockEnd + "\n")
	return b.String()
}

// splitTOMLBlock returns the file without tuios's block, and the block.
func splitTOMLBlock(text string) (outside, block string, err error) {
	start := strings.Index(text, tomlBlockBegin)
	if start < 0 {
		return text, "", nil
	}
	rel := strings.Index(text[start:], tomlBlockEnd)
	if rel < 0 {
		return "", "", errors.New("tuios's block has a start marker and no end marker")
	}
	end := start + rel + len(tomlBlockEnd)
	if end < len(text) && text[end] == '\n' {
		end++
	}
	block = text[start:end]
	before := strings.TrimRight(text[:start], "\n")
	after := text[end:]
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

// inlineHooksRe finds a top-level hooks = [...] key, which [[hooks]] tables
// cannot be added to.
var inlineHooksRe = regexp.MustCompile(`(?m)^\s*hooks\s*=`)

func (f tomlBlock) apply(t *Target, have []byte, tuios string, install bool) ([]byte, bool, bool, error) {
	if !install && have == nil {
		return nil, false, false, nil
	}
	outside, _, err := splitTOMLBlock(string(have))
	if err != nil {
		return nil, false, false, err
	}
	out := outside
	if install {
		if inlineHooksRe.MatchString(outside) {
			return nil, false, false, errors.New("it sets hooks as an inline array, which tuios's [[hooks]] tables cannot join. Move your hooks to [[hooks]] tables and install again")
		}
		trimmed := strings.TrimRight(outside, "\n")
		if trimmed != "" {
			trimmed += "\n\n"
		}
		out = trimmed + f.block(t, tuios)
	}
	return []byte(out), out != string(have), false, nil
}

func (f tomlBlock) state(t *Target, have []byte, tuios string) (bool, bool, int, error) {
	_, block, err := splitTOMLBlock(string(have))
	if err != nil || block == "" {
		return false, false, 0, err
	}
	return true, block == f.block(t, tuios), maxVersion([]byte(block)), nil
}

func (tomlBlock) owned() bool { return false }
