package session

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A queued message in parts.
//
// The reply composer holds a long paste as one block. queue-prompt takes the
// message as ordered parts so that the daemon, on the machine where the pane
// runs, decides how each block reaches the agent, and decides it when the
// message is typed, not when it is queued:
//
//   - When the whole message is queueInlineMax or less and the pane takes a
//     bracketed paste, every block is typed in its place, as one paste. The
//     agent's own paste handling and history see it as the person pasted it.
//   - Otherwise each block is written to a file in the paste directory, and
//     the typed text names the file in its place: [Pasted text #1: PATH,
//     42 lines]. Large text stays out of the agent's input box, and the typed
//     message stays under queueMaxText whatever the blocks hold.
//
// Files are written at typing time, so a message that waits in the queue for
// an hour never points at a file the sweep already deleted. They are kept for
// pasteReplyTTL. Written once, their paths are kept with the entry, so an
// entry typed again after a failed write to the pane writes no second copy.
//
// The text parts alone, with each block's label, must fit queueMaxText, which
// is what is typed in the worst case. The whole message must fit
// queueMessageMaxBytes, which keeps one request under maxVerbLine. A pane's
// queue and the daemon's queues are bounded in bytes as well as entries.

// Part kinds on the wire.
const (
	queuePartText  = "text"
	queuePartPaste = "paste"
)

const (
	// queueMaxParts bounds the parts of one message.
	queueMaxParts = 64
	// queueMessageMaxBytes bounds one message, its blocks included. The
	// request carries it in one line, and maxVerbLine is 12 MiB.
	queueMessageMaxBytes = 8 << 20
	// queueInlineMax is the largest message whose blocks are typed in their
	// place. Above it, every block goes as a file.
	queueInlineMax = 12 << 10
	// queuePaneMaxBytes and queueDaemonMaxBytes bound what the queues hold.
	queuePaneMaxBytes   = 32 << 20
	queueDaemonMaxBytes = 128 << 20
)

// queuePart is one part of a queued message, as queue-prompt takes it.
type queuePart struct {
	// Kind is text or paste.
	Kind string `json:"kind"`
	// Text is the part's text.
	Text string `json:"text"`
	// N is a block's number, as the person's label showed it. Zero numbers
	// the block by its place among the blocks.
	N int `json:"n,omitempty"`
}

// queuePartsError is the refusal of a message whose parts are not valid.
func queuePartsError(msg string) *verbError {
	return hintedVerbError(ErrVerbInvalidParams, msg, &VerbHint{
		Param:  "parts",
		Detail: "Nothing was queued. parts is a list of {kind, text}: kind text for typed text and paste for a pasted block.",
	})
}

// checkQueueParts checks a message's parts, numbers its blocks, and returns
// the message as list-queued and the log show it, with each block as its
// label, and the bytes it holds.
func checkQueueParts(parts []queuePart) (string, int, *verbError) {
	if len(parts) > queueMaxParts {
		return "", 0, queuePartsError("parts holds " + strconv.Itoa(len(parts)) + " parts, and a message takes at most " + strconv.Itoa(queueMaxParts))
	}
	var shown strings.Builder
	size, blocks, typed := 0, 0, 0
	blank := true
	for i := range parts {
		p := &parts[i]
		size += len(p.Text)
		if strings.TrimSpace(p.Text) != "" {
			blank = false
		}
		switch p.Kind {
		case queuePartText:
			shown.WriteString(p.Text)
			typed += len(p.Text)
		case queuePartPaste:
			if p.Text == "" {
				return "", 0, queuePartsError("part " + strconv.Itoa(i+1) + " is a paste with no text")
			}
			blocks++
			if p.N <= 0 || p.N > queueMaxParts*16 {
				p.N = blocks
			}
			label := pasteLabel(p, "")
			shown.WriteString(label)
			// The label with a path in it, the longest form a block takes
			// when it is typed.
			typed += len(label) + 256
		default:
			return "", 0, queuePartsError("part " + strconv.Itoa(i+1) + " has kind " + echoName(p.Kind) + ": a part is text or paste")
		}
	}
	if blank {
		return "", 0, invalidParam("parts", "parts holds no text: the message to type when the agent is at rest")
	}
	if size > queueMessageMaxBytes {
		return "", 0, queuePartsError("the message is " + strconv.Itoa(size) + " bytes, and a message takes at most " + strconv.Itoa(queueMessageMaxBytes))
	}
	if typed > queueMaxText {
		return "", 0, queuePartsError("the typed text of the message is longer than " + strconv.Itoa(queueMaxText) + " bytes. Send long text as a paste part")
	}
	return shown.String(), size, nil
}

// pasteLabel is the label a block takes in the typed text. With no path it is
// the label the person saw: [Pasted text #1 · 42 lines]. With a path it names
// the file: [Pasted text #1: PATH, 42 lines].
func pasteLabel(p *queuePart, path string) string {
	body := strings.TrimRight(p.Text, "\n")
	measure := groupDigits(strings.Count(body, "\n")+1) + " lines"
	if !strings.Contains(body, "\n") {
		measure = groupDigits(utf8.RuneCountInString(p.Text)) + " chars"
	}
	if path == "" {
		return "[Pasted text #" + strconv.Itoa(p.N) + " · " + measure + "]"
	}
	return "[Pasted text #" + strconv.Itoa(p.N) + ": " + path + ", " + measure + "]"
}

// hasFileParts reports whether a message may put a file on this machine.
func hasFileParts(parts []queuePart) bool {
	for _, p := range parts {
		if p.Kind != queuePartText {
			return true
		}
	}
	return false
}

// linkFilesRefusal is the refusal of a message with blocks from a linked
// machine whose policy here does not allow files.
func linkFilesRefusal(peer string) *verbError {
	from := "the machine at the other end of this link"
	if peer != "" {
		from = peer
	}
	return hintedVerbError(ErrVerbForbidden, "queue-prompt with pasted blocks from "+from+" is refused: a block can be written to a file here, and the link does not allow files", &VerbHint{
		Detail: "Nothing was queued. Add files to allow in [hosts." + firstNonEmpty(peer, "<peer>") + "] on this machine, or send the message as text.",
	})
}

// linkAllowsFiles reports whether the link that queued e may still put a
// file on this machine.
func (d *Daemon) linkAllowsFiles(peer string) bool {
	var hosts linkPolicyTable
	if t := d.linkPolicies.Load(); t != nil {
		hosts = *t
	}
	return config.LinkPolicyFor(hosts, peer).Allows(config.LinkAllowFiles)
}

// composeQueued is the text an entry in parts is typed as: its blocks in
// their place when the message is small and the pane takes a bracketed paste,
// and as files otherwise. It writes the files once and keeps the text on the
// entry. An entry without parts is typed as its text.
func (d *Daemon) composeQueued(sess *Session, target WindowState, e *queueEntry) (string, error) {
	if len(e.parts) == 0 {
		return e.text, nil
	}
	if e.typed != "" {
		return e.typed, nil
	}
	inline := e.size <= queueInlineMax && d.inputProfileFor(sess, target.ID).BracketedPaste
	if inline {
		if pty, err := d.resolvePTYForTarget(sess, target.ID); err != nil || !pty.BracketedPasteOn() {
			inline = false
		}
	}
	var b strings.Builder
	held := map[string]bool{}
	for i := range e.parts {
		p := &e.parts[i]
		if p.Kind == queuePartText || inline {
			b.WriteString(p.Text)
			continue
		}
		path, err := d.pastes.saveReply([]byte(p.Text), ".txt", held)
		if err != nil {
			return "", err
		}
		b.WriteString(pasteLabel(p, path))
	}
	e.typed = b.String()
	// The parts are typed now. Only the text is kept, for a second try.
	e.parts = []queuePart{{Kind: queuePartText}}
	return e.typed, nil
}

// queueFailedSummary is the Inbox question for an entry whose files could not
// be written.
func queueFailedSummary(w WindowState, why string) string {
	name := windowLabelOf(w)
	if name == "" {
		name = "the agent"
	}
	return "your queued message to " + name + " was not typed: a pasted block could not be saved (" + why + ")"
}

// openQueueFailed opens a question on the pane's blocking key for an entry
// that was dropped because its files could not be written. The person's
// message is gone from the queue, and they must hear of it.
func (a *attentionStore) openQueueFailed(sessionName string, w WindowState, why string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.upsertLocked(AttentionItem{
		Kind:      AttentionQuestion,
		Session:   sessionName,
		Window:    w.ID,
		Workspace: w.Workspace,
		Harness:   w.AgentHarness,
		Name:      attentionText(windowLabelOf(w), attentionMaxSummary),
		Summary:   attentionText(queueFailedSummary(w, why), attentionMaxSummary),
	})
}

// groupDigits writes n with a comma between each group of three digits, as
// the composer's labels do.
func groupDigits(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
