package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/invisible"
)

// Edits answered from the Inbox with their body.
//
// An Edit, MultiEdit or Write is not decided by its path: what it writes is
// the call. So the Inbox only offers one with its whole body, the path and
// every hunk, which a client shows as a diff. The daemon holds the body,
// serves it from get-approval, and refuses an allow that does not name the
// body's digest (EditBody.Digest), so an allow made from the line alone, or
// from another body, answers nothing.
//
// An edit is kept in the pane, as before, when its body is over MaxEdit, has
// a character that would make it read as something else (anything but a
// printable character, a tab or a newline), or carries a field the body does
// not show. NotebookEdit is always kept in the pane. "Always" is never offered
// for an edit: it would allow later edits nobody read.

// MaxEdit bounds an edit's body, in bytes: the path and every old and new
// string together. It is the plan bound.
const MaxEdit = MaxPlan

// MaxEditHunks bounds a MultiEdit's hunks. Each one is a head and lines on
// the card, and a person does not read thousands of them before an allow.
const MaxEditHunks = 256

// EditHunk is one replacement in an edit. A Write is one hunk with an empty
// Old and the whole new content as New.
type EditHunk struct {
	Old        string `json:"old"`
	New        string `json:"new"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

// EditBody is an edit as the Inbox shows it: the tool, the path as the
// harness sent it, and the hunks in order.
type EditBody struct {
	Tool  string     `json:"tool"`
	Path  string     `json:"path"`
	Hunks []EditHunk `json:"hunks"`
}

// EditTools are the tools whose body the Inbox can show.
var EditTools = []string{"Edit", "MultiEdit", "Write"}

// editDigestTag starts the canonical encoding. A change to the encoding
// changes the tag.
const editDigestTag = "tuios-edit-v1\n"

// Canonical is the encoding edit_sha is the digest of: editDigestTag, then
// each field as a netstring, "<decimal byte length>:<bytes>,", in this order:
// the tool, the path, the number of hunks in decimal, then for each hunk its
// old string, its new string, and replace_all as "1" or "0". The lengths make
// every field's bounds part of the digest.
func (e EditBody) Canonical() []byte {
	var b strings.Builder
	b.WriteString(editDigestTag)
	field := func(s string) {
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteByte(':')
		b.WriteString(s)
		b.WriteByte(',')
	}
	field(e.Tool)
	field(e.Path)
	field(strconv.Itoa(len(e.Hunks)))
	for _, h := range e.Hunks {
		field(h.Old)
		field(h.New)
		if h.ReplaceAll {
			field("1")
		} else {
			field("0")
		}
	}
	return []byte(b.String())
}

// Digest is edit_sha: the SHA-256 of Canonical, in lower-case hex.
func (e EditBody) Digest() string {
	sum := sha256.Sum256(e.Canonical())
	return hex.EncodeToString(sum[:])
}

// Size is the body's length in bytes, what MaxEdit bounds.
func (e EditBody) Size() int {
	n := len(e.Path)
	for _, h := range e.Hunks {
		n += len(h.Old) + len(h.New)
	}
	return n
}

// Lines is how many lines the hunks hold, old and new together, each string
// counted without a final newline.
func (e EditBody) Lines() int {
	n := 0
	for _, h := range e.Hunks {
		n += textLines(h.Old) + textLines(h.New)
	}
	return n
}

func textLines(s string) int {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// Showable reports whether the Inbox can show the body exactly as it is: an
// edit tool, a printable absolute path, one to MaxEditHunks hunks, none of a
// MultiEdit's with both sides empty, a Write as one hunk with no old text,
// the whole within MaxEdit, and every string valid UTF-8 of printable
// characters, tabs and newlines only. A relative path is refused: the digest
// does not hold the folder it is read from, so the same body would name two
// files in two panes. The hook checks it before it
// offers an edit and the daemon again before it holds one.
func (e EditBody) Showable() bool {
	if !slices.Contains(EditTools, e.Tool) || !filepath.IsAbs(e.Path) || !invisible.Printable(e.Path) {
		return false
	}
	if len(e.Hunks) == 0 || len(e.Hunks) > MaxEditHunks || (e.Tool != "MultiEdit" && len(e.Hunks) != 1) || e.Size() > MaxEdit {
		return false
	}
	if e.Tool == "Write" && (e.Hunks[0].Old != "" || e.Hunks[0].ReplaceAll) {
		return false
	}
	for _, h := range e.Hunks {
		if !editTextShown(h.Old) || !editTextShown(h.New) || (e.Tool == "MultiEdit" && h.Old == "" && h.New == "") {
			return false
		}
	}
	return true
}

// editTextShown reports whether a diff can show s as it is: printable text
// with tabs and newlines (invisible.PrintableText: no format character, no
// character that draws as a blank, no long stack of combining marks). U+FFFD
// is refused too: a JSON decoder puts it where the harness sent bytes that
// are not UTF-8 or a lone surrogate, so the diff would show a character the
// file does not get.
func editTextShown(s string) bool {
	return invisible.PrintableText(s) && !strings.ContainsRune(s, utf8.RuneError)
}

// claudeEditApproval is the Approval for a Claude Code Edit, MultiEdit or
// Write, or nil for one the Inbox keeps in the pane. The line is the path,
// shown whole, and the body rides beside it.
func claudeEditApproval(p fields) *Approval {
	tool := p.str("tool_name")
	input := p.obj("tool_input")
	body, ok := claudeEditBody(tool, input)
	if !ok || !body.Showable() {
		return nil
	}
	if ToolSummary(tool, input) != tool+": "+body.Path {
		return nil
	}
	return &Approval{
		Kind:        KindApproval,
		Options:     []string{DecisionOnce, DecisionDeny},
		Tool:        tool,
		Target:      body.Path,
		Cwd:         p.str("cwd"),
		Edit:        &body,
		DenyMessage: true,
	}
}

// claudeEditBody reads tool_input into a body. Every field must be one the
// body shows, with the type the tool takes; anything else keeps the edit in
// the pane.
func claudeEditBody(tool string, input fields) (EditBody, bool) {
	path, ok := input["file_path"].(string)
	if !ok {
		return EditBody{}, false
	}
	body := EditBody{Tool: tool, Path: path}
	switch tool {
	case "Edit":
		h, ok := claudeHunk(map[string]any(input), "file_path")
		if !ok {
			return EditBody{}, false
		}
		body.Hunks = []EditHunk{h}
	case "MultiEdit":
		if !onlyKeys(input, "file_path", "edits") {
			return EditBody{}, false
		}
		list, ok := input["edits"].([]any)
		if !ok {
			return EditBody{}, false
		}
		for _, raw := range list {
			m, ok := raw.(map[string]any)
			if !ok {
				return EditBody{}, false
			}
			h, ok := claudeHunk(m)
			if !ok {
				return EditBody{}, false
			}
			body.Hunks = append(body.Hunks, h)
		}
	case "Write":
		content, ok := input["content"].(string)
		if !ok || !onlyKeys(input, "file_path", "content") {
			return EditBody{}, false
		}
		body.Hunks = []EditHunk{{New: content}}
	default:
		return EditBody{}, false
	}
	return body, true
}

// claudeHunk reads one old_string, new_string and replace_all. extra are the
// other keys m may carry.
func claudeHunk(m map[string]any, extra ...string) (EditHunk, bool) {
	if !onlyKeys(m, append([]string{"old_string", "new_string", "replace_all"}, extra...)...) {
		return EditHunk{}, false
	}
	old, okOld := m["old_string"].(string)
	repl, okNew := m["new_string"].(string)
	if !okOld || !okNew {
		return EditHunk{}, false
	}
	h := EditHunk{Old: old, New: repl}
	if v, set := m["replace_all"]; set {
		all, ok := v.(bool)
		if !ok {
			return EditHunk{}, false
		}
		h.ReplaceAll = all
	}
	return h, true
}
