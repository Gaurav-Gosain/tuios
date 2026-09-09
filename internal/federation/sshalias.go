package federation

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Address discovery: the names a user could reasonably type as a host's addr.
//
// An addr is anything ssh understands, and the thing most people already have
// is an ssh_config alias. Reading ~/.ssh/config for its Host names turns "what
// do I type here" into a list to pick from, without tuios guessing anything.
//
// Three rules keep this honest, and they are the whole design.
//
// Only the Host keyword is read. known_hosts is never opened, no key file is
// ever opened, and no value other than an alias is taken from the file: an
// alias is a label the user chose, and it is the one thing in the ssh
// configuration that is safe to show back to them.
//
// Nothing is added automatically. The list is offered, and a host exists only
// because the user named it. That is section 3 of the federation design, which
// refuses discovery as a way of finding machines; this is discovery of what to
// type, not of what to connect to.
//
// An alias found here is never written anywhere but the user's own config file,
// and only by a command the user ran.

// maxSSHAliases bounds the list. A generated ssh config can hold thousands of
// hosts, and a completion list that long helps nobody.
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
	// A long generated line must not stop the scan halfway through the file.
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
	sort.Strings(out)
	return out
}

// sshConfigFields splits one ssh config line into its keyword and values. ssh
// accepts "Host name", "Host=name" and quoted values, so all three are handled
// here rather than only the common one.
func sshConfigFields(line string) []string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	// The separator between the keyword and its first value may be an equals
	// sign, with or without spaces around it.
	if i := strings.IndexByte(line, '='); i >= 0 && !strings.ContainsAny(line[:i], " \t") {
		line = line[:i] + " " + line[i+1:]
	}
	fields := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == '\t' })
	for i, f := range fields {
		fields[i] = strings.Trim(f, `"`)
	}
	return fields
}

// usableSSHAlias reports whether a Host value names one machine a user could
// type as an addr.
func usableSSHAlias(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	if strings.ContainsAny(name, "*?!") {
		return false
	}
	// A control character in a name would end up in a listing this program
	// prints, so it is dropped here rather than escaped everywhere else.
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
