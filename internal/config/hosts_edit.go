package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// Editing the [hosts] table in the file, one table at a time.
//
// The rest of the config is saved by marshalling the whole UserConfig back out
// (see save.go), which is right for the settings page: every value it can write
// is a value it already holds. It is wrong for `tuios hosts add`, because a
// command that adds one machine must not rewrite the file the user hand-wrote
// around it. So this works on the bytes: it finds the [hosts.NAME] table, and
// replaces, appends or deletes exactly those lines. Every comment, every blank
// line and every other table is left where it was.
//
// The scan is a table-header scan, not a TOML parse. That is enough because the
// only question asked of the file is where one table starts and where the next
// one starts, and it is what lets an unparseable line elsewhere in the file
// survive the edit rather than being rewritten into something else.

// SetHostInFile writes the [hosts.NAME] table at path, replacing the table that
// is there or appending one at the end. It creates the file and its directory
// when neither exists.
func SetHostInFile(path, name string, h HostConfig) error {
	if err := federation.ValidHostName(name); err != nil {
		return err
	}
	if strings.TrimSpace(h.Addr) == "" {
		return fmt.Errorf("host %q needs an address. Give the name ssh uses, for example user@machine", name)
	}
	h.Addr = strings.TrimSpace(h.Addr)

	data, err := readConfigForEdit(path)
	if err != nil {
		return err
	}
	block := renderHostBlock(name, h)
	lines := splitLines(string(data))
	start, end, found := findTableBlock(lines, []string{"hosts", name})

	var out []string
	switch {
	case found:
		out = append(out, lines[:start]...)
		out = append(out, splitLines(block)...)
		out = append(out, lines[end:]...)
	default:
		out = append(out, trimTrailingBlank(lines)...)
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, splitLines(block)...)
	}
	return writeConfigBytes([]byte(joinLines(out)), path)
}

// RemoveHostFromFile deletes the [hosts.NAME] table at path. It reports whether
// a table was there to delete, so the caller can say "no host is named that"
// rather than reporting a success that removed nothing.
func RemoveHostFromFile(path, name string) (bool, error) {
	data, err := readConfigForEdit(path)
	if err != nil {
		return false, err
	}
	lines := splitLines(string(data))
	start, end, found := findTableBlock(lines, []string{"hosts", name})
	if !found {
		return false, nil
	}
	out := append([]string{}, lines[:start]...)
	out = append(out, lines[end:]...)
	return true, writeConfigBytes([]byte(joinLines(trimTrailingBlank(out))), path)
}

// HostsInFile reads the [hosts] table at path. It is the set a command edits,
// read from the file rather than from a running daemon, so `tuios hosts add`
// works with no daemon running.
func HostsInFile(path string) (map[string]HostConfig, error) {
	data, err := readConfigForEdit(path)
	if err != nil {
		return nil, err
	}
	cfg, err := ParseUserConfig(data)
	if err != nil {
		return nil, err
	}
	if cfg.Hosts == nil {
		return map[string]HostConfig{}, nil
	}
	return cfg.Hosts, nil
}

// readConfigForEdit reads the config file. A file that is not there yet is an
// empty one: adding the first host to a machine that has never saved a setting
// must work.
func readConfigForEdit(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("no config file path is set")
	}
	data, err := os.ReadFile(path) //nolint:gosec // the path is the user's own config file
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	return data, nil
}

// findTableBlock locates the lines of one TOML table. The block starts at its
// header line and ends at the next header, so the values under it and any
// comment written between them move with it.
//
// A comment sitting directly above the header belongs to it as far as a reader
// is concerned, but it is left out on purpose: taking it would mean guessing
// which of the comment lines above a table are about that table, and a delete
// that removes a line the user did not expect is worse than one that leaves a
// stale comment they can see.
func findTableBlock(lines []string, want []string) (start, end int, found bool) {
	for i, line := range lines {
		path, ok := tableHeader(line)
		if !ok {
			continue
		}
		if found {
			// The next header of any kind ends the block.
			return start, i, true
		}
		if samePath(path, want) {
			start, found = i, true
		}
	}
	if found {
		return start, len(lines), true
	}
	return 0, 0, false
}

// tableHeader parses a line that is a TOML table header, returning its dotted
// path. An array-of-tables header ("[[x]]") is reported as a header too, so a
// block scan stops at one, but its path never matches a host.
func tableHeader(line string) ([]string, bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "[") {
		return nil, false
	}
	s = strings.TrimPrefix(s, "[")
	array := strings.HasPrefix(s, "[")
	if array {
		s = strings.TrimPrefix(s, "[")
	}
	inside, rest, ok := cutOutsideQuotes(s, ']')
	if !ok {
		return nil, false
	}
	if array {
		if !strings.HasPrefix(rest, "]") {
			return nil, false
		}
		rest = strings.TrimPrefix(rest, "]")
	}
	rest = strings.TrimSpace(rest)
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return nil, false
	}
	if array {
		// A real header, but never one of ours: [hosts.NAME] is a table.
		return []string{"\x00array"}, true
	}
	return splitDotted(inside), true
}

// cutOutsideQuotes splits s at the first sep that is not inside a quoted key.
func cutOutsideQuotes(s string, sep byte) (before, after string, ok bool) {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == sep:
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// splitDotted breaks a table path into its key segments, respecting quotes, and
// unquotes each one.
func splitDotted(s string) []string {
	var out []string
	for {
		before, after, ok := cutOutsideQuotes(s, '.')
		out = append(out, unquoteKey(before))
		if !ok {
			return out
		}
		s = after
	}
}

// unquoteKey strips the quotes from a TOML key. Only the escapes a host name
// could carry are undone; a key this does not fully understand simply fails to
// match, which leaves the table alone.
func unquoteKey(s string) string {
	s = strings.TrimSpace(s)
	switch {
	case len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'':
		return s[1 : len(s)-1]
	case len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"':
		if v, err := strconv.Unquote(s); err == nil {
			return v
		}
		return s[1 : len(s)-1]
	}
	return s
}

func samePath(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// renderHostBlock is one [hosts.NAME] table as text. Only the fields that were
// set are written, so a host added with nothing but an address gets a two-line
// table rather than a table full of zeroes.
func renderHostBlock(name string, h HostConfig) string {
	var b strings.Builder
	b.WriteString("[hosts." + tomlKey(name) + "]\n")
	b.WriteString("addr = " + tomlString(h.Addr) + "\n")
	if h.Command != "" {
		b.WriteString("command = " + tomlString(h.Command) + "\n")
	}
	if h.ConnectTimeout > 0 {
		b.WriteString("connect_timeout = " + strconv.Itoa(h.ConnectTimeout) + "\n")
	}
	if len(h.SSHOptions) > 0 {
		parts := make([]string, 0, len(h.SSHOptions))
		for _, o := range h.SSHOptions {
			parts = append(parts, tomlString(o))
		}
		b.WriteString("ssh_options = [" + strings.Join(parts, ", ") + "]\n")
	}
	return b.String()
}

// tomlKey writes a table key, quoting it when it is not a bare key. A host name
// may hold a dot, and an unquoted dot would make two tables out of one name.
func tomlKey(name string) string {
	bare := name != ""
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			bare = false
			break
		}
	}
	if bare {
		return name
	}
	return tomlString(name)
}

// tomlString writes a TOML basic string. The escapes are TOML's own, which are
// not Go's: a byte outside the printable range is written as \uXXXX, the only
// short escape TOML has for one.
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
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f || r == utf8.RuneError {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// splitLines breaks text into lines, dropping the final empty piece a trailing
// newline leaves behind so joinLines can put exactly one back.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// joinLines is splitLines undone: every line, then one trailing newline.
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// trimTrailingBlank drops blank lines from the end, so repeated adds do not
// grow a run of empty lines at the bottom of the file.
func trimTrailingBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
