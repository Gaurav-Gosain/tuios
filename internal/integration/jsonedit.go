package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// orderedObject is a JSON object that keeps its keys in the order it read
// them and every value it does not touch byte for byte. A harness's settings
// file is the user's, so rewriting it has to change only the entries tuios
// owns: a map would sort every key the user wrote, and a struct would drop
// every key tuios does not know.
type orderedObject struct {
	keys []string
	vals map[string]json.RawMessage
}

func newObject() *orderedObject {
	return &orderedObject{vals: map[string]json.RawMessage{}}
}

// parseObject reads a JSON object. An empty or blank document is an empty
// object, which is what a harness that has no settings file yet means.
func parseObject(data []byte) (*orderedObject, error) {
	o := newObject()
	if len(bytes.TrimSpace(data)) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, errors.New("object key is not a string")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		o.set(key, raw)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, errors.New("trailing data after the object")
	}
	return o, nil
}

func (o *orderedObject) get(key string) (json.RawMessage, bool) {
	v, ok := o.vals[key]
	return v, ok
}

func (o *orderedObject) set(key string, v json.RawMessage) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

func (o *orderedObject) del(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *orderedObject) empty() bool { return len(o.keys) == 0 }

// compact writes the object on one line, values as they were read.
func (o *orderedObject) compact() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		b.Write(key)
		b.WriteByte(':')
		_ = json.Compact(&b, o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}

// render writes the object indented by two spaces with a final newline, the
// layout every one of these harnesses writes its own settings in.
func (o *orderedObject) render() ([]byte, error) {
	var out bytes.Buffer
	if err := json.Indent(&out, o.compact(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// HookEvent is one event a managed integration registers a command for.
type HookEvent struct {
	Name string
	// Timeout is in the harness's own unit: seconds for Claude Code and Codex,
	// milliseconds for Gemini CLI.
	Timeout int
}

// managedMarker is in every command tuios writes. It is how a later install,
// uninstall or status tells tuios's entries from the user's own, and it
// carries the integration version. `tuios agent-hook` accepts and ignores it.
const managedMarker = "--integration"

// isManagedCommand reports whether a hook command is one tuios wrote.
func isManagedCommand(cmd string) bool {
	return strings.Contains(cmd, " agent-hook ") && strings.Contains(cmd, " "+managedMarker+" ")
}

// managedEntry is one managed command found in a hooks file.
type managedEntry struct {
	Event   string
	Command string
}

// hookGroups decodes an event's array of matcher groups. A value that is not
// an array is an error: rewriting it would destroy something tuios does not
// understand.
func hookGroups(event string, raw json.RawMessage) ([]json.RawMessage, error) {
	var groups []json.RawMessage
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, fmt.Errorf("hooks.%s is not an array", event)
	}
	return groups, nil
}

// groupCommands lists the command of every command hook in a matcher group.
func groupCommands(group json.RawMessage) []string {
	var g struct {
		Hooks []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(group, &g) != nil {
		return nil
	}
	var out []string
	for _, h := range g.Hooks {
		if h.Command != "" {
			out = append(out, h.Command)
		}
	}
	return out
}

// findManaged lists the managed commands in a settings document.
func findManaged(doc []byte) ([]managedEntry, error) {
	root, err := parseObject(doc)
	if err != nil {
		return nil, err
	}
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
		groups, err := hookGroups(event, hooks.vals[event])
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			for _, cmd := range groupCommands(g) {
				if isManagedCommand(cmd) {
					out = append(out, managedEntry{Event: event, Command: cmd})
				}
			}
		}
	}
	return out, nil
}

// stripManaged removes every managed command from a matcher group. It returns
// the group to keep, or nil when the group held nothing but managed commands,
// and whether it changed anything.
func stripManaged(group json.RawMessage) (json.RawMessage, bool, error) {
	obj, err := parseObject(group)
	if err != nil {
		// Not an object: not something tuios wrote, so kept as it is.
		return group, false, nil
	}
	rawHooks, ok := obj.get("hooks")
	if !ok {
		return group, false, nil
	}
	var items []json.RawMessage
	if json.Unmarshal(rawHooks, &items) != nil {
		return group, false, nil
	}
	kept := items[:0:0]
	removed := false
	for _, item := range items {
		var h struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(item, &h) == nil && isManagedCommand(h.Command) {
			removed = true
			continue
		}
		kept = append(kept, item)
	}
	if !removed {
		return group, false, nil
	}
	if len(kept) == 0 {
		return nil, true, nil
	}
	arr, err := json.Marshal(kept)
	if err != nil {
		return nil, false, err
	}
	obj.set("hooks", arr)
	return obj.compact(), true, nil
}

// editHooks rewrites a settings document's hooks object: every managed
// command is removed, and when install is set one managed group is added per
// event. Everything else in the document is kept as it was, in its order.
//
// It returns the new document and whether it differs from the old one, so a
// caller writes nothing when nothing changed: installing twice is a no-op.
func editHooks(doc []byte, events []HookEvent, command string, install bool) ([]byte, bool, error) {
	root, err := parseObject(doc)
	if err != nil {
		return nil, false, err
	}
	hooks := newObject()
	hadHooks := false
	if raw, ok := root.get("hooks"); ok {
		hadHooks = true
		if hooks, err = parseObject(raw); err != nil {
			return nil, false, fmt.Errorf("hooks is not an object: %w", err)
		}
	}
	removedAny := false
	for _, event := range append([]string(nil), hooks.keys...) {
		groups, err := hookGroups(event, hooks.vals[event])
		if err != nil {
			return nil, false, err
		}
		var kept []json.RawMessage
		changed := false
		for _, g := range groups {
			ng, ch, err := stripManaged(g)
			if err != nil {
				return nil, false, err
			}
			changed = changed || ch
			if ng != nil {
				kept = append(kept, ng)
			}
		}
		if !changed {
			continue
		}
		removedAny = true
		if len(kept) == 0 {
			hooks.del(event)
			continue
		}
		arr, err := json.Marshal(kept)
		if err != nil {
			return nil, false, err
		}
		hooks.set(event, arr)
	}
	if install {
		for _, ev := range events {
			group, err := json.Marshal(map[string]any{
				"hooks": []map[string]any{{"type": "command", "command": command, "timeout": ev.Timeout}},
			})
			if err != nil {
				return nil, false, err
			}
			var groups []json.RawMessage
			if raw, ok := hooks.get(ev.Name); ok {
				if groups, err = hookGroups(ev.Name, raw); err != nil {
					return nil, false, err
				}
			}
			groups = append(groups, group)
			arr, err := json.Marshal(groups)
			if err != nil {
				return nil, false, err
			}
			hooks.set(ev.Name, arr)
		}
	}
	switch {
	case !hooks.empty():
		root.set("hooks", hooks.compact())
	case hadHooks && removedAny:
		// Emptied by removing tuios's own entries, so the key goes too.
		root.del("hooks")
	}
	out, err := root.render()
	if err != nil {
		return nil, false, err
	}
	return out, !sameJSON(doc, out), nil
}

// sameJSON reports whether two documents hold the same JSON, ignoring layout.
func sameJSON(a, b []byte) bool {
	var ca, cb bytes.Buffer
	if len(bytes.TrimSpace(a)) == 0 {
		a = []byte("{}")
	}
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return false
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}
