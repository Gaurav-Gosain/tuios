package integration

import (
	"errors"
	"regexp"
	"strings"
)

// yamlListItem is one item tuios adds to a list two keys deep in a YAML file:
// Hermes Agent's plugins.enabled in config.yaml, which is how Hermes is told
// to load a plugin. It edits lines rather than parsing the document, so the
// user's comments, order and quoting are kept. It handles the block layouts
// the list is written in, and an empty flow list; any other layout it refuses,
// with the line to add by hand, rather than rewrite what it does not follow.
type yamlListItem struct {
	key, sub, item string
}

var (
	yamlTopKeyRe = regexp.MustCompile(`^([A-Za-z0-9_-]+):[ \t]*(.*)$`)
	yamlItemRe   = regexp.MustCompile(`^([ \t]*)-[ \t]+(.*)$`)
)

// yamlValue strips a trailing comment and quotes from a scalar.
func yamlValue(s string) string {
	if i := strings.Index(s, " #"); i >= 0 {
		s = s[:i]
	}
	if strings.HasPrefix(s, "#") {
		return ""
	}
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}
	return s
}

func yamlIndent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

func yamlBlank(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

// yamlListPlace is where a list sits in a file's lines. See locate.
type yamlListPlace struct {
	key, end, sub, childIndent int
	items                      []int
	flowEmpty                  bool
}

// locate finds the list: the index of the top-level key's line (-1 when
// absent), the end of its block, the index of the sub key's line (-1 when
// absent), the indent its children use, and the item lines of the list.
func (f yamlListItem) locate(lines []string) (yamlListPlace, error) {
	p := yamlListPlace{key: -1, sub: -1, childIndent: 2}
	for i, line := range lines {
		m := yamlTopKeyRe.FindStringSubmatch(line)
		if m == nil || m[1] != f.key {
			continue
		}
		if v := yamlValue(m[2]); v != "" {
			return p, errors.New("its " + f.key + " key is written inline")
		}
		p.key = i
		break
	}
	if p.key < 0 {
		return p, nil
	}
	p.end = len(lines)
	for i := p.key + 1; i < len(lines); i++ {
		if !yamlBlank(lines[i]) && yamlIndent(lines[i]) == 0 && !strings.HasPrefix(strings.TrimSpace(lines[i]), "-") {
			p.end = i
			break
		}
	}
	for i := p.key + 1; i < p.end; i++ {
		if !yamlBlank(lines[i]) {
			p.childIndent = yamlIndent(lines[i])
			break
		}
	}
	subRe := regexp.MustCompile(`^[ \t]+` + regexp.QuoteMeta(f.sub) + `:[ \t]*(.*)$`)
	for i := p.key + 1; i < p.end; i++ {
		m := subRe.FindStringSubmatch(lines[i])
		if m == nil || yamlIndent(lines[i]) != p.childIndent {
			continue
		}
		p.sub = i
		switch v := yamlValue(m[1]); v {
		case "":
		case "[]":
			p.flowEmpty = true
		default:
			return p, errors.New("its " + f.key + "." + f.sub + " list is written inline")
		}
		break
	}
	if p.sub < 0 {
		return p, nil
	}
	for i := p.sub + 1; i < p.end; i++ {
		line := lines[i]
		if yamlBlank(line) {
			continue
		}
		ind := yamlIndent(line)
		isItem := yamlItemRe.MatchString(line)
		if ind < p.childIndent || (ind == p.childIndent && !isItem) {
			break
		}
		if isItem {
			p.items = append(p.items, i)
		}
	}
	return p, nil
}

func (f yamlListItem) has(lines []string, p yamlListPlace) int {
	for _, i := range p.items {
		m := yamlItemRe.FindStringSubmatch(lines[i])
		if m != nil && yamlValue(m[2]) == f.item {
			return i
		}
	}
	return -1
}

func (f yamlListItem) apply(_ *Target, have []byte, _ string, install bool) ([]byte, bool, bool, error) {
	if !install && have == nil {
		return nil, false, false, nil
	}
	text := string(have)
	trailing := text == "" || strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	p, err := f.locate(lines)
	if err != nil {
		if !install {
			return nil, false, false, nil
		}
		return nil, false, false, errors.New(err.Error() + ". Add " + f.item + " to " + f.key + "." + f.sub + " by hand")
	}
	at := f.has(lines, p)
	switch {
	case install && at >= 0, !install && at < 0:
		return have, false, false, nil
	case install && p.key < 0:
		lines = append(lines, f.key+":", "  "+f.sub+":", "    - "+f.item)
	case install && p.sub < 0:
		pad := strings.Repeat(" ", p.childIndent)
		lines = insertLines(lines, p.key+1, pad+f.sub+":", pad+"  - "+f.item)
	case install:
		pad := strings.Repeat(" ", p.childIndent)
		if p.flowEmpty {
			lines[p.sub] = pad + f.sub + ":"
		}
		itemPad := pad + "  "
		after := p.sub
		if len(p.items) > 0 {
			itemPad = strings.Repeat(" ", yamlIndent(lines[p.items[0]]))
			after = p.items[len(p.items)-1]
		}
		lines = insertLines(lines, after+1, itemPad+"- "+f.item)
	default:
		lines = append(lines[:at], lines[at+1:]...)
		if len(p.items) == 1 {
			lines[p.sub] = strings.Repeat(" ", p.childIndent) + f.sub + ": []"
		}
	}
	out := strings.Join(lines, "\n")
	if trailing {
		out += "\n"
	}
	return []byte(out), out != text, false, nil
}

func insertLines(lines []string, at int, add ...string) []string {
	out := make([]string, 0, len(lines)+len(add))
	out = append(out, lines[:at]...)
	out = append(out, add...)
	return append(out, lines[at:]...)
}

func (f yamlListItem) state(_ *Target, have []byte, _ string) (bool, bool, int, error) {
	if have == nil {
		return false, false, 0, nil
	}
	lines := strings.Split(string(have), "\n")
	p, err := f.locate(lines)
	if err != nil {
		return false, false, 0, nil
	}
	on := f.has(lines, p) >= 0
	return on, on, 0, nil
}

func (yamlListItem) owned() bool { return false }
