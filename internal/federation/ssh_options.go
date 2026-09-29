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
// with -F that holds any of these. ssh reads an -o value its own way: it
// unquotes the keyword, and splits it from the value at =, spaces, tabs, CR
// and LF. A list of options to refuse would have to follow every spelling
// that reading allows, so CheckSSHOptions takes the other way: it accepts only
// flags and -o keywords known to be safe, written plainly. What
// ~/.ssh/config says is ssh's own and is not checked here; a process ssh
// starts that way is still held by pane grants (see session/pane_grants.go).

// sshSafeFlags are the ssh flags that take no value and are accepted.
const sshSafeFlags = "46AaCgKkNnqsTtvXxY"

// sshSafeArgFlags are the ssh flags that take a value and are accepted. -o
// and -J have their values checked further.
const sshSafeArgFlags = "bBcDeiJlLmopRS"

// sshSafeOptions are the -o keywords, lower case, that are accepted. None of
// them runs a command or loads code on this machine.
var sshSafeOptions = map[string]bool{
	"addressfamily": true, "batchmode": true, "bindaddress": true, "bindinterface": true,
	"canonicaldomains": true, "canonicalizefallbacklocal": true, "canonicalizehostname": true,
	"canonicalizemaxdots": true, "casignaturealgorithms": true, "certificatefile": true,
	"checkhostip": true, "ciphers": true, "clearallforwardings": true, "compression": true,
	"connectionattempts": true, "connecttimeout": true, "controlmaster": true, "controlpath": true,
	"controlpersist": true, "escapechar": true, "exitonforwardfailure": true, "fingerprinthash": true,
	"forwardagent": true, "forwardx11": true, "forwardx11timeout": true, "forwardx11trusted": true,
	"globalknownhostsfile": true, "gssapiauthentication": true, "gssapidelegatecredentials": true,
	"hashknownhosts": true, "hostbasedacceptedalgorithms": true, "hostbasedauthentication": true,
	"hostkeyalgorithms": true, "hostkeyalias": true, "hostname": true, "identitiesonly": true,
	"identityagent": true, "identityfile": true, "ipqos": true, "kbdinteractiveauthentication": true,
	"kexalgorithms": true, "loglevel": true, "macs": true, "nohostauthenticationforlocalhost": true,
	"numberofpasswordprompts": true, "passwordauthentication": true, "port": true,
	"preferredauthentications": true, "proxyjump": true, "pubkeyacceptedalgorithms": true,
	"pubkeyacceptedkeytypes": true, "pubkeyauthentication": true, "rekeylimit": true,
	"requesttty": true, "sendenv": true, "serveralivecountmax": true, "serveraliveinterval": true,
	"setenv": true, "stricthostkeychecking": true, "tcpkeepalive": true, "updatehostkeys": true,
	"user": true, "userknownhostsfile": true, "verifyhostkeydns": true, "visualhostkey": true,
}

// plainSSHOption is an -o value written plainly: a keyword of letters and
// digits, then = or spaces, then a value with no quote, backslash or control
// character. ssh reads such a value one way only.
var plainSSHOption = regexp.MustCompile(`^([A-Za-z0-9]+)(?:=| +)([^\x00-\x1f\x7f"'\\]*)$`)

// plainSSHValue is a flag value with no quote, backslash or control character.
var plainSSHValue = regexp.MustCompile(`^[^\x00-\x1f\x7f"'\\]*$`)

// jumpPattern is a ProxyJump value that is only hosts: [user@]host[:port],
// comma separated. It keeps a value ssh could read as an option out.
var jumpPattern = regexp.MustCompile(`^[A-Za-z0-9_.\[\]][A-Za-z0-9_.@:,\[\]-]*$`)

// CheckSSHOptions reports the first entry in opts that is not a safe ssh
// option written plainly.
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
			if strings.IndexByte(sshSafeFlags, flag) >= 0 {
				continue
			}
			if strings.IndexByte(sshSafeArgFlags, flag) < 0 {
				return fmt.Errorf("ssh_options: -%c is not accepted here. Put it in ~/.ssh/config", flag)
			}
			value := arg[j+1:]
			if value == "" {
				if i+1 >= len(opts) {
					return fmt.Errorf("ssh_options: -%c has no value", flag)
				}
				i++
				value = opts[i]
			}
			if err := checkSSHFlagValue(flag, value); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

// checkSSHFlagValue checks the value of one flag.
func checkSSHFlagValue(flag byte, value string) error {
	switch flag {
	case 'J':
		if !jumpPattern.MatchString(value) {
			return fmt.Errorf("ssh_options: -J %q is not a list of hosts", value)
		}
	case 'o':
		m := plainSSHOption.FindStringSubmatch(value)
		if m == nil {
			return fmt.Errorf("ssh_options: -o %q is not written as Keyword=value with plain text", value)
		}
		key := strings.ToLower(m[1])
		if !sshSafeOptions[key] {
			return fmt.Errorf("ssh_options: %s is not accepted here, because tuios cannot tell that it runs nothing on this machine. Put it in ~/.ssh/config", m[1])
		}
		if key == "proxyjump" && !jumpPattern.MatchString(strings.TrimSpace(m[2])) {
			return fmt.Errorf("ssh_options: ProxyJump %q is not a list of hosts", m[2])
		}
	default:
		if !plainSSHValue.MatchString(value) {
			return fmt.Errorf("ssh_options: -%c %q has a quote, backslash or control character", flag, value)
		}
	}
	return nil
}
