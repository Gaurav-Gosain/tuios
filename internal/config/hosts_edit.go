package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// SetHostInFile writes the [hosts.NAME] table of the config whose main file is
// at path. A config split over several files is written where the host is: the
// file that sets [hosts.NAME], or for a new host the file that holds the other
// hosts, or config.toml. A read-only file is not written: the table goes to
// config.toml, and the WriteNote says so. It creates the file and its
// directory when neither exists.
func SetHostInFile(path, name string, h HostConfig) (WriteNote, error) {
	if err := federation.ValidHostName(name); err != nil {
		return WriteNote{}, err
	}
	if strings.TrimSpace(h.Addr) == "" {
		return WriteNote{}, errfmt.Errorf("host %q needs an address. Give the name ssh uses, for example user@machine", name)
	}
	h.Addr = strings.TrimSpace(h.Addr)

	target, note, err := writeTargetFor(path, []string{"hosts", name})
	if err != nil {
		return note, err
	}
	return note, setHostInOneFile(target, name, h)
}

func writeTargetFor(path string, key []string) (string, WriteNote, error) {
	lc, err := loadLayered(path, true)
	if err != nil {
		return "", WriteNote{}, errfmt.Errorf("%s: %w", errSaveFailed, err)
	}
	return lc.WriteTarget(key)
}

func setHostInOneFile(path, name string, h HostConfig) error {
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

// RemoveHostFromFile deletes the [hosts.NAME] table from every file of the
// config whose main file is at path. It reports whether a table was there to
// delete, so the caller can say "no host is named that" rather than reporting
// a success that removed nothing. A host that a read-only file sets is not
// removed from any file, and the error names that file.
func RemoveHostFromFile(path, name string) (bool, error) {
	lc, err := LoadLayered(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errfmt.Errorf("the config files have an error, so tuios did not save: %w", err)
	}
	if !lc.Layered {
		return removeHostFromOneFile(path, name)
	}
	holders := lc.Holders([]string{"hosts", name})
	for _, h := range holders {
		if !h.Writable() {
			return false, errfmt.Errorf("host %q is set in %s, and tuios cannot write that file. Remove the host there", name, lc.DisplayPath(h.Path))
		}
	}
	removed := false
	for _, h := range holders {
		ok, err := removeHostFromOneFile(h.Path, name)
		if err != nil {
			return removed, err
		}
		removed = removed || ok
	}
	return removed, nil
}

func removeHostFromOneFile(path, name string) (bool, error) {
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
	data, err := readConfigMerged(path)
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

// TailscaleInFile is the [tailscale] table in the file at path, read without
// creating anything.
//
// It exists alongside HostsInFile rather than going through LoadUserConfig for
// the reason that one does: LoadUserConfig writes a default config file when
// there is none, and a reader asking what the table says must not create a
// file as a side effect of asking. A caller on a background goroutine makes
// that a race as well as a surprise.
func TailscaleInFile(path string) (TailscaleConfig, error) {
	data, err := readConfigMerged(path)
	if err != nil {
		return TailscaleConfig{}, err
	}
	cfg, err := ParseUserConfig(data)
	if err != nil {
		return TailscaleConfig{}, err
	}
	return cfg.Tailscale, nil
}

func readConfigForEdit(path string) ([]byte, error) {
	if path == "" {
		return nil, errfmt.Errorf("no config file path is set")
	}
	data, err := os.ReadFile(path) //nolint:gosec // the path is the user's own config file
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errfmt.Errorf("failed to read config file: %w", err)
	}
	return data, nil
}

func findTableBlock(lines []string, want []string) (start, end int, found bool) {
	for i, line := range lines {
		path, ok := tableHeader(line)
		if !ok {
			continue
		}
		if found {
			return start, i, true
		}
		if slices.Equal(path, want) {
			start, found = i, true
		}
	}
	if found {
		return start, len(lines), true
	}
	return 0, 0, false
}

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
		return []string{"\x00array"}, true
	}
	return splitDotted(inside), true
}

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

func renderHostBlock(name string, h HostConfig) string {
	var b strings.Builder
	b.WriteString("[hosts.")
	b.WriteString(tomlKey(name))
	b.WriteString("]\n")
	b.WriteString("addr = ")
	b.WriteString(tomlString(h.Addr))
	b.WriteString("\n")
	if h.Command != "" {
		b.WriteString("command = ")
		b.WriteString(tomlString(h.Command))
		b.WriteString("\n")
	}
	if h.ConnectTimeout > 0 {
		b.WriteString("connect_timeout = ")
		b.WriteString(strconv.Itoa(h.ConnectTimeout))
		b.WriteString("\n")
	}
	if len(h.SSHOptions) > 0 {
		parts := make([]string, 0, len(h.SSHOptions))
		for _, o := range h.SSHOptions {
			parts = append(parts, tomlString(o))
		}
		b.WriteString("ssh_options = [")
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString("]\n")
	}
	if h.ReposRoot != "" {
		b.WriteString("repos_root = ")
		b.WriteString(tomlString(h.ReposRoot))
		b.WriteString("\n")
	}
	if h.TailscaleLogin != "" {
		b.WriteString("tailscale_login = ")
		b.WriteString(tomlString(h.TailscaleLogin))
		b.WriteString("\n")
	}
	if h.Allow != nil {
		parts := make([]string, 0, len(h.Allow))
		for _, c := range h.Allow {
			parts = append(parts, tomlString(c))
		}
		b.WriteString("allow = [")
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString("]\n")
	}
	if h.HoldMail != nil {
		b.WriteString("hold_mail = ")
		b.WriteString(strconv.FormatBool(*h.HoldMail))
		b.WriteString("\n")
	}
	if h.HostedGrace != "" {
		b.WriteString("hosted_grace = ")
		b.WriteString(tomlString(h.HostedGrace))
		b.WriteString("\n")
	}
	return b.String()
}

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

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func trimTrailingBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// ErrHostTableExists is what AddLinkPolicyInFile returns when the config has a
// [hosts.NAME] table already. The caller leaves it alone and tells the person.
var ErrHostTableExists = errors.New("the config has this host table already")

// AddLinkPolicyInFile writes a new [hosts.NAME] table that holds only allow:
// what the machine of that name may do here when it links in. It never edits
// a table that is there: it returns ErrHostTableExists, matched without
// regard to case as the link policy matches names. tuios pair uses it for a
// device it has just let in.
func AddLinkPolicyInFile(path, name string, allow []string) (WriteNote, error) {
	if err := federation.ValidHostName(name); err != nil {
		return WriteNote{}, err
	}
	existing, err := HostsInFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return WriteNote{}, err
	}
	for key := range existing {
		if strings.EqualFold(key, name) {
			return WriteNote{}, ErrHostTableExists
		}
	}
	target, note, err := writeTargetFor(path, []string{"hosts", name})
	if err != nil {
		return note, err
	}
	data, err := readConfigForEdit(target)
	if err != nil {
		return note, err
	}
	lines := splitLines(string(data))
	if _, _, found := findTableBlock(lines, []string{"hosts", name}); found {
		return note, ErrHostTableExists
	}
	var b strings.Builder
	b.WriteString("[hosts.")
	b.WriteString(tomlKey(name))
	b.WriteString("]\n")
	parts := make([]string, 0, len(allow))
	for _, c := range allow {
		parts = append(parts, tomlString(c))
	}
	b.WriteString("allow = [")
	b.WriteString(strings.Join(parts, ", "))
	b.WriteString("]\n")
	out := trimTrailingBlank(lines)
	if len(out) > 0 {
		out = append(out, "")
	}
	out = append(out, splitLines(b.String())...)
	return note, writeConfigBytes([]byte(joinLines(out)), target)
}
