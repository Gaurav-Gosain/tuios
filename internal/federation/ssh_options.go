package federation

import (
	"fmt"
	"regexp"
	"strings"
)

// The ssh options of a host are read from config.toml, which any process of
// the user can write, a process in a pane included. ssh runs some options as
// commands on this machine, as the daemon's child: ProxyCommand, LocalCommand,
// KnownHostsCommand, a PKCS#11 or security key library, or a config file named
// with -F that holds any of these. CheckSSHOptions refuses those, so a host
// entry cannot make the daemon run a command. What ~/.ssh/config says is
// ssh's own and is not checked here; a process ssh starts that way is still
// held by pane grants (see session/pane_grants.go).

// sshCommandOptions are the -o keywords, lower case, that run a command or
// load code on this machine, or read more options from a file.
var sshCommandOptions = map[string]bool{
	"proxycommand":        true,
	"localcommand":        true,
	"permitlocalcommand":  true,
	"knownhostscommand":   true,
	"pkcs11provider":      true,
	"securitykeyprovider": true,
	"include":             true,
}

// sshArgFlags are the ssh flags that take a value, from ssh(1).
const sshArgFlags = "BbcDEeFIiJLlmOoPpQRSWw"

// sshRefusedFlags run a command or load code: -F reads another config file,
// -I loads a PKCS#11 library.
const sshRefusedFlags = "FI"

// jumpPattern is a ProxyJump value that is only hosts: [user@]host[:port],
// comma separated. It keeps a value ssh could read as an option out.
var jumpPattern = regexp.MustCompile(`^[A-Za-z0-9_.\[\]][A-Za-z0-9_.@:,\[\]-]*$`)

// CheckSSHOptions reports the first ssh option in opts that could run a
// command on this machine, or an argument that is not an option.
func CheckSSHOptions(opts []string) error {
	for i := 0; i < len(opts); i++ {
		arg := opts[i]
		if len(arg) < 2 || arg[0] != '-' || arg == "--" {
			return fmt.Errorf("ssh_options: %q is not an ssh option", arg)
		}
		// Flags can be combined, as in -vo Key=value. A flag that takes a
		// value takes the rest of the argument, or the next argument.
		for j := 1; j < len(arg); j++ {
			flag := arg[j]
			if !strings.ContainsRune(sshArgFlags, rune(flag)) {
				continue
			}
			value := arg[j+1:]
			if value == "" {
				if i+1 >= len(opts) {
					return fmt.Errorf("ssh_options: -%c has no value", flag)
				}
				i++
				value = opts[i]
			}
			if err := checkSSHFlag(flag, value); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

// checkSSHFlag checks one flag and its value.
func checkSSHFlag(flag byte, value string) error {
	if strings.ContainsRune(sshRefusedFlags, rune(flag)) {
		return fmt.Errorf("ssh_options: -%c is refused, because it can make ssh run code on this machine. Put it in ~/.ssh/config", flag)
	}
	switch flag {
	case 'J':
		if !jumpPattern.MatchString(value) {
			return fmt.Errorf("ssh_options: -J %q is not a list of hosts", value)
		}
	case 'o':
		key, rest := splitSSHOption(value)
		if sshCommandOptions[key] {
			return fmt.Errorf("ssh_options: %s is refused, because it makes ssh run a command on this machine. Put it in ~/.ssh/config", key)
		}
		if key == "proxyjump" && !jumpPattern.MatchString(rest) {
			return fmt.Errorf("ssh_options: ProxyJump %q is not a list of hosts", rest)
		}
	}
	return nil
}

// splitSSHOption splits an -o value, Key=value or Key value, and lower-cases
// the key as ssh reads it.
func splitSSHOption(s string) (key, value string) {
	s = strings.TrimSpace(s)
	end := strings.IndexAny(s, "= \t")
	if end < 0 {
		return strings.ToLower(s), ""
	}
	key = strings.ToLower(strings.TrimSpace(s[:end]))
	value = strings.TrimLeft(s[end:], "= \t")
	return key, strings.TrimSpace(value)
}
