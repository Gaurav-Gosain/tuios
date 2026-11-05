package federation

import (
	"strings"
)

// ssh fails in a handful of ways that need different fixes, and it says which
// only in words on stderr. Before this file every one of them read "The host
// did not answer.", so a key the machine refused, a name that does not
// resolve and a machine that is switched off looked the same, and the person
// had to run ssh by hand to learn which.
//
// The words come from OpenSSH on Linux and macOS (portable 8.x to 10.x). They
// are matched in lower case, as substrings, so a prefix such as "ssh: " or the
// address in the line does not matter. The order matters: a changed host key
// also prints "Host key verification failed", which alone means an unknown
// key, so the changed key is tested first.
//
// Everything here reads text from ssh about another machine. It decides a
// class and nothing more: no part of the text is repeated in a fix.

// SSHFailure is one class of ssh failure.
type SSHFailure string

// The classes. Each one has one fix (see SSHFailure.Fix). They are also the
// values of HostReport.Failure.
const (
	// FailHostKeyNew is a machine whose host key ssh does not know. With
	// BatchMode on, ssh refuses it rather than asking.
	FailHostKeyNew SSHFailure = "host_key_new"
	// FailHostKeyChanged is a machine whose host key is not the one ssh
	// knows. It can be an attack, so tuios never accepts the new key.
	FailHostKeyChanged SSHFailure = "host_key_changed"
	// FailAuth is a login the machine refused: no key it accepts.
	FailAuth SSHFailure = "auth"
	// FailDNS is an address that does not resolve.
	FailDNS SSHFailure = "dns"
	// FailRefused is a machine that answered and closed the port: no ssh
	// server listens there, or it closed the connection before the login.
	FailRefused SSHFailure = "refused"
	// FailTimeout is a machine that did not answer at all: it is off, asleep,
	// or on a network this machine cannot reach.
	FailTimeout SSHFailure = "timeout"
)

// sshFailureWords maps the words ssh writes to a class, in the order they are
// tested.
var sshFailureWords = []struct {
	kind  SSHFailure
	words []string
}{
	{FailHostKeyChanged, []string{
		"remote host identification has changed",
		"has changed and you have requested strict checking",
	}},
	{FailHostKeyNew, []string{
		"host key verification failed",
		"you have requested strict checking",
		"the authenticity of host",
	}},
	{FailAuth, []string{
		"permission denied (",
		"too many authentication failures",
		"no supported authentication methods available",
	}},
	{FailDNS, []string{
		"could not resolve hostname",
		"name or service not known",
		"nodename nor servname provided",
		"temporary failure in name resolution",
		"no address associated with hostname",
	}},
	{FailRefused, []string{
		"connection refused",
		"connection reset by peer",
		"connection closed by remote host",
	}},
	{FailTimeout, []string{
		"connection timed out",
		"operation timed out",
		"no route to host",
		"network is unreachable",
		"host is down",
	}},
}

// ClassifySSHFailure reads what ssh wrote on stderr and returns the class of
// the failure, or "" when the words match none. A Tailscale gate is not a
// class here: ParseSSHGate reads those first.
func ClassifySSHFailure(stderr string) SSHFailure {
	if stderr == "" {
		return ""
	}
	low := strings.ToLower(stderr)
	for _, c := range sshFailureWords {
		for _, w := range c.words {
			if strings.Contains(low, w) {
				return c.kind
			}
		}
	}
	return ""
}

// Reason is what happened, in one sentence about "the host".
func (f SSHFailure) Reason() string {
	switch f {
	case FailHostKeyNew:
		return "ssh does not know the host key of the host."
	case FailHostKeyChanged:
		return "The host key of the host changed. This can be an attack."
	case FailAuth:
		return "The host refused your ssh key."
	case FailDNS:
		return "ssh cannot find the address of the host."
	case FailRefused:
		return "The host refused the connection. No ssh server answers on its port."
	case FailTimeout:
		return "The host does not answer. It can be off, asleep or on another network."
	}
	return ""
}

// Fix is what the person does about f, for the host reached at addr. It is
// one or two sentences, and FixCommand is the command in it, when there is
// one.
func (f SSHFailure) Fix(addr string) string {
	switch f {
	case FailHostKeyNew:
		return "Connect one time with this command and check the key that ssh shows: " + f.FixCommand(addr, nil)
	case FailHostKeyChanged:
		return "Check the new key with the owner of the host. tuios does not accept a changed key for you."
	case FailAuth:
		return "Copy your ssh key to the host with this command. It asks for your password on the host one time: " + f.FixCommand(addr, nil)
	case FailDNS:
		return "Check the address. A name in your ssh config or on your tailnet also works."
	case FailRefused:
		return "Start the ssh server on the host, or check the port in the address."
	case FailTimeout:
		return "Turn on the host, or connect this machine to its network. tuios tries again."
	}
	return ""
}

// FixCommand is the command that fixes f for the host at addr with the ssh
// options opts, as a person types it, or "" when no command fixes it. Only a
// port and -o options are carried to ssh-copy-id, which accepts those two.
func (f SSHFailure) FixCommand(addr string, opts []string) string {
	switch f {
	case FailHostKeyNew:
		return joinCommand(append(append([]string{"ssh"}, opts...), addr, "true"))
	case FailAuth:
		words := []string{"ssh-copy-id"}
		for i := 0; i < len(opts); i++ {
			if (opts[i] == "-p" || opts[i] == "-o") && i+1 < len(opts) {
				words = append(words, opts[i], opts[i+1])
				i++
			}
		}
		return joinCommand(append(words, addr))
	}
	return ""
}

// NeedsPerson says the fix of f asks the person something only they can
// answer: a host key to check, or a password. tuios never takes either. It
// runs the command where the person can answer it.
func (f SSHFailure) NeedsPerson() bool {
	return f == FailHostKeyNew || f == FailAuth
}

// joinCommand joins words into one command line, each word quoted for sh when
// it needs it.
func joinCommand(words []string) string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = shellWord(w)
	}
	return strings.Join(out, " ")
}

// shellWord is w as sh reads it back unchanged.
func shellWord(w string) string {
	if w == "" {
		return "''"
	}
	plain := true
	for _, r := range w {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("@%+=:,./_-", r):
		default:
			plain = false
		}
	}
	if plain {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}
