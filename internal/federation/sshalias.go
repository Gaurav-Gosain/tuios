package federation

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxSSHAliases = 200

// UserSSHConfigPath is the ssh client configuration file of the user running
// this process. It returns an empty string when there is no home directory.
func UserSSHConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".ssh", "config")
}

// ReadSSHAliases returns the Host aliases in the ssh config file at path. A
// file that is missing or unreadable yields no aliases and no error: discovery
// is a convenience, and a user with no ssh config still types an address.
func ReadSSHAliases(path string) []string {
	if path == "" {
		return nil
	}
	f, err := os.Open(path) //nolint:gosec // the path is the caller's own ssh config
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	return SSHConfigAliases(f)
}

// SSHConfigAliases reads Host aliases out of an ssh config.
//
// Only exact names are returned. A pattern (one holding *, ? or !) is dropped,
// because it is a rule about several machines rather than the name of one, and
// handing "*" to ssh as an address reaches nothing.
//
// Include directives are not followed. The file the user edits is the file that
// is read, and a directive that names other files is left alone rather than
// walked.
func SSHConfigAliases(r io.Reader) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 16)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		fields := sshConfigFields(sc.Text())
		if len(fields) < 2 || !strings.EqualFold(fields[0], "host") {
			continue
		}
		for _, name := range fields[1:] {
			if !usableSSHAlias(name) {
				continue
			}
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
			if len(out) >= maxSSHAliases {
				sort.Strings(out)
				return out
			}
		}
	}
	_ = sc.Err()
	sort.Strings(out)
	return out
}

func sshConfigFields(line string) []string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if i := strings.IndexByte(line, '='); i >= 0 && !strings.ContainsAny(line[:i], " \t") {
		line = line[:i] + " " + line[i+1:]
	}
	fields := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == '\t' })
	for i, f := range fields {
		fields[i] = strings.Trim(f, `"`)
	}
	return fields
}

func usableSSHAlias(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	if strings.ContainsAny(name, "*?!") {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// ValidHostName reports whether name may be used as a host name, and says what
// is wrong when it may not. The CLI and the settings page both ask before they
// write, so a name the daemon would drop is refused where the user typed it.
func ValidHostName(name string) error {
	_, problems := NewTable([]Host{{Name: name, Addr: "placeholder"}})
	if len(problems) > 0 {
		return problems[0]
	}
	return nil
}
