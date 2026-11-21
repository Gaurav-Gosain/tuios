package session

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

const (
	queuePartText  = "text"
	queuePartPaste = "paste"
	queuePartImage = "image"
)

const queueMaxImages = 8

const (
	queueMaxParts        = 64
	queueMessageMaxBytes = 8 << 20
	queueInlineMax       = 12 << 10
	queuePaneMaxBytes    = 32 << 20
	queueDaemonMaxBytes  = 128 << 20
)

type queuePart struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	N    int    `json:"n,omitempty"`
	Data []byte `json:"data,omitempty"`
}

func queuePartsError(msg string) *verbError {
	return hintedVerbError(ErrVerbInvalidParams, msg, &VerbHint{
		Param:  "parts",
		Detail: "Nothing was queued. parts is a list of {kind, text}: kind text for typed text, paste for a pasted block, and image with data, the image file in base64.",
	})
}

func checkQueueParts(parts []queuePart) (string, int, *verbError) {
	return checkParts(parts, queueMaxText)
}

func checkParts(parts []queuePart, typedMax int) (string, int, *verbError) {
	if len(parts) > queueMaxParts {
		return "", 0, queuePartsError("parts holds " + strconv.Itoa(len(parts)) + " parts, and a message takes at most " + strconv.Itoa(queueMaxParts))
	}
	var shown strings.Builder
	size, blocks, images, typed := 0, 0, 0, 0
	blank := true
	for i := range parts {
		p := &parts[i]
		size += len(p.Text) + len(p.Data)
		if strings.TrimSpace(p.Text) != "" || p.Kind == queuePartImage {
			blank = false
		}
		if p.Kind != queuePartImage && p.Data != nil {
			return "", 0, queuePartsError("part " + strconv.Itoa(i+1) + " is " + p.Kind + " and carries data: only an image does")
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
			typed += len(label) + 256
		case queuePartImage:
			if p.Text != "" {
				return "", 0, queuePartsError("part " + strconv.Itoa(i+1) + " is an image and carries text: an image carries data")
			}
			if len(p.Data) > pasteImageMaxBytes {
				return "", 0, queuePartsError("part " + strconv.Itoa(i+1) + " is an image larger than " + units.Bytes(int64(pasteImageMaxBytes)))
			}
			if sniffImage(p.Data) == "" {
				return "", 0, queuePartsError("part " + strconv.Itoa(i+1) + ": " + errPasteNotImage.Error())
			}
			images++
			if images > queueMaxImages {
				return "", 0, queuePartsError("the message holds more than " + strconv.Itoa(queueMaxImages) + " images")
			}
			if p.N <= 0 || p.N > queueMaxParts*16 {
				p.N = images
			}
			label := "[Image #" + strconv.Itoa(p.N) + " · " + strings.TrimPrefix(sniffImage(p.Data), ".") + "]"
			shown.WriteString(label)
			typed += len(label) + 256
		default:
			return "", 0, queuePartsError("part " + strconv.Itoa(i+1) + " has kind " + echoName(p.Kind) + ": a part is text, paste or image")
		}
	}
	if blank {
		return "", 0, invalidParam("parts", "parts holds no text: the message to type when the agent is at rest")
	}
	if size > queueMessageMaxBytes {
		return "", 0, queuePartsError("the message is " + strconv.Itoa(size) + " bytes, and a message takes at most " + strconv.Itoa(queueMessageMaxBytes))
	}
	if typed > typedMax {
		return "", 0, queuePartsError("the typed text of the message is longer than " + strconv.Itoa(typedMax) + " bytes. Send long text as a paste part")
	}
	return shown.String(), size, nil
}

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

func hasImageParts(parts []queuePart) bool {
	for _, p := range parts {
		if p.Kind == queuePartImage {
			return true
		}
	}
	return false
}

func noImagesError(target WindowState) *verbError {
	return hintedVerbError(ErrVerbInvalidParams, "the agent in window "+shortWindowID(target.ID)+" cannot take images: its harness manifest says images = \"none\"", &VerbHint{
		Param:  "parts",
		Detail: "Nothing was queued. Remove the image, or attach the file's path as text.",
	})
}

func hasFileParts(parts []queuePart) bool {
	for _, p := range parts {
		if p.Kind != queuePartText {
			return true
		}
	}
	return false
}

func linkFilesRefusal(peer string) *verbError {
	from := "the machine at the other end of this link"
	if peer != "" {
		from = peer
	}
	return hintedVerbError(ErrVerbForbidden, "queue-prompt with pasted blocks from "+from+" is refused: a block can be written to a file here, and the link does not allow files", &VerbHint{
		Detail: "Nothing was queued. Add files to allow in [hosts." + firstNonEmpty(peer, "<peer>") + "] on this machine, or send the message as text.",
	})
}

func (d *Daemon) linkAllowsFiles(peer string) bool {
	var hosts linkPolicyTable
	if t := d.linkPolicies.Load(); t != nil {
		hosts = *t
	}
	return config.LinkPolicyFor(hosts, peer).Allows(config.LinkAllowFiles)
}

func (d *Daemon) composeQueued(sess *Session, target WindowState, e *queueEntry) ([]string, error) {
	if len(e.parts) == 0 {
		return []string{e.text}, nil
	}
	if e.typed != nil {
		return e.typed, nil
	}
	profile := d.inputProfileFor(sess, target.ID)
	bracketed := profile.BracketedPaste
	if bracketed {
		if pty, err := d.resolvePTYForTarget(sess, target.ID); err != nil || !pty.BracketedPasteOn() {
			bracketed = false
		}
	}
	text := 0
	for _, p := range e.parts {
		text += len(p.Text)
	}
	inline := text <= queueInlineMax && bracketed
	ownPaste := profile.Images == harness.ImagesPathPaste && bracketed
	var pastes []string
	var b strings.Builder
	held := map[string]bool{}
	for i := range e.parts {
		p := &e.parts[i]
		switch {
		case p.Kind == queuePartText, p.Kind == queuePartPaste && inline:
			b.WriteString(p.Text)
		case p.Kind == queuePartPaste:
			path, err := d.pastes.saveReply([]byte(p.Text), ".txt", held)
			if err != nil {
				return nil, err
			}
			b.WriteString(pasteLabel(p, path))
		case p.Kind == queuePartImage:
			path, err := d.pastes.saveReply(p.Data, sniffImage(p.Data), held)
			if err != nil {
				return nil, err
			}
			if ownPaste {
				pastes = append(pastes, b.String(), path)
				b.Reset()
				continue
			}
			b.WriteString("[Image #")
			b.WriteString(strconv.Itoa(p.N))
			b.WriteString("] ")
			b.WriteString(path)
			b.WriteString(" ")
		}
	}
	pastes = append(pastes, b.String())
	e.typed = pastes
	e.parts = []queuePart{{Kind: queuePartText}}
	return pastes, nil
}

func queueFailedSummary(w WindowState, why string) string {
	name := windowLabelOf(w)
	if name == "" {
		name = "the agent"
	}
	return "your queued message to " + name + " was not typed: a pasted block could not be saved (" + why + ")"
}

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

func groupDigits(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func partsText(parts []queuePart) (text string, images bool) {
	var b strings.Builder
	for _, p := range parts {
		if p.Kind == queuePartImage {
			images = true
			continue
		}
		b.WriteString(p.Text)
	}
	return b.String(), images
}

func (d *Daemon) fileParts(parts []queuePart) (string, []string, error) {
	var b strings.Builder
	held := map[string]bool{}
	var paths []string
	for i := range parts {
		p := &parts[i]
		switch p.Kind {
		case queuePartText:
			b.WriteString(p.Text)
		case queuePartPaste:
			path, err := d.pastes.saveReply([]byte(p.Text), ".txt", held)
			if err != nil {
				return "", paths, err
			}
			paths = append(paths, path)
			b.WriteString(pasteLabel(p, path))
		case queuePartImage:
			path, err := d.pastes.saveReply(p.Data, sniffImage(p.Data), held)
			if err != nil {
				return "", paths, err
			}
			paths = append(paths, path)
			b.WriteString("[Image #")
			b.WriteString(strconv.Itoa(p.N))
			b.WriteString(": ")
			b.WriteString(path)
			b.WriteString("]")
		}
	}
	return b.String(), paths, nil
}
