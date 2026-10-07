package federation

import (
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
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

// sshSafeArgFlags are the ssh flags that take a value and are accepted. -o,
// -J, -l and -p have their values checked further. Flags that make ssh create
// a file at a path they name (-S, -L, -R, -D with a socket, -E) are not here.
const sshSafeArgFlags = "bBceiJlmop"

// sshSafeOptions are the -o keywords, lower case, that are accepted. None of
// them runs a command or loads code on this machine.
var sshSafeOptions = map[string]bool{
	"addressfamily": true, "batchmode": true, "bindaddress": true, "bindinterface": true,
	"canonicaldomains": true, "canonicalizefallbacklocal": true, "canonicalizehostname": true,
	"canonicalizemaxdots": true, "casignaturealgorithms": true, "certificatefile": true,
	"checkhostip": true, "ciphers": true, "clearallforwardings": true, "compression": true,
	"connectionattempts": true, "connecttimeout": true, "escapechar": true, "exitonforwardfailure": true, "fingerprinthash": true,
	"forwardagent": true, "forwardx11": true, "forwardx11timeout": true, "forwardx11trusted": true,
	"gssapiauthentication": true, "gssapidelegatecredentials": true,
	"hashknownhosts": true, "hostbasedacceptedalgorithms": true, "hostbasedauthentication": true,
	"hostkeyalgorithms": true, "hostkeyalias": true, "hostname": true, "identitiesonly": true,
	"identityagent": true, "identityfile": true, "ipqos": true, "kbdinteractiveauthentication": true,
	"kexalgorithms": true, "loglevel": true, "macs": true, "nohostauthenticationforlocalhost": true,
	"numberofpasswordprompts": true, "passwordauthentication": true, "port": true,
	"preferredauthentications": true, "proxyjump": true, "pubkeyacceptedalgorithms": true,
	"pubkeyacceptedkeytypes": true, "pubkeyauthentication": true, "rekeylimit": true,
	"requesttty": true, "sendenv": true, "serveralivecountmax": true, "serveraliveinterval": true,
	"setenv": true, "stricthostkeychecking": true, "tcpkeepalive": true, "updatehostkeys": true,
	"user": true, "verifyhostkeydns": true, "visualhostkey": true,
}

// plainSSHOption is an -o value written plainly: a keyword of letters and
// digits, then = or one space, then the value. The value is checked on its
// own (checkSSHOptionValue).
var plainSSHOption = lazyre.New(`^([A-Za-z0-9]+)(?:=| )(.*)$`)

// plainSSHValue is a value as one plain token: letters, digits and the
// punctuation paths, algorithm lists and addresses need. No space, quote,
// backslash, control character or shell character such as ; { } $ ` |, so
// a value ssh writes into a file (a host key alias in known_hosts, say) is
// never a line a shell would run.
var plainSSHValue = lazyre.New(`^[A-Za-z0-9_.,:@+=/~%\[\]-]+$`)

// sshNameValue is a host name, host key alias or user: letters, digits, dot,
// dash, underscore, @, colon and brackets for IPv6, not starting with a dash.
var sshNameValue = lazyre.New(`^[A-Za-z0-9_.@:\[\]][A-Za-z0-9_.@:\[\]-]*$`)

// sshNameOptions are the -o keywords whose value is a name (sshNameValue).
var sshNameOptions = map[string]bool{"hostkeyalias": true, "hostname": true, "user": true}

// sshPortValue is a port number.
var sshPortValue = lazyre.New(`^[0-9]{1,5}$`)

// jumpHopPattern is one ProxyJump hop: [user@]host[:port], with no part
// starting with a dash. ssh runs a hop as another ssh, so a host part such as
// -Fc would be read as an option there.
var jumpHopPattern = lazyre.New(`^(?:[A-Za-z0-9_.][A-Za-z0-9_.-]*@)?(?:[A-Za-z0-9_.][A-Za-z0-9_.-]*|\[[0-9A-Fa-f:.]+\])(?::[0-9]{1,5})?$`)

// validJump reports whether a ProxyJump value is only hosts, comma separated.
func validJump(value string) bool {
	if value == "" {
		return false
	}
	for hop := range strings.SplitSeq(value, ",") {
		if !jumpHopPattern().MatchString(hop) {
			return false
		}
	}
	return true
}

// sshYesNoOptions take only yes or no. ForwardAgent also takes a socket path,
// which would relay any local unix socket to the far side.
var sshYesNoOptions = map[string]bool{"forwardagent": true}

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
		if !validJump(value) {
			return fmt.Errorf("ssh_options: -J %q is not a list of hosts", value)
		}
	case 'o':
		m := plainSSHOption().FindStringSubmatch(value)
		if m == nil {
			return fmt.Errorf("ssh_options: -o %q is not written as Keyword=value with plain text", value)
		}
		key := strings.ToLower(m[1])
		if !sshSafeOptions[key] {
			return fmt.Errorf("ssh_options: %s is refused, because it can run code or write files here. Put it in ~/.ssh/config", m[1])
		}
		return checkSSHOptionValue(m[1], key, m[2])
	case 'l':
		if !sshNameValue().MatchString(value) {
			return fmt.Errorf("ssh_options: -l %q is not a user name", value)
		}
	case 'p':
		if !sshPortValue().MatchString(value) {
			return fmt.Errorf("ssh_options: -p %q is not a port", value)
		}
	default:
		if !plainSSHValue().MatchString(value) {
			return fmt.Errorf("ssh_options: -%c %q is not one plain value", flag, value)
		}
	}
	return nil
}

// checkSSHOptionValue checks the value of an accepted -o keyword.
func checkSSHOptionValue(name, key, value string) error {
	switch {
	case key == "proxyjump":
		if !validJump(value) {
			return fmt.Errorf("ssh_options: ProxyJump %q is not a list of hosts", value)
		}
	case sshYesNoOptions[key]:
		if v := strings.ToLower(value); v != "yes" && v != "no" {
			return fmt.Errorf("ssh_options: %s takes yes or no", name)
		}
	case key == "port":
		if !sshPortValue().MatchString(value) {
			return fmt.Errorf("ssh_options: Port %q is not a port", value)
		}
	case sshNameOptions[key]:
		if !sshNameValue().MatchString(value) {
			return fmt.Errorf("ssh_options: %s %q is not a plain name: use letters, digits, dot, dash, underscore, @ and colon", name, value)
		}
	default:
		if !plainSSHValue().MatchString(value) {
			return fmt.Errorf("ssh_options: %s %q is not one plain value", name, value)
		}
	}
	return nil
}
