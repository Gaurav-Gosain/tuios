package federation

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
)

const sshSafeFlags = "46AaCgKkNnqsTtvXxY"

const sshSafeArgFlags = "bBceiJlmop"

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

var plainSSHOption = lazyre.New(`^([A-Za-z0-9]+)(?:=| )(.*)$`)

var plainSSHValue = lazyre.New(`^[A-Za-z0-9_.,:@+=/~%\[\]-]+$`)

var sshNameValue = lazyre.New(`^[A-Za-z0-9_.@:\[\]][A-Za-z0-9_.@:\[\]-]*$`)

var sshNameOptions = map[string]bool{"hostkeyalias": true, "hostname": true, "user": true}

var sshPortValue = lazyre.New(`^[0-9]{1,5}$`)

var jumpHopPattern = lazyre.New(`^(?:[A-Za-z0-9_.][A-Za-z0-9_.-]*@)?(?:[A-Za-z0-9_.][A-Za-z0-9_.-]*|\[[0-9A-Fa-f:.]+\])(?::[0-9]{1,5})?$`)

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

var sshYesNoOptions = map[string]bool{"forwardagent": true}

// CheckSSHOptions reports the first entry in opts that is not a safe ssh
// option written plainly.
func CheckSSHOptions(opts []string) error {
	for i := 0; i < len(opts); i++ {
		arg := opts[i]
		if len(arg) < 2 || arg[0] != '-' || arg == "--" {
			return errfmt.Errorf("ssh_options: %q is not an ssh option", arg)
		}
		for j := 1; j < len(arg); j++ {
			flag := arg[j]
			if strings.IndexByte(sshSafeFlags, flag) >= 0 {
				continue
			}
			if strings.IndexByte(sshSafeArgFlags, flag) < 0 {
				return errfmt.Errorf("ssh_options: -%c is not accepted here. Put it in ~/.ssh/config", flag)
			}
			value := arg[j+1:]
			if value == "" {
				if i+1 >= len(opts) {
					return errfmt.Errorf("ssh_options: -%c has no value", flag)
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

func checkSSHFlagValue(flag byte, value string) error {
	switch flag {
	case 'J':
		if !validJump(value) {
			return errfmt.Errorf("ssh_options: -J %q is not a list of hosts", value)
		}
	case 'o':
		m := plainSSHOption().FindStringSubmatch(value)
		if m == nil {
			return errfmt.Errorf("ssh_options: -o %q is not written as Keyword=value with plain text", value)
		}
		key := strings.ToLower(m[1])
		if !sshSafeOptions[key] {
			return errfmt.Errorf("ssh_options: %s is refused, because it can run code or write files here. Put it in ~/.ssh/config", m[1])
		}
		return checkSSHOptionValue(m[1], key, m[2])
	case 'l':
		if !sshNameValue().MatchString(value) {
			return errfmt.Errorf("ssh_options: -l %q is not a user name", value)
		}
	case 'p':
		if !sshPortValue().MatchString(value) {
			return errfmt.Errorf("ssh_options: -p %q is not a port", value)
		}
	default:
		if !plainSSHValue().MatchString(value) {
			return errfmt.Errorf("ssh_options: -%c %q is not one plain value", flag, value)
		}
	}
	return nil
}

func checkSSHOptionValue(name, key, value string) error {
	switch {
	case key == "proxyjump":
		if !validJump(value) {
			return errfmt.Errorf("ssh_options: ProxyJump %q is not a list of hosts", value)
		}
	case sshYesNoOptions[key]:
		if v := strings.ToLower(value); v != "yes" && v != "no" {
			return errfmt.Errorf("ssh_options: %s takes yes or no", name)
		}
	case key == "port":
		if !sshPortValue().MatchString(value) {
			return errfmt.Errorf("ssh_options: Port %q is not a port", value)
		}
	case sshNameOptions[key]:
		if !sshNameValue().MatchString(value) {
			return errfmt.Errorf("ssh_options: %s %q is not a plain name: use letters, digits, dot, dash, underscore, @ and colon", name, value)
		}
	default:
		if !plainSSHValue().MatchString(value) {
			return errfmt.Errorf("ssh_options: %s %q is not one plain value", name, value)
		}
	}
	return nil
}
