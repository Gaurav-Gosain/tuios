package transcriptview

import (
	"bytes"
	"encoding/json"
	"errors"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/transcript"
)

// Format names. Each is the reader a harness manifest names in its
// [transcript] block, so a manifest picks its decoder by name and never
// describes a format itself.
const (
	// FormatClaude is Claude Code's JSONL (claude.go).
	FormatClaude = "claude-code-jsonl"
	// FormatCodex is a Codex rollout, ~/.codex/sessions/YYYY/MM/DD/
	// rollout-*.jsonl (codex.go).
	FormatCodex = "codex-rollout"
	// FormatGemini is a Gemini CLI chat: JSONL under ~/.gemini/tmp/<project>/
	// chats, or the one JSON document older versions rewrote (gemini.go).
	FormatGemini = "gemini-chat"
	// FormatQwen is a Qwen Code chat, ~/.qwen/projects/<cwd>/chats/<session>.
	// jsonl (qwen.go).
	FormatQwen = "qwen-chat"
)

// decoded is what one record gave.
type decoded struct {
	entries []Entry
	// key names the message the record wrote, for a format that writes a
	// message again, whole, each time it changes (Gemini). The window drops
	// what it holds of the same key, so a message shows once, as it is now.
	key string
	// rewind names a message the conversation was taken back to. The window
	// drops it and everything after it.
	rewind string
}

// format is one transcript format.
type format struct {
	// line decodes one record. It reports false for a record that does not
	// decode at all, which the page counts in Skipped. A record that decodes
	// and holds nothing to show returns true and no entries.
	line func(r *reader, line []byte, off int64) (decoded, bool)
	// doc, when set, turns a whole file into records, one per line, for a
	// harness that rewrites one JSON document. It returns nil for a file it
	// cannot read.
	doc func(data []byte) []byte
	// docExt limits doc to a file name with this ending, for a format whose
	// harness wrote a document once and writes lines now. Empty applies doc
	// to every file.
	docExt string
	// match reports whether the head of a file, its first headScan bytes,
	// says the agent ran in cwd. It is how a file found by search is
	// believed. Nil means a file of the format cannot be matched, and only a
	// path the harness reports is joined.
	match func(head []byte, cwd string) bool
}

// isDoc reports whether the file at path is read as a document.
func (f format) isDoc(path string) bool {
	return f.doc != nil && (f.docExt == "" || strings.HasSuffix(path, f.docExt))
}

var formats = map[string]format{
	FormatClaude: {line: claudeLine},
	FormatCodex:  {line: codexLine, match: codexMatch},
	FormatGemini: {line: geminiLine, doc: geminiDoc, docExt: ".json", match: geminiMatch},
	FormatQwen:   {line: qwenLine, match: qwenMatch},
}

// lookupFormat returns the format a name names, FormatClaude for "".
func lookupFormat(name string) (format, bool) {
	if name == "" {
		name = FormatClaude
	}
	f, ok := formats[name]
	return f, ok
}

// Known reports whether this build decodes the format a manifest names.
func Known(name string) bool {
	_, ok := formats[name]
	return ok
}

// IsDocument reports whether the file at path, in the named format, is one
// JSON document the harness rewrites whole rather than lines it appends. The
// daemon tells such a file grew by its size and time, since an offset into
// it means nothing.
func IsDocument(name, path string) bool {
	f, ok := lookupFormat(name)
	return ok && f.isDoc(path)
}

// headScan is how much of a file Matches reads.
const headScan = 64 << 10

// Matches reports whether the file at path, in the named format, says the
// agent that wrote it ran in cwd, from the file's first headScan bytes. It
// is false for a format that cannot tell, and for a file it cannot read.
// It returns nothing of the file.
func Matches(name, path, cwd string) bool {
	f, ok := lookupFormat(name)
	if !ok || f.match == nil || cwd == "" {
		return false
	}
	file, err := transcript.OpenRegular(path)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	head := make([]byte, headScan)
	defer clear(head)
	n, _ := file.ReadAt(head, 0)
	return f.match(head[:n], cwd)
}

// firstLine is the first complete line of head, or nil when none ends in it.
func firstLine(head []byte) []byte {
	i := bytes.IndexByte(head, '\n')
	if i < 0 {
		return nil
	}
	return head[:i]
}

// completeLines calls fn with each complete line of head until it returns
// true, and reports whether one did.
func completeLines(head []byte, fn func([]byte) bool) bool {
	for {
		i := bytes.IndexByte(head, '\n')
		if i < 0 {
			return false
		}
		if fn(head[:i]) {
			return true
		}
		head = head[i+1:]
	}
}

// dropKeyed removes from out the entries of message key, and those of
// message rewind and every entry after the first of them.
func dropKeyed(out []Entry, key, rewind string) []Entry {
	if rewind != "" {
		for i := range out {
			if out[i].key == rewind {
				clear(out[i:])
				out = out[:i]
				break
			}
		}
	}
	if key == "" {
		return out
	}
	n := 0
	for i := range out {
		if out[i].key != key {
			out[n] = out[i]
			n++
		}
	}
	clear(out[n:])
	return out[:n]
}

// offID is the id of the i-th entry of the record at offset off.
func offID(off int64, i int) string {
	return strconv.FormatInt(off, 36) + "-" + strconv.Itoa(i)
}

// keyID is the id of one part of a keyed message. A message written again
// gives its parts the same ids, so the thread replaces them. The key and
// the part come from the file, so they are hashed rather than sent.
func keyID(key, part string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(part))
	return "k" + strconv.FormatUint(h.Sum64(), 36)
}

// toolIDMax bounds a tool call id sent as it is.
const toolIDMax = 96

// cleanToolID is a tool call's id as the page carries it: as written when it
// is short and plain, otherwise a hash of it. It only pairs a call with its
// result, so its spelling does not matter.
func cleanToolID(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= toolIDMax && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' || r == ':')
	}) < 0 {
		return s
	}
	return keyID("tool", s)
}

// parseAt is an RFC 3339 time in milliseconds, 0 when it is not one.
func parseAt(s string) int64 {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UnixMilli()
	}
	return 0
}

// unmarshalLoose decodes line into v, keeping what decodes when a field has
// an unexpected type.
func unmarshalLoose(line []byte, v any) error {
	err := json.Unmarshal(line, v)
	if _, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return nil
	}
	return err
}

// --- Entries every format makes.

// textEntry is a text, thinking or error entry. Empty text gives none.
func textEntry(role, kind, s string) (Entry, bool) {
	if strings.TrimSpace(s) == "" {
		return Entry{}, false
	}
	return Entry{Role: role, Kind: kind, raw: rawText(s)}, true
}

// resultEntry is a tool result: its status and the head of its text, cut
// before Clean as Claude's are.
func resultEntry(toolID, text string, failed bool) Entry {
	e := Entry{Role: RoleTool, Kind: KindToolResult, ToolID: cleanToolID(toolID), Status: StatusOK}
	if failed {
		e.Status = StatusError
	}
	text, cutLines := headLines(text, ResultLines)
	text, cutBytes := cutString(text, rawTextMax)
	e.raw = &rawEntry{text: text, textCut: cutLines || cutBytes}
	return e
}

// --- Tool calls.

// toolClass is what a call does, which says how it is drawn.
type toolClass int

const (
	classOther toolClass = iota
	// classEdit replaces old text with new text in a file.
	classEdit
	// classWrite writes a whole file.
	classWrite
	// classTodos sets the todo list.
	classTodos
	// classPlan proposes a plan.
	classPlan
	// classPatch applies a patch in the apply_patch format (Codex).
	classPatch
)

// toolArgs is every argument field a call's card shows, under the names the
// harnesses give them.
type toolArgs struct {
	Command     json.RawMessage `json:"command"`
	Cmd         json.RawMessage `json:"cmd"`
	Description string          `json:"description"`
	FilePath    string          `json:"file_path"`
	AbsPath     string          `json:"absolute_path"`
	Path        string          `json:"path"`
	DirPath     string          `json:"dir_path"`
	Pattern     string          `json:"pattern"`
	URL         string          `json:"url"`
	Query       string          `json:"query"`
	Prompt      string          `json:"prompt"`
	Objective   string          `json:"objective"`
	Skill       string          `json:"skill"`
	OldString   string          `json:"old_string"`
	NewString   string          `json:"new_string"`
	OldStr      string          `json:"old_str"`
	NewStr      string          `json:"new_str"`
	Content     string          `json:"content"`
	FileText    string          `json:"file_text"`
	Plan        json.RawMessage `json:"plan"`
	Todos       json.RawMessage `json:"todos"`
	Input       string          `json:"input"`
}

// decodeArgs decodes a call's arguments: an object, or a string holding one,
// as Codex writes them. What does not decode is left empty.
func decodeArgs(raw []byte) toolArgs {
	var a toolArgs
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return a
		}
		raw = []byte(s)
	}
	if len(raw) > 0 && raw[0] == '{' {
		_ = unmarshalLoose(raw, &a)
	}
	return a
}

// command is the command a shell call runs: a string, or an argv joined.
// Codex runs ["bash", "-lc", "script"], which shows as the script.
func (a toolArgs) command() string {
	for _, raw := range []json.RawMessage{a.Command, a.Cmd} {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		var argv []string
		if json.Unmarshal(raw, &argv) == nil && len(argv) > 0 {
			if len(argv) == 3 && (argv[1] == "-lc" || argv[1] == "-c") {
				return argv[2]
			}
			return strings.Join(argv, " ")
		}
	}
	return ""
}

// file is the file a call acts on.
func (a toolArgs) file() string {
	for _, s := range []string{a.FilePath, a.AbsPath, a.Path, a.DirPath} {
		if s != "" {
			return s
		}
	}
	return ""
}

// target is the one line that says what a call acts on.
func (a toolArgs) target() string {
	for _, s := range []string{a.file(), a.command(), a.Pattern, a.URL, a.Query, a.Skill, a.Description, a.Objective, a.Prompt} {
		if s != "" {
			return s
		}
	}
	return ""
}

// callEntry is a tool call as decoded: a tool call with its target and, for
// an edit, its diff; a todo list; or a plan.
func callEntry(name, id string, class toolClass, a toolArgs) []Entry {
	raw := &rawEntry{}
	raw.tool, _ = cutString(oneLine(name), 4*toolMax)
	e := Entry{Role: RoleAssistant, Kind: KindToolCall, ToolID: cleanToolID(id), raw: raw}
	switch class {
	case classTodos:
		e.Kind = KindTodos
		raw.todos = todoItems(a.Todos)
		if raw.todos == nil {
			raw.todos = todoItems(a.Plan)
		}
		return []Entry{e}
	case classPlan:
		plan := planText(a.Plan)
		if plan == "" {
			break
		}
		e.Kind = KindPlan
		raw.plan, raw.textCut = cutString(plan, rawTextMax)
		return []Entry{e}
	case classEdit:
		old, nw := a.OldString, a.NewString
		if old == "" && nw == "" {
			old, nw = a.OldStr, a.NewStr
		}
		raw.diff = &diffSrc{file: a.file(), pairs: [][2]string{{old, nw}}}
	case classWrite:
		text := a.Content
		if text == "" {
			text = a.FileText
		}
		raw.diff = &diffSrc{file: a.file(), pairs: [][2]string{{"", text}}}
	case classPatch:
		patch := a.Input
		if patch == "" {
			patch = a.command()
		}
		if out := patchEntries(name, e.ToolID, patch); len(out) > 0 {
			return out
		}
	}
	raw.target, _ = cutString(oneLine(a.target()), 4*targetMax)
	return []Entry{e}
}

// patchEntries is one tool call per file an apply_patch patch changes, each
// with its diff. They share the call's id, so each takes the result's status.
func patchEntries(name, id, patch string) []Entry {
	files := parseApplyPatch(patch)
	out := make([]Entry, 0, len(files))
	for _, f := range files {
		raw := &rawEntry{}
		raw.tool, _ = cutString(oneLine(name), 4*toolMax)
		raw.target, _ = cutString(oneLine(f.file), 4*targetMax)
		if len(f.pairs) > 0 {
			raw.diff = &diffSrc{file: f.file, pairs: f.pairs}
		}
		out = append(out, Entry{Role: RoleAssistant, Kind: KindToolCall, ToolID: id, raw: raw})
	}
	return out
}

// planText is a plan as text: a string, or Codex's list of steps.
func planText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var b strings.Builder
	for _, t := range todoItems(raw) {
		b.WriteString("- ")
		b.WriteString(t.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// todoItems decodes a todo list: objects with a text under any of the names
// the harnesses use and a status, or a Markdown checklist in one string.
func todoItems(raw json.RawMessage) []Todo {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil
		}
		return markdownTodos(s)
	}
	var items []struct {
		Content     string `json:"content"`
		Description string `json:"description"`
		Step        string `json:"step"`
		Title       string `json:"title"`
		Text        string `json:"text"`
		Status      string `json:"status"`
	}
	if unmarshalLoose(raw, &items) != nil {
		return nil
	}
	var out []Todo
	for _, it := range items[:min(len(items), todosMax)] {
		text := it.Content
		for _, s := range []string{it.Description, it.Step, it.Title, it.Text} {
			if text == "" {
				text = s
			}
		}
		text, _ = cutString(oneLine(text), 4*targetMax)
		status, _ := cutString(oneLine(it.Status), 4*toolMax)
		out = append(out, Todo{Text: text, Status: status})
	}
	return out
}

// markdownTodos reads "- [ ] item", "- [x] item" and "- [~] item" lines.
func markdownTodos(s string) []Todo {
	var out []Todo
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "-*0123456789. ")
		if len(line) < 4 || line[0] != '[' || line[2] != ']' {
			continue
		}
		status := "pending"
		switch line[1] {
		case 'x', 'X':
			status = "completed"
		case '~', '-', '>':
			status = "in_progress"
		}
		text, _ := cutString(strings.TrimSpace(line[3:]), 4*targetMax)
		out = append(out, Todo{Text: text, Status: status})
		if len(out) == todosMax {
			break
		}
	}
	return out
}

// --- Patches.

// filePatch is the change a patch makes to one file: hunks with line
// numbers (a unified diff), or old and new texts (an apply_patch patch).
type filePatch struct {
	file  string
	hunks []ccPatch
	pairs [][2]string
}

// patchFilesMax bounds the files one patch shows.
const patchFilesMax = 32

// parseUnified reads a unified diff, with or without a Markdown fence around
// it, into the changes of each file it names. A hunk is as long as its header
// says, so a removed line that starts with "--" is not read as a file name. A
// diff with no file name is given the name fallback.
func parseUnified(s, fallback string) []filePatch {
	var out []filePatch
	var hunk *ccPatch
	oldLeft, newLeft := 0, 0
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if hunk != nil && (oldLeft > 0 || newLeft > 0) {
			op := byte(' ')
			if line != "" {
				op = line[0]
			}
			switch op {
			case '+':
				newLeft--
			case '-':
				oldLeft--
			case ' ':
				oldLeft--
				newLeft--
			default:
				// "\ No newline at end of file", or a hunk shorter than
				// its header.
				if line == "" || line[0] != '\\' {
					hunk, oldLeft, newLeft = nil, 0, 0
				}
				continue
			}
			if len(hunk.Lines) < 4*DiffLinesMax {
				if line == "" {
					line = " "
				}
				hunk.Lines = append(hunk.Lines, line)
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "+++ "):
			if len(out) == patchFilesMax {
				return compactPatches(out)
			}
			out = append(out, filePatch{file: diffName(line[4:])})
			hunk = nil
		case strings.HasPrefix(line, "@@"):
			old, nw, ol, nl, ok := hunkHeader(line)
			if !ok {
				hunk = nil
				continue
			}
			if len(out) == 0 {
				out = append(out, filePatch{file: fallback})
			}
			cur := &out[len(out)-1]
			cur.hunks = append(cur.hunks, ccPatch{OldStart: old, NewStart: nw})
			hunk, oldLeft, newLeft = &cur.hunks[len(cur.hunks)-1], ol, nl
		}
	}
	return compactPatches(out)
}

// compactPatches drops the files with no hunk: they changed nothing to show.
func compactPatches(out []filePatch) []filePatch {
	n := 0
	for _, f := range out {
		if len(f.hunks) > 0 {
			out[n] = f
			n++
		}
	}
	return out[:n]
}

// diffName is a file name from a "+++ " or "--- " line: without the a/ or b/
// prefix and the tab and time after it.
func diffName(s string) string {
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "a/") || strings.HasPrefix(s, "b/") {
		s = s[2:]
	}
	return s
}

// hunkHeader reads "@@ -a,b +c,d @@": the start line and the line count of
// each side. A count left out is 1.
func hunkHeader(line string) (old, nw, oldN, newN int, ok bool) {
	f := strings.Fields(line)
	if len(f) < 3 || len(f[1]) < 2 || len(f[2]) < 2 || f[1][0] != '-' || f[2][0] != '+' {
		return 0, 0, 0, 0, false
	}
	side := func(s string) (int, int, bool) {
		start, count, has := strings.Cut(s[1:], ",")
		a, err := strconv.Atoi(start)
		if err != nil || a < 0 {
			return 0, 0, false
		}
		if !has {
			return a, 1, true
		}
		n, err := strconv.Atoi(count)
		// A count past what any hunk holds is not believed.
		return a, n, err == nil && n >= 0 && n <= 1<<20
	}
	old, oldN, ok1 := side(f[1])
	nw, newN, ok2 := side(f[2])
	return old, nw, oldN, newN, ok1 && ok2
}

// parseApplyPatch reads Codex's apply_patch format:
//
//	*** Begin Patch
//	*** Update File: path
//	@@ context
//	 kept
//	-removed
//	+added
//	*** Add File: path
//	+line
//	*** Delete File: path
//	*** End Patch
//
// Each "@@" chunk of an update becomes an old and a new text, which the diff
// is worked out from, since the format gives no line numbers.
func parseApplyPatch(s string) []filePatch {
	var out []filePatch
	var cur *filePatch
	var old, nw strings.Builder
	var adding, inChunk bool
	flush := func() {
		if cur != nil && inChunk {
			cur.pairs = append(cur.pairs, [2]string{old.String(), nw.String()})
		}
		old.Reset()
		nw.Reset()
		inChunk = false
	}
	start := func(name string, add bool) {
		flush()
		if len(out) == patchFilesMax {
			cur = nil
			return
		}
		out = append(out, filePatch{file: strings.TrimSpace(name)})
		cur, adding = &out[len(out)-1], add
		inChunk = add
	}
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case strings.HasPrefix(line, "*** Update File: "):
			start(line[len("*** Update File: "):], false)
		case strings.HasPrefix(line, "*** Add File: "):
			start(line[len("*** Add File: "):], true)
		case strings.HasPrefix(line, "*** Delete File: "):
			start(line[len("*** Delete File: "):], false)
		case strings.HasPrefix(line, "*** Move to: "):
			if cur != nil {
				cur.file = strings.TrimSpace(line[len("*** Move to: "):])
			}
		case strings.HasPrefix(line, "***"):
			// Begin Patch, End Patch, End of File.
		case cur == nil:
		case strings.HasPrefix(line, "@@"):
			if !adding {
				flush()
				inChunk = true
			}
		case line == "":
		default:
			if old.Len()+nw.Len() > rawTextMax*4 {
				continue
			}
			inChunk = true
			text := line[1:] + "\n"
			switch line[0] {
			case '+':
				nw.WriteString(text)
			case '-':
				old.WriteString(text)
			case ' ':
				old.WriteString(text)
				nw.WriteString(text)
			}
		}
	}
	flush()
	return out
}

// itoa is strconv.Itoa, short for the part names of keyed ids.
func itoa(i int) string { return strconv.Itoa(i) }
