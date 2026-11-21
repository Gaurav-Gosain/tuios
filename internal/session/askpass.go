package session

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/askpass"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
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
//   - Only the person answers. askpass-answer takes the nonce of a client
//     attached right now, from a process outside every pane, the proof
//     answer-ask takes (human_sender.go, human_origin.go). An agent answers
//     no prompt, its own or another's.
//   - A program asks only from inside a pane, and only as that pane. The
//     daemon finds the pane and the program that asks from the kernel's
//     record of the process, never from what the caller says.
//   - The secret is held in memory only, for one answer. It is never in the
//     item, so it is never in the Inbox file, an event, a log line or a mail.
//     The item is not saved at all: the call that waits on it ends with the
//     daemon. The buffers the daemon owns are zeroed after the answer.
//   - Both verbs are refused over a link. A secret never leaves the machine
//     it is typed on for another one.

// askpassDefaultWait is how long askpass waits for the person when the caller
// names no timeout.
const askpassDefaultWait = 5 * time.Minute

// askpassMaxPrompt bounds a prompt, in bytes. A host key question with its
// fingerprint is a few hundred.
const askpassMaxPrompt = 1024

// askpassMaxSecret bounds an answer, in bytes.
const askpassMaxSecret = 4096

// askpassMaxProgram bounds the program line the dialog shows, in bytes.
const askpassMaxProgram = 200

// AskpassCancelled ends a prompt the person cancelled, or one whose program
// went away before the answer.
const AskpassCancelled = "cancelled"

// SecretAsk is what the secret dialog shows besides the asking pane. Nothing
// in it is the answer.
type SecretAsk struct {
	// Kind is the prompt's kind: passphrase, password, pin or secret
	// (internal/askpass).
	Kind string `json:"kind"`
	// Title is the dialog's heading.
	Title string `json:"title"`
	// Prompt is the prompt as the program wrote it.
	Prompt string `json:"prompt"`
	// Subject is the key file or account the prompt names, when it names one.
	Subject string `json:"subject,omitempty"`
	// Program is the command line of the program that asks, as the kernel
	// records it, with likely secrets masked.
	Program string `json:"program,omitempty"`
	// Known is true when the program is one that asks through an askpass
	// program in the normal course of its work, such as ssh or git. The
	// dialog warns about any other.
	Known bool `json:"known,omitempty"`
}

// askpassPromptShown reports whether a prompt can be shown as written: valid
// UTF-8, printable but for line breaks and tabs, and not too long.
func askpassPromptShown(s string) bool {
	if s == "" || len(s) > askpassMaxPrompt || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if !unicode.IsPrint(r) || InvisibleFormatRune(r) {
			return false
		}
	}
	return true
}

// askpassProgramLine is the command line the dialog shows for the process
// pid: its arguments joined with spaces, one line, likely secrets masked.
func askpassProgramLine(args []string) string {
	line := strings.Join(args, " ")
	return attentionText(line, askpassMaxProgram)
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
	if cs != nil && cs.viaLink {
		return nil, hintedVerbError(ErrVerbForbidden, "askpass is refused over a link: a secret is typed on the machine where the program asks", &VerbHint{
			Detail: "Nothing was asked.",
		})
	}
	if !askpassPromptShown(p.Prompt) {
		return nil, invalidParam("prompt", "prompt is required: printable text, at most 1024 bytes, which the dialog shows as written")
	}
	if p.Timeout < 0 {
		return nil, invalidParam("timeout", "timeout is milliseconds and cannot be negative")
	}
	wait := askpassDefaultWait
	if p.Timeout > 0 {
		wait = min(time.Duration(p.Timeout)*time.Millisecond, maxAskWait)
	}
	if !d.agentsEnabled() {
		return nil, hintedVerbError(ErrVerbAgentsDisabled, "askpass asks in the Inbox, and the agent features are off", &VerbHint{
			Detail: "Nothing was asked. Run the command in a pane where you can type the answer.",
		})
	}
	fromPane, own := d.peerPane(cs)
	if !fromPane || own == "" {
		return nil, hintedVerbError(ErrVerbForbidden, "askpass is for a program inside a pane of this daemon", &VerbHint{
			Detail: "Nothing was asked. Run the program in a tuios pane, or answer its prompt in your own terminal.",
		})
	}

	read := askpass.Read(p.Prompt)
	secret := &SecretAsk{Kind: read.Kind, Title: read.Title, Prompt: read.Text, Subject: read.Subject}
	if cs != nil && cs.peerPID > 0 {
		// The caller is the askpass program. The program that asks is the
		// one that started it.
		if ppid, ok := askpassParent(cs.peerPID); ok {
			exe, args := askingProgram(ppid)
			secret.Program = askpassProgramLine(args)
			secret.Known = exe != "" && askpass.KnownProgram(exe)
		}
	}
	summary := read.Title
	if read.Subject != "" {
		summary += ": " + read.Subject
	}
	item := AttentionItem{Summary: attentionText(summary, attentionMaxSummary), Secret: secret}
	for _, sess := range d.manager.AllSessions() {
		st := sess.GetState()
		if idx, err := findWindowStateIndex(st.Windows, own); err == nil {
			w := st.Windows[idx]
			item.Session, item.Window, item.Name = sess.Name(), w.ID, windowLabelOf(w)
			item.Harness, item.Workspace = w.AgentHarness, w.Workspace
			break
		}
	}
	if item.Session == "" {
		return nil, hintedVerbError(ErrVerbForbidden, "askpass found no session that holds the caller's pane", &VerbHint{
			Detail: "Nothing was asked.",
		})
	}
	hold, err := d.attention.openAsk(item, true)
	if err != nil {
		return nil, hintedVerbError(ErrVerbRateLimited, "the prompt could not be opened: "+err.Error(), nil)
	}
	LogBasic("Secret prompt %s opened for pane %s in %s", hold.id, shortID(item.Window), item.Session)

	// A program that is killed while it waits, such as an ssh the agent
	// stopped, takes its prompt with it.
	gone := make(chan struct{})
	if canWaitForeignExit && cs != nil && cs.peerPID > 0 {
		pid := cs.peerPID
		go func() {
			waitForeignExit(pid)
			close(gone)
		}()
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
	case <-d.ctx.Done():
		out, _ = d.attention.closeAsk(hold, askEndShutdown)
	}
	LogBasic("Secret prompt %s ended: %s", hold.id, out.Reason)
	res := map[string]any{"type": "askpass", "request_id": hold.id, "status": out.Reason}
	if out.Reason != AskAnswered {
		return res, nil
	}
	select {
	case b := <-hold.secret:
		// The reply is encoded before the handler's caller writes it, so a
		// copy goes to the encoder and this buffer is zeroed when the call
		// returns.
		res["secret"] = append([]byte(nil), b...)
		clear(b)
		return askpassReply{fields: res}, nil
	default:
		res["status"] = AskpassCancelled
		return res, nil
	}
}

// askpassReply is askpass's answer. It zeroes the copy of the secret once the
// reply is encoded.
type askpassReply struct {
	fields map[string]any
}

// MarshalJSON encodes the reply and zeroes the secret it held.
func (r askpassReply) MarshalJSON() ([]byte, error) {
	data, err := jsonx.Marshal(r.fields)
	if b, ok := r.fields["secret"].([]byte); ok {
		clear(b)
	}
	return data, err
}

// verbAskpassAnswer puts the person's answer to a secret prompt in.
func (d *Daemon) verbAskpassAnswer(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		RequestID  string `json:"request_id"`
		HumanNonce string `json:"human_nonce"`
		// Secret is the answer, base64 in JSON, so it is decoded straight
		// into a buffer that can be zeroed.
		Secret []byte `json:"secret"`
		Cancel bool   `json:"cancel"`
	}
	verr := decodeParams(params, &p)
	defer clear(p.Secret)
	if verr != nil {
		return nil, verr
	}
	if cs != nil && cs.viaLink {
		return nil, hintedVerbError(ErrVerbForbidden, "askpass-answer is refused over a link: a secret is typed on the machine where the program asks", &VerbHint{
			Detail: "Nothing was answered. Answer at a client attached to that machine.",
		})
	}
	if p.RequestID == "" {
		return nil, invalidParam("request_id", "request_id is required: the prompt's, from its Inbox item")
	}
	if !p.Cancel && len(p.Secret) == 0 {
		return nil, invalidParam("secret", "secret is required unless cancel is true")
	}
	if len(p.Secret) > askpassMaxSecret {
		return nil, invalidParam("secret", "secret is at most 4096 bytes")
	}
	if !d.humanNonceHeld(p.HumanNonce, cs) {
		return nil, hintedVerbError(ErrVerbNotHuman, "askpass-answer is for the person at an attached client", &VerbHint{
			Param:  "human_nonce",
			Detail: "Nothing was answered. Only a client attached right now can answer a prompt, with the nonce its attach reply carried. An agent never can.",
		})
	}
	askSession, _ := d.attention.askSession(p.RequestID)
	by, ok := d.humanNonceFor(p.HumanNonce, d.sessionIDOf(askSession), cs)
	if !ok {
		return nil, nonceScopeError("askpass-answer")
	}
	applied, reason, err := d.attention.answerSecret(p.RequestID, p.Secret, p.Cancel, by)
	if errors.Is(err, errNoHold) {
		return nil, invalidParam("request_id", "no secret prompt is open under request "+echoName(p.RequestID))
	}
	if applied {
		LogBasic("Secret prompt %s %s by %s", p.RequestID, reason, by)
	}
	return map[string]any{"type": "askpass_answered", "request_id": p.RequestID, "applied": applied, "reason": reason}, nil
}

// answerSecret ends an open secret prompt with the person's answer, or with
// a cancel. The item closes with no answer on it. It reports whether this
// call ended the prompt, and how the prompt ended.
func (a *attentionStore) answerSecret(requestID string, secret []byte, cancel bool, by string) (bool, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	hold, ok := a.asks[requestID]
	if !ok {
		if prev, ok := a.askSettled[requestID]; ok {
			return false, prev.Reason, nil
		}
		return false, "", errNoHold
	}
	if it, ok := a.items[hold.itemID]; !ok || it.Secret == nil {
		return false, "", errNoHold
	}
	if cancel {
		a.closeLocked(hold.itemID, AskpassCancelled)
		return true, AskpassCancelled, nil
	}
	// The waiting call takes it from here. hold.secret holds one buffer, and
	// a hold is answered once, since the answer deletes it.
	hold.secret <- append([]byte(nil), secret...)
	a.endAskLocked(requestID, askOutcome{Reason: AskAnswered, By: by})
	a.closeLocked(hold.itemID, AttentionClosedAnswered)
	return true, AskAnswered, nil
}

// askIsSecret reports whether the open question under requestID is a secret
// prompt, which only askpass-answer answers.
func (a *attentionStore) askIsSecret(requestID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	h, ok := a.asks[requestID]
	if !ok {
		return false
	}
	it, ok := a.items[h.itemID]
	return ok && it.Secret != nil
}
