package hostsetup

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/hostsync"
)

// The classes of failure besides the ssh ones federation names.
const (
	// FailTailscaleCheck is a login that waits for the person to sign in to
	// Tailscale in a browser.
	FailTailscaleCheck = federation.GateTailscaleCheck
	// FailTailscalePolicy is a login the tailnet policy refuses.
	FailTailscalePolicy = federation.GateTailscalePolicy
	// FailOther is an ssh failure whose words match no class.
	FailOther = "other"
)

// Failure is a step that ssh could not run, with its one fix. It is an error.
type Failure struct {
	// Class is a federation.SSHFailure, FailTailscaleCheck,
	// FailTailscalePolicy or FailOther.
	Class string `json:"class"`
	// Reason says what happened, about the machine by name.
	Reason string `json:"reason"`
	// Fix says what to do, and FixCommand is the command in it, as a person
	// types it here. Empty when no command fixes it.
	Fix        string `json:"fix,omitempty"`
	FixCommand string `json:"fix_command,omitempty"`
	// NeedsPerson says the fix asks for something only the person can give:
	// a host key to check, a password, a sign-in. tuios never takes them.
	NeedsPerson bool `json:"needs_person,omitempty"`
	// URL is the Tailscale sign-in page, with FailTailscaleCheck.
	URL string `json:"url,omitempty"`
	// SSHSaid is the last line ssh wrote. It came from another machine.
	SSHSaid string `json:"ssh_said,omitempty"`
}

func (f *Failure) Error() string {
	if f.Fix == "" {
		return f.Reason
	}
	return f.Reason + " " + f.Fix
}

// failureOf turns an error from a run over ssh into a Failure about the
// machine called name at addr with opts. An error that is not an ssh failure
// is returned as it is.
func failureOf(err error, name, addr string, opts []string) error {
	if err == nil {
		return nil
	}
	if ge := hostsync.GateErrorOf(err); ge != nil {
		return gateFailure(ge.Gate, name)
	}
	fe, ok := errors.AsType[*hostsync.SSHFailError](err)
	if !ok {
		return err
	}
	return sshFailure(fe.Failure, name, addr, opts, fe.Said)
}

// sshFailure is the Failure of one class of ssh failure.
func sshFailure(class federation.SSHFailure, name, addr string, opts []string, said string) *Failure {
	f := &Failure{
		Class:       string(class),
		Reason:      named(class.Reason(), name),
		Fix:         named(class.Fix(addr), name),
		FixCommand:  class.FixCommand(addr, opts),
		NeedsPerson: class.NeedsPerson(),
		SSHSaid:     said,
	}
	if class == "" {
		f.Class = FailOther
		f.Reason = name + " did not answer as expected."
		f.Fix = "Run ssh " + addr + " by hand to see the whole error."
	}
	return f
}

// gateFailure is the Failure of a Tailscale gate.
func gateFailure(g federation.SSHGate, name string) *Failure {
	if g.Kind == federation.GateTailscalePolicy {
		user := "this user"
		if g.User != "" {
			user = g.User
		}
		return &Failure{
			Class:  FailTailscalePolicy,
			Reason: fmt.Sprintf("The tailnet policy does not let you log in to %s as %s.", name, user),
			Fix:    "Change the user in the address.",
		}
	}
	return &Failure{
		Class:       FailTailscaleCheck,
		Reason:      "Tailscale SSH waits for you to sign in.",
		Fix:         "Open the sign-in page. Then test again.",
		NeedsPerson: true,
		URL:         g.URL,
	}
}

// named puts the machine's name in a sentence about "the host".
func named(sentence, name string) string {
	return strings.NewReplacer("The host", name, "the host", name).Replace(sentence)
}

// said is the last line of what ssh wrote, cleaned.
func said(stderr string) string {
	return clean(strings.TrimSpace(hostsync.LastLine(stderr)))
}
