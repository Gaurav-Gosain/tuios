package session

import (
	"encoding/json"
	"path/filepath"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/askpass"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/shimlink"
)

// Secrets a program in a pane asks the person for.
//
// ssh asks for a key's passphrase, git for a password and gpg for a PIN. An
// agent's command has no terminal to ask on, so the program calls askpass,
// and the daemon asks the person where they are: an ask in the Inbox, marked
// with Secret, that a client opens as a dialog with a hidden field
// (internal/app/askpass_dialog.go). The answer goes back to the call that
// waits, which hands it to the program.
//
// The rules it keeps:
//
//   - Only the person answers. answer-ask takes the secret only with the
//     nonce of a client attached right now, from a process outside every
//     pane (human_sender.go, human_origin.go). An agent answers no prompt,
//     its own or another's.
//   - A program asks only from inside a pane, and only as that pane. The
//     daemon finds the pane and the program that asks from the kernel's
//     record of the process, never from what the caller says.
//   - The secret is held in memory only, for one answer. It is never in the
//     item, so it is never in the Inbox file, an event, a log line or a mail.
//     The item is not saved at all: the call that waits on it ends with the
//     daemon. The daemon zeroes the buffers it decodes the secret into and
//     hands it on in. The copies inside the encoded request and reply frames
//     are not cleared: Go gives no hold on them, and they are reused.
//   - askpass and a secret's answer are refused over a link. A secret never
//     leaves the machine it is typed on for another one.

// askpassDefaultWait is how long askpass waits for the person when the caller
// names no timeout.
const askpassDefaultWait = 5 * time.Minute

// Bounds, in bytes: a prompt (a host key question with its fingerprint is a
// few hundred), an answer, and the program line the dialog shows.
const (
	askpassMaxPrompt  = 1024
	askpassMaxSecret  = 4096
	askpassMaxProgram = 512
)

// AskpassCancelled ends a prompt the person cancelled, or one whose program
// went away before the answer.
const AskpassCancelled = "cancelled"

// SecretAsk is what the secret dialog shows besides the asking pane. Nothing
// in it is the answer.
type SecretAsk struct {
	// Kind is the prompt's kind: passphrase, password, pin or secret
	// (internal/askpass).
	Kind string `json:"kind"`
	// Prompt is the prompt as the program wrote it.
	Prompt string `json:"prompt"`
	// Program is the command line of the program that asks, as the kernel
	// records it, with the passwords and tokens in it masked
	// (askpass.MaskCommandLine).
	Program string `json:"program,omitempty"`
}

// askpassPromptShown reports whether a prompt can be shown as written: valid
// UTF-8, printable but for line breaks and tabs, and not too long.
func askpassPromptShown(s string) bool {
	if s == "" || len(s) > askpassMaxPrompt || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r != '\n' && r != '\t' && (!unicode.IsPrint(r) || InvisibleFormatRune(r)) {
			return false
		}
	}
	return true
}

// AskpassLinkName is the name tuios's askpass program runs by: a link to the
// tuios binary, which main runs as the askpass program when it is called by
// this name.
const AskpassLinkName = "tuios-askpass"

// installAskpassLink makes the tuios-askpass link beside the daemon socket
// and points every new pane's SSH_ASKPASS at it. OpenSSH runs SSH_ASKPASS as
// one path with the prompt as its argument, so the program is chosen by the
// link's name, not by an argument.
func (d *Daemon) installAskpassLink(socketPath string) {
	exe, err := d.agentProtoExecutable()
	if err != nil {
		return
	}
	link, err := shimlink.Install(filepath.Join(filepath.Dir(socketPath), "askpass"), AskpassLinkName, exe)
	if err != nil {
		LogBasic("The askpass link could not be made: %v. Programs in panes ask for passphrases on their terminal only.", err)
		return
	}
	d.manager.SetAskpassBin(link)
	d.manager.SetAskpassAgentsOn(d.agentsEnabled)
}

// askpassRefused is askpass's refusal, with nothing asked.
func askpassRefused(why string) *verbError {
	return hintedVerbError(ErrVerbForbidden, why, &VerbHint{Detail: "Nothing was asked. Answer the prompt in a terminal where you can type."})
}

// verbAskpass asks the person for a secret on behalf of a program in the
// caller's pane, and waits for the answer.
func (d *Daemon) verbAskpass(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Prompt  string `json:"prompt"`
		Timeout int    `json:"timeout"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if !askpassPromptShown(p.Prompt) || p.Timeout < 0 {
		return nil, invalidParam("prompt", "prompt is required: printable text, at most 1024 bytes. timeout is milliseconds, not negative")
	}
	wait := askpassDefaultWait
	if p.Timeout > 0 {
		wait = min(time.Duration(p.Timeout)*time.Millisecond, maxAskWait)
	}
	if cs == nil || cs.viaLink || !d.agentsEnabled() {
		return nil, askpassRefused("askpass asks in the Inbox of this machine: it is refused over a link and with the agent features off")
	}
	_, own := d.peerPane(cs)
	read := askpass.Read(p.Prompt)
	item := AttentionItem{Summary: attentionText(read.Text, attentionMaxSummary), Secret: &SecretAsk{Kind: read.Kind, Prompt: read.Text}}
	if own == "" || !d.placeOnPane(own, &item) {
		return nil, askpassRefused("askpass is for a program inside a pane of this daemon")
	}
	// The caller is the askpass program. The program that asks is the one
	// that started it. The dialog shows its command line, and never says
	// the prompt can be trusted: a process in the pane can run under a real
	// ssh, as a ProxyCommand, and ask anything.
	if ppid, ok := askpassParent(cs.peerPID); ok {
		item.Secret.Program = attentionText(askpass.MaskCommandLine(askingProgram(ppid)), askpassMaxProgram)
	}
	hold, err := d.attention.openAsk(item, true)
	if err != nil {
		return nil, hintedVerbError(ErrVerbRateLimited, "the prompt could not be opened: "+err.Error(), nil)
	}
	LogBasic("Secret prompt %s opened for pane %s", hold.id, shortID(own))

	// A program that is killed while it waits, such as an ssh the agent
	// stopped, takes its prompt with it. OpenSSH does not stop its askpass
	// program when ssh itself is killed, so the daemon watches both.
	stop := make(chan struct{})
	defer close(stop)
	gone := askpassWatch(cs.peerPID, stop)
	asker := make(<-chan struct{})
	if ppid, ok := askpassParent(cs.peerPID); ok && ppid > 1 {
		asker = askpassWatch(ppid, stop)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	var out askOutcome
	select {
	case out = <-hold.done:
	case <-timer.C:
		out, _ = d.attention.closeAsk(hold, approvalEndTimeout)
	case <-gone:
		out, _ = d.attention.closeAsk(hold, AskpassCancelled)
	case <-asker:
		out, _ = d.attention.closeAsk(hold, AskpassCancelled)
	case <-d.ctx.Done():
		out, _ = d.attention.closeAsk(hold, askEndShutdown)
	}
	LogBasic("Secret prompt %s ended: %s", hold.id, out.Reason)
	res := askpassReply{"type": "askpass", "request_id": hold.id, "status": out.Reason}
	if out.Reason == AskAnswered {
		select {
		case b := <-hold.secret:
			res["secret"] = b
		default:
			res["status"] = AskpassCancelled
		}
	}
	return res, nil
}

// askpassReply is askpass's answer. It zeroes the secret once the reply is
// encoded.
type askpassReply map[string]any

// MarshalJSON encodes the reply and zeroes the secret it held.
func (r askpassReply) MarshalJSON() ([]byte, error) {
	data, err := jsonx.Marshal(map[string]any(r))
	if b, ok := r["secret"].([]byte); ok {
		clear(b)
	}
	return data, err
}

// answerSecret ends an open secret prompt with the person's answer, or with
// a cancel. The item closes with no answer on it. It reports whether this
// call ended the prompt and how the prompt ended, and ok false when request
// is no secret prompt, open or ended.
func (a *attentionStore) answerSecret(requestID string, secret []byte, cancel bool, by string) (applied bool, reason string, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	hold, open := a.asks[requestID]
	if !open || hold.secret == nil {
		prev, settled := a.askSettled[requestID]
		return false, prev.Reason, settled && !open
	}
	if cancel {
		a.closeLocked(hold.itemID, AskpassCancelled)
		return true, AskpassCancelled, true
	}
	// The waiting call takes it from here. hold.secret holds one buffer, and
	// a hold is answered once, since the answer deletes it.
	hold.secret <- append([]byte(nil), secret...)
	a.endAskLocked(requestID, askOutcome{Reason: AskAnswered, By: by})
	a.closeLocked(hold.itemID, AttentionClosedAnswered)
	return true, AskAnswered, true
}
