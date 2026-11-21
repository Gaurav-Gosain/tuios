package config

import (
	"bytes"
	"strconv"
	"strings"
)

// KeySource is where a binding is set. File is empty for a default binding.
type KeySource struct {
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
}

// String is the source as a warning writes it: file:line, the file, or "the
// defaults".
func (s KeySource) String() string {
	switch {
	case s.File == "":
		return "the defaults"
	case s.Line > 0:
		return s.File + ":" + strconv.Itoa(s.Line)
	}
	return s.File
}

// BindingSource is where the key of action in the config table section is
// set. action is a registry name, user:NAME for an [[actions]] entry, or
// command:NAME for a [[keybindings.command]] entry. It is the defaults when
// the config files are not known or none sets it.
func (c *UserConfig) BindingSource(section, action string) KeySource {
	if c == nil || c.layers == nil {
		return KeySource{}
	}
	lc := c.layers
	header, entry, field := "[keybindings."+section+"]", "", action
	path := []string{"keybindings", section, action}
	switch {
	case section == "keybindings":
		header, path = "[keybindings]", []string{"keybindings", action}
	case strings.HasPrefix(action, UserActionPrefix):
		header, entry, path = "[[actions]]", strings.TrimPrefix(action, UserActionPrefix), []string{"actions"}
	case strings.HasPrefix(action, CommandActionPrefix):
		header, entry, path = "[[keybindings.command]]", strings.TrimPrefix(action, CommandActionPrefix), []string{"keybindings", "command"}
	}
	holders := lc.Holders(path)
	for i := len(holders) - 1; i >= 0; i-- {
		if line := findKeyLine(holders[i].Data, header, entry, field); line > 0 {
			return KeySource{File: lc.DisplayPath(holders[i].Path), Line: line}
		}
	}
	if len(holders) > 0 {
		return KeySource{File: lc.DisplayPath(holders[len(holders)-1].Path)}
	}
	return KeySource{}
}

func findKeyLine(data []byte, header, entry, field string) int {
	in, entryAt, keyAt, named := false, 0, 0, false
	done := func() int {
		if in && entry != "" && named {
			return max(keyAt, entryAt)
		}
		return 0
	}
	for i, raw := range bytes.Split(data, []byte("\n")) {
		line := strings.TrimSpace(string(raw))
		if strings.HasPrefix(line, "[") {
			if n := done(); n > 0 {
				return n
			}
			h := strings.ReplaceAll(strings.SplitN(line, "#", 2)[0], " ", "")
			in, entryAt, keyAt, named = h == header, i+1, 0, false
			continue
		}
		if !in {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.Trim(strings.TrimSpace(k), "\"'")
		switch {
		case entry == "" && k == field:
			return i + 1
		case entry != "" && k == "key":
			keyAt = i + 1
		case entry != "" && k == "name" && strings.Trim(strings.TrimSpace(v), "\"'") == entry:
			named = true
		}
	}
	return done()
}
