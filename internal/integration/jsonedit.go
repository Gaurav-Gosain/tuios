package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

type orderedObject struct {
	keys []string
	vals map[string]json.RawMessage
}

func newObject() *orderedObject {
	return &orderedObject{vals: map[string]json.RawMessage{}}
}

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

func (o *orderedObject) compact() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := marshalPlain(k)
		b.Write(key)
		b.WriteByte(':')
		_ = json.Compact(&b, o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}

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

const managedMarker = "--integration"

func isManagedCommand(cmd string) bool {
	return strings.Contains(cmd, " agent-hook ") && strings.Contains(cmd, " "+managedMarker+" ")
}

type managedEntry struct {
	Event   string
	Command string
}

func hookGroups(event string, raw json.RawMessage) ([]json.RawMessage, error) {
	var groups []json.RawMessage
	if err := jsonx.Unmarshal(raw, &groups); err != nil {
		return nil, errfmt.Errorf("hooks.%s is not an array", event)
	}
	return groups, nil
}

func groupCommands(group json.RawMessage) []string {
	var g struct {
		Hooks []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if jsonx.Unmarshal(group, &g) != nil {
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
		return nil, errfmt.Errorf("hooks is not an object: %w", err)
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

func stripManaged(group json.RawMessage) (json.RawMessage, bool, error) {
	obj, err := parseObject(group)
	if err != nil {
		return group, false, nil
	}
	rawHooks, ok := obj.get("hooks")
	if !ok {
		return group, false, nil
	}
	var items []json.RawMessage
	if jsonx.Unmarshal(rawHooks, &items) != nil {
		return group, false, nil
	}
	kept := items[:0:0]
	removed := false
	for _, item := range items {
		var h struct {
			Command string `json:"command"`
		}
		if jsonx.Unmarshal(item, &h) == nil && isManagedCommand(h.Command) {
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
	obj.set("hooks", joinArray(kept))
	return obj.compact(), true, nil
}

func marshalPlain(v any) (json.RawMessage, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

func joinArray(elems []json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, e := range elems {
		if i > 0 {
			b.WriteByte(',')
		}
		_ = json.Compact(&b, e)
	}
	b.WriteByte(']')
	return b.Bytes()
}

func editHooks(doc []byte, events []HookEvent, command string, install bool, matcher string) ([]byte, bool, error) {
	root, err := parseObject(doc)
	if err != nil {
		return nil, false, err
	}
	hooks := newObject()
	hadHooks := false
	if raw, ok := root.get("hooks"); ok {
		hadHooks = true
		if hooks, err = parseObject(raw); err != nil {
			return nil, false, errfmt.Errorf("hooks is not an object: %w", err)
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
		hooks.set(event, joinArray(kept))
	}
	if install {
		for _, ev := range events {
			g := map[string]any{
				"hooks": []map[string]any{{"type": "command", "command": command, "timeout": ev.Timeout}},
			}
			if matcher != "" {
				g["matcher"] = matcher
			}
			group, err := marshalPlain(g)
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
			hooks.set(ev.Name, joinArray(groups))
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
		return nil, false, err
	}
	return out, !sameJSON(doc, out), nil
}

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
