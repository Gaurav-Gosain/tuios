package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
)

type fileFormat interface {
	apply(t *Target, have []byte, run Runner, install bool) (out []byte, changed, remove bool, err error)
	state(t *Target, have []byte, run Runner) (installed, current bool, version int, err error)
	owned() bool
}

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

type nestedHooks struct {
	matcher string
}

func (f nestedHooks) apply(t *Target, have []byte, run Runner, install bool) ([]byte, bool, bool, error) {
	if !install && have == nil {
		return nil, false, false, nil
	}
	cmd := ""
	if install {
		cmd = run.command(t.ID, t.Version)
	}
	out, changed, err := editHooks(have, t.Events, cmd, install, f.matcher)
	return out, changed, false, err
}

func (nestedHooks) state(t *Target, have []byte, run Runner) (bool, bool, int, error) {
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
	return len(entries) > 0, managedCurrent(entries, t.Events, run.command(t.ID, t.Version)), version, nil
}

func (nestedHooks) owned() bool { return false }

type flatHooks struct {
	entry    func(command string, ev HookEvent) map[string]any
	version1 bool
}

func flatManaged(root *orderedObject) ([]managedEntry, error) {
	raw, ok := root.get("hooks")
	if !ok {
		return nil, nil
	}
	hooks, err := parseObject(raw)
	if err != nil {
		return nil, errfmt.Errorf("hooks is not an object: %w", err)
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
			if jsonx.Unmarshal(item, &h) == nil && isManagedCommand(h.Command) {
				out = append(out, managedEntry{Event: event, Command: h.Command})
			}
		}
	}
	return out, nil
}

func (f flatHooks) apply(t *Target, have []byte, run Runner, install bool) ([]byte, bool, bool, error) {
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
			return nil, false, false, errfmt.Errorf("hooks is not an object: %w", err)
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
			if jsonx.Unmarshal(item, &h) == nil && isManagedCommand(h.Command) {
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
		cmd := run.command(t.ID, t.Version)
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

func (flatHooks) state(t *Target, have []byte, run Runner) (bool, bool, int, error) {
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
	return len(entries) > 0, managedCurrent(entries, t.Events, run.command(t.ID, t.Version)), version, nil
}

func (flatHooks) owned() bool { return false }

type ownedFile struct {
	render func(t *Target, run Runner) []byte
}

func isManagedFile(data []byte) bool {
	if bytes.Contains(data, []byte("TUIOS_INTEGRATION_ID=")) {
		return true
	}
	for _, m := range managedCommandRe().FindAll(data, -1) {
		if isManagedCommand(string(m)) {
			return true
		}
	}
	return false
}

var managedCommandRe = lazyre.New(` agent-hook [a-z-]+ ` + managedMarker + ` \d+`)

func (f ownedFile) apply(t *Target, have []byte, run Runner, install bool) ([]byte, bool, bool, error) {
	if have != nil && !isManagedFile(have) {
		if install {
			return nil, false, false, errNotOurs
		}
		return nil, false, false, nil
	}
	if !install {
		return nil, have != nil, have != nil, nil
	}
	want := f.render(t, run)
	return want, !bytes.Equal(have, want), false, nil
}

func (f ownedFile) state(t *Target, have []byte, run Runner) (bool, bool, int, error) {
	if have == nil || !isManagedFile(have) {
		return false, false, 0, nil
	}
	return true, bytes.Equal(have, f.render(t, run)), maxVersion(have), nil
}

func (ownedFile) owned() bool { return true }

var errNotOurs = errors.New("exists and was not written by tuios, so it is left alone")

type namedBlock struct {
	key string
}

func (f namedBlock) block(t *Target, run Runner) (json.RawMessage, error) {
	cmd := run.command(t.ID, t.Version)
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

func (f namedBlock) apply(t *Target, have []byte, run Runner, install bool) ([]byte, bool, bool, error) {
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
		return nil, false, false, errfmt.Errorf("its %q entry was not written by tuios, so it is left alone", f.key)
	}
	if install {
		want, err := f.block(t, run)
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

func (f namedBlock) state(t *Target, have []byte, run Runner) (bool, bool, int, error) {
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
	want, err := f.block(t, run)
	if err != nil {
		return false, false, 0, err
	}
	return true, sameJSON(cur, want), maxVersion(cur), nil
}

func (namedBlock) owned() bool { return false }

type tomlBlock struct {
	named bool
}

const (
	tomlBlockBegin = "# >>> tuios integration: managed by tuios integration install, removed by uninstall"
	tomlBlockEnd   = "# <<< tuios integration"
)

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

func (f tomlBlock) block(t *Target, run Runner) string {
	cmd := run.command(t.ID, t.Version)
	var b strings.Builder
	b.WriteString(tomlBlockBegin + "\n")
	for _, ev := range t.Events {
		if f.named {
			fmt.Fprintf(&b, "[[hooks]]\nname = %s\ntype = %s\ncommand = %s\ntimeout = %d\n\n", tomlString("tuios-"+ev.Name), tomlString(ev.Name), tomlString(cmd), ev.Timeout)
		} else {
			fmt.Fprintf(&b, "[[hooks]]\nevent = %s\ncommand = %s\ntimeout = %d\n\n", tomlString(ev.Name), tomlString(cmd), ev.Timeout)
		}
	}
	b.WriteString(tomlBlockEnd + "\n")
	return b.String()
}

var inlineHooksRe = lazyre.New(`(?m)^\s*hooks\s*=`)

func (f tomlBlock) apply(t *Target, have []byte, run Runner, install bool) ([]byte, bool, bool, error) {
	if !install && have == nil {
		return nil, false, false, nil
	}
	outside, _, err := splitBlock(string(have), tomlBlockBegin, tomlBlockEnd)
	if err != nil {
		return nil, false, false, err
	}
	out := outside
	if install {
		if inlineHooksRe().MatchString(outside) {
			return nil, false, false, errors.New("it sets hooks as an inline array, which tuios's [[hooks]] tables cannot join. Move your hooks to [[hooks]] tables and install again")
		}
		trimmed := strings.TrimRight(outside, "\n")
		if trimmed != "" {
			trimmed += "\n\n"
		}
		out = trimmed + f.block(t, run)
	}
	return []byte(out), out != string(have), false, nil
}

func (f tomlBlock) state(t *Target, have []byte, run Runner) (bool, bool, int, error) {
	_, block, err := splitBlock(string(have), tomlBlockBegin, tomlBlockEnd)
	if err != nil || block == "" {
		return false, false, 0, err
	}
	return true, block == f.block(t, run), maxVersion([]byte(block)), nil
}

func (tomlBlock) owned() bool { return false }
