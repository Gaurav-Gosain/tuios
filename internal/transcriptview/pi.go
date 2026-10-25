package transcriptview

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/transcript"
)

// FormatPi is Pi's v2/v3 session tree. Checked against Pi 1.3.0's
// docs/session-format.md, docs/message-types.md and core/session-manager.ts.
// Unlike a linear log, sibling entries are different conversations.
const FormatPi = "pi-jsonl"

// ErrPiTree reports invalid or unavailable ancestry, without naming any content.
var ErrPiTree = errors.New("transcriptview: Pi branch is unavailable or invalid")

var errPiPending = errors.New("transcriptview: selected Pi entry is not complete yet")

// piNodesMax bounds the metadata index as well as the snapshot's byte bound.
const piNodesMax = 100000

// piRead reads the conversation on the branch that ends at the selected entry
// of a Pi session file, or at its last complete entry when none is selected.
// The projection of that branch is kept for a short time like a document's
// records (docCache), so paging through a long session, or a burst of events,
// does not read and parse the whole file again. The key is the file's size and
// modification time and the selected entry, so an appended file, or another
// branch of it, is read again.
func piRead(path string, opts Options) (Page, error) {
	f, err := transcript.OpenRegular(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Page{}, ErrNoFile
		}
		return Page{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return Page{}, err
	}
	if info.Size() > scanMax {
		return Page{}, ErrTooLarge
	}
	key := docKey{path: path, size: info.Size(), mod: info.ModTime()}
	if opts.Leaf != nil {
		key.leaf, key.hasLeaf = *opts.Leaf, true
	}
	var lines []byte
	var pending bool
	if e, ok := docs.get(key); ok {
		defer docs.release(e)
		lines = e.lines
	} else {
		data, err := io.ReadAll(io.LimitReader(f, scanMax+1))
		defer clear(data)
		if err != nil {
			return Page{}, err
		}
		if len(data) > scanMax {
			return Page{}, ErrTooLarge
		}
		lines, err = piProject(data, opts.Leaf)
		pending = errors.Is(err, errPiPending)
		if err != nil && !pending {
			clear(lines)
			return Page{}, err
		}
		// An entry the hook named before its line is written is a moment, so the
		// empty view it gives is not kept.
		if pending {
			defer clear(lines)
		} else if e := docs.put(key, lines); e != nil {
			defer docs.release(e)
		} else {
			defer clear(lines)
		}
	}
	id, err := fileID(bytes.NewReader(lines), path, int64(len(lines)))
	if err != nil {
		return Page{}, err
	}
	r := &reader{src: bytes.NewReader(lines), size: int64(len(lines)), opts: opts, fm: formats[FormatPi], lcsLeft: LCSCellsPerCall}
	defer r.zero()
	var page Page
	read := func(c string, before bool) (Page, error) {
		// Hash the selected prefix, not just the file header: switching to a
		// sibling can leave a syntactically valid byte offset in the same file.
		parts := strings.SplitN(c, ":", 2)
		if len(parts) == 2 {
			if at, ok := parseCursor(parts[1], id); ok && at.off <= r.size && r.atBoundary(at.off) && parts[0] == piPrefix(lines, at) {
				if before {
					return r.back(at, id, false)
				}
				return r.forward(at, id)
			}
		}
		p, err := r.newest(id)
		p.Reset = true
		return p, err
	}
	switch {
	case opts.After != "":
		page, err = read(opts.After, false)
	case opts.Before != "":
		page, err = read(opts.Before, true)
	default:
		page, err = r.newest(id)
	}
	wrap := func(c string) string {
		if at, ok := parseCursor(c, id); ok {
			return piPrefix(lines, at) + ":" + c
		}
		return ""
	}
	page.Cursor, page.Older = wrap(page.Cursor), wrap(page.Older)
	page.Reset = page.Reset || pending
	return page, err
}

// piPrefix identifies the ancestry already returned by a cursor. A descendant
// append keeps it; a sibling or rewind does not. A partial-record cursor also
// names that record, since its earlier parts may already have been displayed.
func piPrefix(lines []byte, at cursorPos) string {
	end := min(at.off, int64(len(lines)))
	if at.sub > 0 {
		if n := bytes.IndexByte(lines[end:], '\n'); n >= 0 {
			end += int64(n + 1)
		}
	}
	sum := sha256.Sum256(lines[:end])
	return hex.EncodeToString(sum[:8])
}

// piProject returns the header and the records of the branch that ends at the
// last complete entry. Sibling entries are different conversations, so only
// that ancestry is kept. A record longer than lineMax keeps its place in the
// ancestry but is left out of the view, as the other readers skip such a line,
// so one huge tool result does not hide the whole conversation.
func piProject(data []byte, leaf *string) ([]byte, error) {
	type node struct {
		parent     string
		start, end int
		skip       bool
	}
	nodes := make(map[string]node)
	var header []byte
	last := ""
	for start := 0; start < len(data); {
		n := bytes.IndexByte(data[start:], '\n')
		if n < 0 {
			break // Pi may be in the middle of appending this record.
		}
		end := start + n + 1
		line := data[start : end-1]
		var rec struct {
			Type     string  `json:"type"`
			ID       string  `json:"id"`
			ParentID *string `json:"parentId"`
			Version  int     `json:"version"`
		}
		if json.Unmarshal(line, &rec) == nil {
			if rec.Type == "session" {
				if header != nil || len(line) > lineMax || (rec.Version != 2 && rec.Version != 3) {
					return nil, ErrPiTree
				}
				header = data[start:end]
			} else if rec.ID != "" {
				if len(rec.ID) > 128 || (rec.ParentID != nil && len(*rec.ParentID) > 128) || len(nodes) >= piNodesMax {
					return nil, ErrTooLarge
				}
				if _, exists := nodes[rec.ID]; exists {
					return nil, ErrPiTree
				}
				parent := ""
				if rec.ParentID != nil {
					parent = *rec.ParentID
				}
				nodes[rec.ID] = node{parent: parent, start: start, end: end, skip: len(line) > lineMax}
				last = rec.ID
			}
		}
		start = end
	}
	if header == nil {
		return nil, ErrPiTree
	}
	if leaf != nil {
		last = *leaf
		if last != "" {
			if _, ok := nodes[last]; !ok {
				// The hook may name an entry before its append completes. Clear
				// the old branch, never substitute a sibling's history.
				return append([]byte(nil), header...), errPiPending
			}
		}
	}
	var chain []node
	seen := make(map[string]bool)
	for last != "" {
		if seen[last] {
			return nil, ErrPiTree
		}
		seen[last] = true
		n, ok := nodes[last]
		if !ok {
			return nil, ErrPiTree
		}
		chain = append(chain, n)
		last = n.parent
	}
	slices.Reverse(chain)
	out := append([]byte(nil), header...)
	for _, n := range chain {
		if !n.skip {
			out = append(out, data[n.start:n.end]...)
		}
	}
	return out, nil
}

func piLine(r *reader, line []byte, off int64) (decoded, bool) {
	rec, ok := parseObj(line)
	if !ok {
		return decoded{}, false
	}
	var out []Entry
	switch rec.str("type") {
	case "message":
		m := rec.obj("message")
		switch m.str("role") {
		case "user", "assistant":
			role := m.str("role")
			if text, ok := m["content"].(string); ok {
				if e, ok := textEntry(role, KindText, text); ok {
					out = append(out, e)
				}
			}
			// Adjacent blocks of one text kind are one entry. Secret masking runs
			// per entry, so a multi-line secret split across blocks would
			// otherwise lose its boundaries and reach the client unmasked.
			var run []string
			var runKind string
			flush := func() {
				if runKind == "" {
					return
				}
				if e, ok := textEntry(role, runKind, strings.Join(run, "\n")); ok {
					out = append(out, e)
				}
				run, runKind = nil, ""
			}
			add := func(kind, text string) {
				if runKind != kind {
					flush()
					runKind = kind
				}
				run = append(run, text)
			}
			for _, part := range m.list("content") {
				b := asObj(part)
				switch b.str("type") {
				case "text":
					add(KindText, b.str("text"))
				case "thinking":
					if role == RoleAssistant {
						add(KindThinking, b.str("thinking"))
					}
				case "image":
					flush()
					out = append(out, Entry{Role: role, Kind: KindText, raw: rawText("[image]")})
				case "toolCall":
					flush()
					if role == RoleAssistant {
						name, a, class := b.str("name"), args(b["arguments"]), classOther
						switch name {
						case "edit":
							class = classEdit
							a = jobj{"path": fileOf(a), "old_string": a.str("oldText"), "new_string": a.str("newText")}
						case "write":
							class = classWrite
						}
						out = append(out, callEntry(name, b.str("id"), class, a)...)
					}
				}
			}
			flush()
			if role == RoleAssistant && (m.str("stopReason") == "error" || m.str("stopReason") == "aborted") {
				text := m.str("errorMessage")
				if text == "" {
					text = "The turn stopped: " + m.str("stopReason")
				}
				if e, ok := textEntry(role, KindError, text); ok {
					out = append(out, e)
				}
			}
		case "toolResult":
			failed, _ := m["isError"].(bool)
			out = append(out, resultEntry(m.str("toolCallId"), blockText(m["content"]), failed))
		case "bashExecution":
			// Direct !/!! shell commands are history too, not model tool calls.
			// excludeFromContext affects inference, not what the person sees.
			id := rec.str("id") + "-bash"
			calls := callEntry("bash", id, classOther, jobj{"command": m.str("command")})
			code, hasCode := m["exitCode"].(float64)
			cancelled, _ := m["cancelled"].(bool)
			text := m.str("output")
			if cancelled {
				text = "Command cancelled\n" + text
			}
			result := resultEntry(id, text, cancelled || !hasCode || code != 0)
			result.raw.textCut = result.raw.textCut || m["truncated"] == true
			for i := range calls {
				calls[i].Role, calls[i].Status = RoleUser, result.Status
			}
			out = append(out, calls...)
			out = append(out, result)
		}
	case "compaction", "branch_summary":
		if e, ok := textEntry(RoleAssistant, KindText, rec.str("summary")); ok {
			out = append(out, e)
		}
	case "custom_message":
		if rec["display"] == true {
			if e, ok := textEntry(RoleAssistant, KindText, blockText(rec["content"])); ok {
				out = append(out, e)
			}
		}
	}
	for i := range out {
		out[i].ID, out[i].At = keyID(rec.str("id"), strconv.Itoa(i)), parseAt(rec.str("timestamp"))
	}
	return decoded{entries: out}, true
}
