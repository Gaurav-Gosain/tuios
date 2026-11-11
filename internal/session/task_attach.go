package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/ghpr"
	"github.com/Gaurav-Gosain/tuios/internal/invisible"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// Attachments on task cards: a picture, a text, a diff or a link an agent or
// the person puts on a card, so the board can show the proof of the work
// beside the card.
//
// The caller sends the bytes. The daemon never opens a path a caller names:
// a pane could otherwise have the daemon read a file the pane cannot. The
// bytes are stored once by their sha256 under the state folder, beside the
// saved sessions that name them, and the card holds only a reference
// (TaskAttachment). So a card's attachments survive a restart and a live
// upgrade, as the card does, and a state push carries none of them
// (pushCopy): a client reads the list with get-task and the bytes with
// get-task-attachment.
//
// A stored file goes when no card names it any more: after remove-task,
// detach-task and a replace, the old hashes are checked, and at the daemon's
// start every stored file is. A card of a saved session that is not running
// counts, so a session brought back later finds its files. A file an attach
// is still writing or putting on its card is never removed (attachSweeper
// pending).
//
// Who may attach: the pane that holds the card, and the pane that did the
// work on it while the card waits in verify or review (task done lets the
// card go before the agent can attach its proof). Admin, the person, a shell,
// a plugin and a link attach to any card. detach-task follows the same rule.

// The kinds of attachment. They are wire values: the board draws each kind
// its own way.
const (
	AttachImage    = "image"
	AttachMarkdown = "markdown"
	AttachMermaid  = "mermaid"
	AttachDiff     = "diff"
	AttachText     = "text"
	AttachLink     = "link"
	AttachPR       = "pr"
	AttachFile     = "file"
)

// AttachKinds is every kind, for the catalogue.
var AttachKinds = []string{AttachImage, AttachMarkdown, AttachMermaid, AttachDiff, AttachText, AttachLink, AttachPR, AttachFile}

// Error codes of the attachment verbs.
const (
	// ErrVerbAttachmentTooLarge reports a file over [tasks]
	// attachment_max_mib, or a session over attachment_session_mib.
	ErrVerbAttachmentTooLarge = "attachment_too_large"
	// ErrVerbAttachmentLimit reports a card that holds
	// [tasks] attachments_per_card already.
	ErrVerbAttachmentLimit = "attachment_limit"
	// ErrVerbNoAttachment reports a name the card has no attachment by.
	ErrVerbNoAttachment = "no_attachment"
)

// The history and event actions of an attachment.
const (
	TaskActionAttached = "attached"
	TaskActionDetached = "detached"
)

// Bounds that are not the person's to set.
const (
	attachMaxName = 120
	attachMaxNote = 500
	attachMaxURL  = 2048
	// attachHardMaxBytes is the most one file may be whatever the config
	// says: its base64 must fit one request line (maxVerbLine).
	attachHardMaxBytes = stashTransferMaxBytes
)

// TaskAttachment is one attachment on a card. A link has URL and no Hash.
type TaskAttachment struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	MediaType string `json:"media_type,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Hash      string `json:"hash,omitempty"`
	URL       string `json:"url,omitempty"`
	Note      string `json:"note,omitempty"`
	By        string `json:"by"`
	At        int64  `json:"at"`
}

// attachLimits are [tasks] attachment_max_mib, attachments_per_card and
// attachment_session_mib, as the daemon holds them.
type attachLimits struct {
	maxBytes, sessionBytes int64
	perCard                int
}

// setAttachLimits takes the [tasks] attachment limits.
func (d *Daemon) setAttachLimits(c config.TasksConfig) {
	var l attachLimits
	l.maxBytes, l.sessionBytes, l.perCard = c.AttachLimits()
	l.maxBytes = min(l.maxBytes, attachHardMaxBytes)
	d.attachLimit.Store(&l)
}

// attachLimits are the limits now: the defaults until a config is read.
func (d *Daemon) attachLimits() attachLimits {
	if l := d.attachLimit.Load(); l != nil {
		return *l
	}
	var l attachLimits
	l.maxBytes, l.sessionBytes, l.perCard = config.TasksConfig{}.AttachLimits()
	return l
}

// attachmentDir is the store's folder: beside the saved sessions, so it
// follows the named server's state folder and a test's override.
func attachmentDir() string {
	return filepath.Join(filepath.Dir(getResurrectionDir()), "attachments")
}

// attachHashRE is a stored file's name: a sha256 in hex.
var attachHashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// attachmentPath is where the file with hash is stored.
func attachmentPath(hash string) (string, error) {
	if !attachHashRE.MatchString(hash) {
		return "", errors.New("not an attachment hash")
	}
	return filepath.Join(attachmentDir(), hash[:2], hash), nil
}

// attachHash is the store's name for data.
func attachHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// storeAttachment writes data under hash. A file already stored is kept.
func storeAttachment(hash string, data []byte) error {
	path, err := attachmentPath(hash)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() == int64(len(data)) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicfile.WritePrivate(path, data, true)
}

// readAttachment reads the stored file with hash.
func readAttachment(hash string) ([]byte, error) {
	path, err := attachmentPath(hash)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path) // #nosec G304 -- the path is the store's own, built from a checked hash
}

// classifyCardFile is the kind and media type of a file named name: by its
// content first, then by the name's extension.
func classifyCardFile(name string, data []byte) (kind, media string) {
	switch sniffImage(data) {
	case ".png":
		return AttachImage, "image/png"
	case ".jpg":
		return AttachImage, "image/jpeg"
	case ".gif":
		return AttachImage, "image/gif"
	case ".webp":
		return AttachImage, "image/webp"
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return AttachFile, "application/octet-stream"
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return AttachMarkdown, "text/markdown"
	case ".mmd", ".mermaid":
		return AttachMermaid, "text/vnd.mermaid"
	case ".diff", ".patch":
		return AttachDiff, "text/x-diff"
	}
	if looksLikeDiff(data) {
		return AttachDiff, "text/x-diff"
	}
	return AttachText, "text/plain"
}

// looksLikeDiff reports a text that starts as a unified diff does.
func looksLikeDiff(data []byte) bool {
	head := data[:min(len(data), 4096)]
	if bytes.HasPrefix(head, []byte("diff --git ")) {
		return true
	}
	return bytes.HasPrefix(head, []byte("--- ")) && bytes.Contains(head, []byte("\n+++ ")) && bytes.Contains(head, []byte("\n@@ "))
}

// linkAttachment checks raw as a link and returns its kind.
func linkAttachment(raw string) (string, *verbError) {
	if len(raw) > attachMaxURL || invisible.Strip(raw) != raw || strings.ContainsAny(raw, " \t\r\n") {
		return "", invalidParam("link", "link is not an address tuios keeps: at most "+strconv.Itoa(attachMaxURL)+" bytes, no spaces and no invisible characters")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", invalidParam("link", "link must be an http or https address")
	}
	if ghpr.IsURL(raw) {
		return AttachPR, nil
	}
	return AttachLink, nil
}

// attachName is a name as a card keeps it: printable, with no invisible
// characters, so a name reads the same on the board, in a prompt and in the
// CLI. The store names a file by its hash, so a name never becomes a path.
// Empty when nothing is left.
func attachName(raw string) string {
	return invisible.Strip(printableClaim(raw, attachMaxName))
}

// mayAttach refuses an attach or a detach by a caller that may not change
// tk's attachments.
func (c taskCaller) mayAttach(verb string, tk *TaskState) *verbError {
	if c.any || tk.Holder == c.window {
		return nil
	}
	if w := tk.Work; w != nil && w.Host == "" && w.Window == c.window && tk.Holder == "" &&
		(tk.Stage == TaskStageVerify || tk.Stage == TaskStageReview) {
		return nil
	}
	return hintedVerbError(ErrVerbForbidden, verb+" of "+tk.ID+" is refused: a pane changes the attachments of the card it holds, or of the card it finished while that card waits in verify or review", &VerbHint{
		Command: "tuios task claim " + tk.ID,
		Detail:  "Nothing changed. Only the person, a shell outside every pane, or a pane holding admin changes any card.",
	})
}

// sessionAttachBytes is what the files of the session's cards take, each
// stored file counted once.
func sessionAttachBytes(st *SessionState) int64 {
	seen := map[string]bool{}
	var n int64
	for _, tk := range st.Tasks {
		for _, a := range tk.Attachments {
			if a.Hash != "" && !seen[a.Hash] {
				seen[a.Hash] = true
				n += a.Size
			}
		}
	}
	return n
}

// attachIndex is the index of the attachment called name on tk, or -1.
func attachIndex(tk *TaskState, name string) int {
	return slices.IndexFunc(tk.Attachments, func(a TaskAttachment) bool { return a.Name == name })
}

// attachTaskParams are what attach-task takes.
type attachTaskParams struct {
	Session    string `json:"session"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Note       string `json:"note"`
	Content    string `json:"content"`
	Link       string `json:"link"`
	Replace    bool   `json:"replace"`
	HumanNonce string `json:"human_nonce"`
	// Screenshot and Capture name a pane of the card's session whose
	// picture or text the daemon takes and attaches.
	Screenshot *attachPaneParams `json:"screenshot"`
	Capture    *attachPaneParams `json:"capture"`
	// Files are several files put on the card in one change.
	Files []attachFileParam `json:"files"`
	// Host is the machine that owns the card: the attach is passed on to
	// it for the calling pane, which holds the card there.
	Host string `json:"host"`
	remoteClaim
}

// attachPaneParams name the pane a picture or a text is taken of.
type attachPaneParams struct {
	Window     string `json:"window"`
	Scrollback bool   `json:"scrollback"`
	Lines      int    `json:"lines"`
}

// attachCaptureLines is how many lines a capture keeps when none are asked
// for.
const attachCaptureLines = 2000

// takeFromPane takes the picture (shot) or the text of the pane pp names in
// sess for a card, and the name it is attached by when none was given. A pane
// that calls needs the read grant, as screenshot and capture-pane do; the
// pane is one of the card's session, which a pane with the task grant
// reaches.
func (d *Daemon) takeFromPane(cs *connState, caller taskCaller, sess *Session, pp *attachPaneParams, shot bool) ([]byte, string, *verbError) {
	param := "capture"
	if shot {
		param = "screenshot"
	}
	if pp.Window == "" {
		return nil, "", invalidParam(param, param+" needs window: the pane to take")
	}
	if caller.window != "" && !caller.any {
		if pa := d.paneAuthority(cs); pa == nil || !pa.grants.Has(GrantRead) {
			return nil, "", hintedVerbError(ErrVerbForbidden, "attach-task with "+param+" is refused: taking a pane needs the read grant", &VerbHint{
				Param:  param,
				Detail: "Nothing was attached. Attach a file instead, or ask the person for the read grant.",
			})
		}
	}
	label := ""
	if w, ok := findWindowState(sess.GetState(), pp.Window); ok {
		label = windowLabelOf(w)
	}
	if shot {
		pic, verr := d.renderPaneShot(cs, sess, pp.Window, paneShotOptions{format: "png", scrollback: pp.Scrollback, lines: pp.Lines})
		if verr != nil {
			return nil, "", verr
		}
		return pic.data, "screenshot " + cmpOrString(label, shortWindowID(pp.Window)) + ".png", nil
	}
	_, pty, verr := d.resolveWindowPTY(sess, pp.Window)
	if verr != nil {
		return nil, "", verr
	}
	content, _ := pty.CaptureContentMeta(pp.Scrollback, false, true)
	lines := pp.Lines
	if lines <= 0 {
		lines = attachCaptureLines
	}
	content = sliceCaptureLines(trimCapture(content), 0, 0, lines)
	return []byte(content), "capture " + cmpOrString(label, shortWindowID(pp.Window)) + ".txt", nil
}

// cmpOrPane is a when set, else b.
func cmpOrPane(a, b *attachPaneParams) *attachPaneParams {
	if a != nil {
		return a
	}
	return b
}

// cmpOrString is a when set, else b.
func cmpOrString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// tooLarge is the refusal of a file or a session over a limit.
func tooLarge(what string, size, limit int64, option string) *verbError {
	return hintedVerbError(ErrVerbAttachmentTooLarge, what+" is "+attachMiB(size)+", over the limit of "+attachMiB(limit), &VerbHint{
		Param:  "content",
		Detail: "Nothing was attached. The limit is [tasks] " + option + ". Attach a smaller file, or detach one first.",
	})
}

// attachMiB is n bytes as a person reads a size.
func attachMiB(n int64) string {
	if n < 1<<20 {
		return strconv.FormatInt((n+1023)>>10, 10) + " KiB"
	}
	return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MiB"
}

// attachItem is one attachment an attach-task call puts on a card, with its
// bytes for a file.
type attachItem struct {
	att  TaskAttachment
	data []byte
}

// attachFileParam is one file of attach-task's files.
type attachFileParam struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// decodeAttachContent decodes one file's base64 and holds it to the limit.
func decodeAttachContent(content string, lim attachLimits) ([]byte, *verbError) {
	if int64(base64.StdEncoding.DecodedLen(len(content))) > lim.maxBytes+2 {
		return nil, tooLarge("the file", int64(base64.StdEncoding.DecodedLen(len(content))), lim.maxBytes, "attachment_max_mib")
	}
	data, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return nil, invalidParam("content", "content is not base64")
	}
	if int64(len(data)) > lim.maxBytes {
		return nil, tooLarge("the file", int64(len(data)), lim.maxBytes, "attachment_max_mib")
	}
	return data, nil
}

// fileItem is the attachment of a file named name.
func fileItem(name string, data []byte, base TaskAttachment) (attachItem, *verbError) {
	it := attachItem{att: base, data: data}
	it.att.Kind, it.att.MediaType = classifyCardFile(name, data)
	it.att.Size = int64(len(data))
	if it.att.Name = attachName(name); it.att.Name == "" {
		return it, invalidParam("name", "name is required: the name the card shows the attachment by")
	}
	return it, nil
}

// linkName is a link's name when the caller gave none: "PR #N" for a pull
// request, else its host and path, short enough to read in a list.
func linkName(kind, raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	path := strings.TrimSuffix(u.Path, "/")
	if kind == AttachPR {
		if i := strings.LastIndexByte(path, '/'); i >= 0 {
			return "PR #" + path[i+1:]
		}
	}
	name := u.Host + path
	if r := []rune(name); len(r) > 60 {
		name = string(r[:59]) + "…"
	}
	return name
}

// attachReason is what a card's history says an attach put on it: the
// names, at most three, and how many more.
func attachReason(items []attachItem) string {
	var names []string
	for i, it := range items {
		if i == 3 {
			names = append(names, "and "+strconv.Itoa(len(items)-3)+" more")
			break
		}
		names = append(names, it.att.Name)
	}
	return strings.Join(names, ", ")
}

// verbAttachTask answers attach-task: files, a link, or a pane the daemon
// takes, put on a card in one change, which the card's history records as
// one entry. An attachment whose name and bytes the card holds already is
// left as it is, so a retry after a lost answer succeeds.
func (d *Daemon) verbAttachTask(cs *connState, params json.RawMessage) (any, *verbError) {
	var p attachTaskParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Host != "" {
		return d.forwardAttach(cs, p)
	}
	sess, verr := d.sessionForTaskID(cs, p.Session, p.ID)
	if verr != nil {
		return nil, verr
	}
	caller, verr := d.taskCallerFor(cs, "attach-task", sess, p.HumanNonce, p.remoteClaim)
	if verr != nil {
		return nil, verr
	}
	if (p.Screenshot != nil || p.Capture != nil) && p.RemoteWindow != "" {
		return nil, invalidParam("screenshot", "a pane on another machine sends what it took as content")
	}
	lim := d.attachLimits()
	base := TaskAttachment{Note: invisible.Strip(printableClaim(p.Note, attachMaxNote)), By: caller.by}
	sources := 0
	for _, set := range []bool{p.Link != "", p.Content != "", p.Screenshot != nil, p.Capture != nil, len(p.Files) > 0} {
		if set {
			sources++
		}
	}
	var items []attachItem
	switch {
	case sources > 1:
		return nil, invalidParam("content", "pass one of content, files, link, screenshot and capture")
	case len(p.Files) > 0:
		if p.Name != "" {
			return nil, invalidParam("name", "files name each file; leave name out")
		}
		if len(p.Files) > lim.perCard {
			return nil, hintedVerbError(ErrVerbAttachmentLimit, strconv.Itoa(len(p.Files))+" files is more than a card holds", &VerbHint{
				Param:  "files",
				Detail: "Nothing was attached. The limit is [tasks] attachments_per_card.",
			})
		}
		seen := map[string]bool{}
		for _, f := range p.Files {
			data, verr := decodeAttachContent(f.Content, lim)
			if verr != nil {
				return nil, verr
			}
			it, verr := fileItem(f.Name, data, base)
			if verr != nil {
				return nil, verr
			}
			if seen[it.att.Name] {
				return nil, invalidParam("files", "two files are called "+echoName(it.att.Name)+"; give each its own name")
			}
			seen[it.att.Name] = true
			items = append(items, it)
		}
	case p.Screenshot != nil || p.Capture != nil:
		// Refused before the pane is taken, so a pane that may not change
		// the card costs no picture.
		st := sess.GetState()
		if i := taskIndex(st.Tasks, p.ID); i < 0 {
			return nil, noTask(sess, p.ID)
		} else if verr := caller.mayAttach("attach-task", &st.Tasks[i]); verr != nil {
			return nil, verr
		}
		taken, name, verr := d.takeFromPane(cs, caller, sess, cmpOrPane(p.Screenshot, p.Capture), p.Screenshot != nil)
		if verr != nil {
			return nil, verr
		}
		if int64(len(taken)) > lim.maxBytes {
			return nil, tooLarge("what the pane gave", int64(len(taken)), lim.maxBytes, "attachment_max_mib")
		}
		it, verr := fileItem(cmpOrString(p.Name, name), taken, base)
		if verr != nil {
			return nil, verr
		}
		items = append(items, it)
	case p.Link != "":
		kind, verr := linkAttachment(p.Link)
		if verr != nil {
			return nil, verr
		}
		it := attachItem{att: base}
		it.att.Kind, it.att.URL = kind, p.Link
		if it.att.Name = attachName(cmpOrString(p.Name, linkName(kind, p.Link))); it.att.Name == "" {
			return nil, invalidParam("name", "name is required: the name the card shows the attachment by")
		}
		items = append(items, it)
	case p.Content != "":
		data, verr := decodeAttachContent(p.Content, lim)
		if verr != nil {
			return nil, verr
		}
		it, verr := fileItem(p.Name, data, base)
		if verr != nil {
			return nil, verr
		}
		items = append(items, it)
	default:
		return nil, invalidParam("content", "attach-task needs content (a file, base64 encoded), files, link, screenshot or capture")
	}
	// Deferred first, so it runs last: a sweep starts after every hold
	// below is dropped, and so sees the files no card names.
	var sweep []string
	defer func() {
		if len(sweep) > 0 {
			d.sweepAttachments(sweep...)
		}
	}()
	var held []string
	defer func() {
		// The card names the files now, or the attach was refused and they
		// may be no card's.
		for _, h := range held {
			d.attachSweep.drop(h)
		}
	}()
	for i := range items {
		it := &items[i]
		if it.data == nil {
			continue
		}
		it.att.Hash = attachHash(it.data)
		d.attachSweep.hold(it.att.Hash)
		held = append(held, it.att.Hash)
		if err := storeAttachment(it.att.Hash, it.data); err != nil {
			sweep = append(sweep, held...)
			return nil, newVerbError(ErrVerbInternal, "could not store the attachment: "+err.Error())
		}
	}
	var olds, same []string
	var added []attachItem
	row, action, spec, verr := d.changeTask(sess, p.ID, func(st *SessionState, tk *TaskState, now int64) (string, *verbError) {
		olds, same, added = nil, nil, nil
		if verr := caller.mayAttach("attach-task", tk); verr != nil {
			return "", verr
		}
		fresh := 0
		var newBytes int64
		for _, it := range items {
			i := attachIndex(tk, it.att.Name)
			if i >= 0 {
				had := tk.Attachments[i]
				if had.Hash == it.att.Hash && had.URL == it.att.URL {
					same = append(same, it.att.Name)
					continue
				}
				if !p.Replace {
					return "", hintedVerbError(ErrVerbInvalidParams, "task card "+tk.ID+" has another attachment called "+echoName(it.att.Name)+" already", &VerbHint{
						Param:  "name",
						Detail: "Nothing was attached. Pass replace (tuios task attach --replace) to put this one in its place, or give it another name (--name).",
					})
				}
				olds = append(olds, had.Hash)
			} else {
				fresh++
			}
			if it.att.Hash != "" && !slices.ContainsFunc(st.Tasks, func(t TaskState) bool {
				return slices.ContainsFunc(t.Attachments, func(a TaskAttachment) bool { return a.Hash == it.att.Hash })
			}) {
				newBytes += it.att.Size
			}
			added = append(added, it)
		}
		if n := len(tk.Attachments) + fresh; n > lim.perCard {
			return "", hintedVerbError(ErrVerbAttachmentLimit, "task card "+tk.ID+" would hold "+strconv.Itoa(n)+" attachments, over the limit of "+strconv.Itoa(lim.perCard), &VerbHint{
				Command: "tuios task detach " + tk.ID + " NAME",
				Detail:  "Nothing was attached. The limit is [tasks] attachments_per_card. Detach one first.",
			})
		}
		if used := sessionAttachBytes(st); newBytes > 0 && used+newBytes > lim.sessionBytes {
			return "", tooLarge("the session's attachments with these", used+newBytes, lim.sessionBytes, "attachment_session_mib")
		}
		if len(added) == 0 {
			return "", nil
		}
		list := make([]TaskAttachment, len(added))
		for i := range added {
			added[i].att.At = now
			list[i] = added[i].att
		}
		action, _, _ := sess.moveCard(tk, cardMove{kind: moveAttach, by: caller.by, reason: attachReason(added), attachments: list}, now)
		return action, nil
	}, func(action string) taskEventSpec {
		if action == "" {
			return taskEventSpec{}
		}
		return taskEventSpec{EventTaskChanged, action, "", attachReason(added)}
	})
	if verr != nil {
		for _, it := range items {
			if it.att.Hash != "" {
				sweep = append(sweep, it.att.Hash)
			}
		}
		return nil, verr
	}
	for _, h := range olds {
		if !slices.ContainsFunc(items, func(it attachItem) bool { return it.att.Hash == h }) {
			sweep = append(sweep, h)
		}
	}
	if action != "" {
		LogBasic("Attached %s to task %s in session %s", attachReason(added), p.ID, sess.Name())
		d.afterTaskSpec(sess, spec, row)
	}
	out := make([]TaskAttachment, len(items))
	for i, it := range items {
		out[i] = it.att
	}
	res := map[string]any{"type": "task_attached", "attachment": out[0], "attachments": out, "unchanged": cmpOrEmpty(same), "task": row}
	return res, nil
}

// cmpOrEmpty is s, or an empty list for nil, so a caller reads a list.
func cmpOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// detachTaskParams are what detach-task and get-task-attachment take.
type detachTaskParams struct {
	Session    string `json:"session"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	HumanNonce string `json:"human_nonce"`
	remoteClaim
}

// noAttachment is the refusal for a name the card has no attachment by.
func noAttachment(id, name string) *verbError {
	return hintedVerbError(ErrVerbNoAttachment, "task card "+id+" has no attachment called "+echoName(name), &VerbHint{
		Param:   "name",
		Command: "tuios task attachments " + id,
		Detail:  "Nothing changed. List the card's attachments for their names.",
	})
}

// verbDetachTask answers detach-task: the attachment leaves the card, and
// its file goes when no card names it.
func (d *Daemon) verbDetachTask(cs *connState, params json.RawMessage) (any, *verbError) {
	var p detachTaskParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.sessionForTaskID(cs, p.Session, p.ID)
	if verr != nil {
		return nil, verr
	}
	caller, verr := d.taskCallerFor(cs, "detach-task", sess, p.HumanNonce, p.remoteClaim)
	if verr != nil {
		return nil, verr
	}
	var hash string
	row, action, spec, verr := d.changeTask(sess, p.ID, func(_ *SessionState, tk *TaskState, now int64) (string, *verbError) {
		if verr := caller.mayAttach("detach-task", tk); verr != nil {
			return "", verr
		}
		i := attachIndex(tk, p.Name)
		if i < 0 {
			return "", noAttachment(tk.ID, p.Name)
		}
		hash = tk.Attachments[i].Hash
		action, _, _ := sess.moveCard(tk, cardMove{kind: moveDetach, by: caller.by, reason: p.Name}, now)
		return action, nil
	}, func(action string) taskEventSpec {
		if action == "" {
			return taskEventSpec{}
		}
		return taskEventSpec{EventTaskChanged, action, "", p.Name}
	})
	if verr != nil {
		return nil, verr
	}
	if hash != "" {
		d.sweepAttachments(hash)
	}
	if action != "" {
		d.afterTaskSpec(sess, spec, row)
	}
	return row.typed("task_changed"), nil
}

// verbGetTaskAttachment answers get-task-attachment: one attachment and, for
// a file, its bytes in base64.
func (d *Daemon) verbGetTaskAttachment(cs *connState, params json.RawMessage) (any, *verbError) {
	var p detachTaskParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.sessionForTaskID(cs, p.Session, p.ID)
	if verr != nil {
		return nil, verr
	}
	st := sess.GetState()
	ti := taskIndex(st.Tasks, p.ID)
	if ti < 0 {
		return nil, noTask(sess, p.ID)
	}
	tk := &st.Tasks[ti]
	i := attachIndex(tk, p.Name)
	if i < 0 {
		return nil, noAttachment(tk.ID, p.Name)
	}
	att := tk.Attachments[i]
	out := map[string]any{"type": "task_attachment", "attachment": att}
	if att.Kind == AttachPR {
		pr, err := d.prLinks.read(d.ctx, att.URL)
		if err != nil {
			out["pr_error"] = err.Error()
		} else {
			out["pr"] = pr
		}
	}
	if att.Hash != "" {
		data, err := readAttachment(att.Hash)
		if err != nil {
			return nil, newVerbError(ErrVerbInternal, "could not read the attachment's file: "+err.Error())
		}
		out["content"] = base64.StdEncoding.EncodeToString(data)
	}
	return out, nil
}

// attachSweeper removes stored files no card names. One sweep runs at a
// time, and pending counts the attaches that are writing a file or putting
// it on a card, which a sweep leaves alone.
type attachSweeper struct {
	mu      sync.Mutex
	pending map[string]int
}

// hold marks hash as being attached. It waits for a sweep under way, so the
// file is written after the sweep, never removed under the attach.
func (s *attachSweeper) hold(hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = map[string]int{}
	}
	s.pending[hash]++
}

// drop ends a hold.
func (s *attachSweeper) drop(hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending[hash]--; s.pending[hash] <= 0 {
		delete(s.pending, hash)
	}
}

// sweepAttachments removes the stored files of hashes that no card names,
// off the caller's goroutine.
func (d *Daemon) sweepAttachments(hashes ...string) {
	go d.sweepAttachmentsNow(hashes)
}

// sweepAttachmentsNow removes the stored files of hashes, or with none every
// stored file, that no card of a running or a saved session names and no
// attach holds. A saved session it cannot read keeps every file: it may name
// any of them.
func (d *Daemon) sweepAttachmentsNow(hashes []string) {
	d.attachSweep.mu.Lock()
	defer d.attachSweep.mu.Unlock()
	if len(hashes) == 0 {
		hashes = storedAttachments()
		if len(hashes) == 0 {
			return
		}
	}
	used, ok := d.attachmentsInUse()
	if !ok {
		return
	}
	for _, h := range hashes {
		if used[h] || d.attachSweep.pending[h] > 0 {
			continue
		}
		path, err := attachmentPath(h)
		if err != nil {
			continue
		}
		if err := os.Remove(path); err == nil {
			_ = os.Remove(filepath.Dir(path))
		}
	}
}

// storedAttachments is the hash of every stored file.
func storedAttachments() []string {
	var out []string
	_ = filepath.WalkDir(attachmentDir(), func(_ string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() && attachHashRE.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
		return nil
	})
	return out
}

// attachmentsInUse is every hash a card names, in the running sessions and
// in the saved ones. ok is false when a saved session could not be read.
func (d *Daemon) attachmentsInUse() (map[string]bool, bool) {
	used := map[string]bool{}
	note := func(tasks []TaskState) {
		for _, tk := range tasks {
			for _, a := range tk.Attachments {
				if a.Hash != "" {
					used[a.Hash] = true
				}
			}
		}
	}
	// A running session's saved file may be older than its state, so its
	// state is read and its file is not.
	running := map[string]bool{}
	if d.manager != nil {
		for _, s := range d.manager.AllSessions() {
			running[s.Name()] = true
			if st := s.GetState(); st != nil {
				note(st.Tasks)
			}
		}
	}
	names, err := ListResurrectableSessions()
	if err != nil {
		return nil, false
	}
	for _, name := range names {
		if running[name] {
			continue
		}
		path, err := getResurrectionPath(name)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(path) // #nosec G304 -- a saved session file of this daemon
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		var saved struct {
			Tasks []TaskState `json:"tasks"`
		}
		if err != nil || jsonx.Unmarshal(data, &saved) != nil {
			return nil, false
		}
		note(saved.Tasks)
	}
	return used, true
}

// attachForwardMax bounds a file passed on to another machine: a linked
// daemon reads at most 4 MiB in one answer line, and the call holds the
// link's one control stream while it crosses.
const attachForwardMax = 3 << 20

// forwardAttach passes an attach on to host, the machine that owns the card,
// for the calling pane, which holds the card there. A picture or a text of a
// pane is taken here, where the pane is, and crosses as content.
func (d *Daemon) forwardAttach(cs *connState, p attachTaskParams) (any, *verbError) {
	if cs != nil && cs.viaLink {
		return nil, invalidParam("host", "a call that came over the link is not passed on again")
	}
	pa := d.paneAuthority(cs)
	if pa == nil || !pa.inPane() || pa.hosted || pa.window == unplacedWindow {
		return nil, hintedVerbError(ErrVerbInvalidParams, "attach-task with host is for a pane: the pane on this machine that holds the card", &VerbHint{
			Param:  "host",
			Detail: "Nothing changed. Run it in the agent's pane, or let the person attach on " + echoName(p.Host) + ".",
		})
	}
	if p.Session == "" {
		return nil, invalidParam("session", "attach-task with host names the card's session on "+echoName(p.Host)+": list-tasks with project and hosts gives it")
	}
	if d.federation == nil {
		return nil, invalidParam("host", "host "+echoName(p.Host)+" is not a linked machine: this daemon has no [hosts]")
	}
	params := map[string]any{"session": p.Session, "id": p.ID, "note": p.Note, "replace": p.Replace, "remote_window": pa.window}
	name := p.Name
	switch {
	case p.Screenshot != nil || p.Capture != nil:
		own := d.manager.GetSession(pa.session)
		if own == nil {
			return nil, newVerbError(ErrVerbInternal, "the calling pane's session is gone")
		}
		data, taken, verr := d.takeFromPane(cs, taskCaller{by: pa.window, window: pa.window}, own, cmpOrPane(p.Screenshot, p.Capture), p.Screenshot != nil)
		if verr != nil {
			return nil, verr
		}
		name = cmpOrString(name, taken)
		params["content"] = base64.StdEncoding.EncodeToString(data)
	case p.Link != "":
		params["link"] = p.Link
	case len(p.Files) > 0:
		params["files"] = p.Files
	default:
		params["content"] = p.Content
	}
	crossing, _ := params["content"].(string)
	size := base64.StdEncoding.DecodedLen(len(crossing))
	for _, f := range p.Files {
		size += base64.StdEncoding.DecodedLen(len(f.Content))
	}
	if size > attachForwardMax+2 {
		return nil, tooLarge("what goes to another machine", int64(size), attachForwardMax, "attachment_max_mib (and 3 MiB over a link)")
	}
	if name != "" {
		params["name"] = name
	}
	ctx, cancel := context.WithTimeout(d.ctx, federationVerbBudget)
	defer cancel()
	raw, err := d.federation.Call(ctx, p.Host, "attach-task", params)
	if err != nil {
		return nil, federationTaskErr("attach-task", p.Host, err)
	}
	var res struct {
		Attachment  TaskAttachment   `json:"attachment"`
		Attachments []TaskAttachment `json:"attachments"`
		Unchanged   []string         `json:"unchanged"`
		Task        TaskRow          `json:"task"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return nil, newVerbError(ErrVerbInternal, "read "+p.Host+"'s answer: "+err.Error())
	}
	res.Task.Host = p.Host
	if res.Attachments == nil {
		// A daemon from before files answers with one attachment.
		res.Attachments = []TaskAttachment{res.Attachment}
	}
	return map[string]any{"type": "task_attached", "attachment": res.Attachment, "attachments": res.Attachments,
		"unchanged": cmpOrEmpty(res.Unchanged), "task": res.Task}, nil
}

// prLinkFresh is how long a pull request link's state is kept before gh is
// asked again.
const prLinkFresh = time.Minute

// prLinkCache keeps the state of the pull request links the board shows, so
// a preview that comes back to a card asks gh at most once a minute. Only a
// get-task-attachment of a pr link reads it; nothing polls.
type prLinkCache struct {
	mu sync.Mutex
	m  map[string]prLinkEntry
}

// prLinkEntry is one link's last answer.
type prLinkEntry struct {
	pr  ghpr.PR
	err error
	at  time.Time
}

// prLinkMax bounds the links kept: the oldest answer goes first.
const prLinkMax = 256

// read is the state of the pull request at url, from gh when the kept answer
// is older than prLinkFresh. gh runs outside the lock, so a slow answer for
// one link holds no other.
func (c *prLinkCache) read(ctx context.Context, url string) (ghpr.PR, error) {
	c.mu.Lock()
	if e, ok := c.m[url]; ok && time.Since(e.at) < prLinkFresh {
		c.mu.Unlock()
		return e.pr, e.err
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pr, err := ghpr.ViewURL(ctx, url)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]prLinkEntry{}
	}
	c.m[url] = prLinkEntry{pr: pr, err: err, at: time.Now()}
	for len(c.m) > prLinkMax {
		oldest, at := "", time.Time{}
		for u, e := range c.m {
			if oldest == "" || e.at.Before(at) {
				oldest, at = u, e.at
			}
		}
		delete(c.m, oldest)
	}
	return pr, err
}
