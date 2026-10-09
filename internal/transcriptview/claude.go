package transcriptview

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// The Claude Code record, as far as a conversation needs it. A user record's
// content is a string (a prompt) or an array of blocks (text, images and tool
// results), and an assistant record's is an array of text, thinking and
// tool_use blocks. A tool result's record also carries toolUseResult, whose
// structuredPatch has the real line numbers of an edit.
type ccRecord struct {
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	Message     struct {
		Content ccContent `json:"content"`
	} `json:"message"`
	ToolUseResult ccToolUseResult `json:"toolUseResult"`
}

// ccBlock is one content block.
type ccBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	// Input is decoded per tool, by toolInput, so a tool whose input has a
	// field of an unexpected type does not cost the whole record.
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   ccResultContent `json:"content"`
	IsError   bool            `json:"is_error"`
}

// ccContent is a message's content: a plain string, or blocks.
type ccContent []ccBlock

func (c *ccContent) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || b[0] == 'n':
		*c = nil
		return nil
	case b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*c = ccContent{{Type: "text", Text: s}}
		return nil
	}
	var blocks []ccBlock
	err := json.Unmarshal(b, &blocks)
	*c = blocks
	return err
}

// ccResultContent is a tool result's content: a string, or text and image
// blocks, joined as text.
type ccResultContent string

func (c *ccResultContent) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || b[0] == 'n':
		return nil
	case b[0] == '"':
		var s string
		err := json.Unmarshal(b, &s)
		*c = ccResultContent(s)
		return err
	case b[0] != '[':
		return nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(b, &parts)
	var sb strings.Builder
	for _, p := range parts {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		switch p.Type {
		case "text":
			sb.WriteString(p.Text)
		case "image":
			sb.WriteString("[image]")
		}
	}
	*c = ccResultContent(sb.String())
	return nil
}

// ccToolUseResult is the part of toolUseResult a diff needs. It is a string
// for some tools and an object for others, so anything but an object leaves
// it empty.
type ccToolUseResult struct {
	FilePath        string    `json:"filePath"`
	Type            string    `json:"type"`
	StructuredPatch []ccPatch `json:"structuredPatch"`
}

type ccPatch struct {
	OldStart int      `json:"oldStart"`
	NewStart int      `json:"newStart"`
	Lines    []string `json:"lines"`
}

func (t *ccToolUseResult) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' {
		return nil
	}
	type plain ccToolUseResult
	var p plain
	_ = json.Unmarshal(b, &p)
	*t = ccToolUseResult(p)
	return nil
}

// ccInput is every tool input field a conversation shows.
type ccInput struct {
	Command      string `json:"command"`
	Description  string `json:"description"`
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Path         string `json:"path"`
	Pattern      string `json:"pattern"`
	URL          string `json:"url"`
	Query        string `json:"query"`
	Skill        string `json:"skill"`
	OldString    string `json:"old_string"`
	NewString    string `json:"new_string"`
	Content      string `json:"content"`
	Plan         string `json:"plan"`
	Edits        []struct {
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	} `json:"edits"`
	Todos []struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	} `json:"todos"`
}

// toolInput decodes what it can of a tool's input. A field of another type
// is left empty and the rest is kept.
func toolInput(raw json.RawMessage) ccInput {
	var in ccInput
	if len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &in)
	}
	return in
}

// unmarshalRecord decodes a line. A field of an unexpected type leaves that
// field empty rather than dropping the record.
func unmarshalRecord(line []byte, rec *ccRecord) error {
	err := json.Unmarshal(line, rec)
	if _, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return nil
	}
	return err
}

// recordEntries turns one record at offset off into entries.
func (r *reader) recordEntries(rec *ccRecord, off int64) []Entry {
	// A subagent's records share the file with its parent's. They are the
	// subagent's own conversation, which the parent's Task call stands for.
	if rec.IsSidechain || rec.IsMeta {
		return nil
	}
	if rec.Type != "user" && rec.Type != "assistant" {
		return nil
	}
	at := int64(0)
	if t, err := time.Parse(time.RFC3339Nano, rec.Timestamp); err == nil {
		at = t.UnixMilli()
	}
	prefix := strconv.FormatInt(off, 36) + "-"
	var out []Entry
	for i, b := range rec.Message.Content {
		e, ok := r.blockEntry(rec, &b)
		if !ok {
			continue
		}
		e.ID = prefix + strconv.Itoa(i)
		e.At = at
		out = append(out, e)
	}
	return out
}

// blockEntry turns one block into an entry.
func (r *reader) blockEntry(rec *ccRecord, b *ccBlock) (Entry, bool) {
	role := RoleUser
	if rec.Type == "assistant" {
		role = RoleAssistant
	}
	switch b.Type {
	case "text":
		if strings.TrimSpace(b.Text) == "" {
			return Entry{}, false
		}
		return Entry{Role: role, Kind: KindText, raw: rawText(b.Text)}, true
	case "image":
		return Entry{Role: role, Kind: KindText, Text: "[image]"}, true
	case "thinking":
		if strings.TrimSpace(b.Thinking) == "" {
			return Entry{}, false
		}
		return Entry{Role: RoleAssistant, Kind: KindThinking, raw: rawText(b.Thinking)}, true
	case "tool_use":
		return r.toolCall(b), true
	case "tool_result":
		return r.toolResult(rec, b), true
	}
	return Entry{}, false
}

// rawText is the raw of a text entry, cut to rawTextMax.
func rawText(s string) *rawEntry {
	text, cut := cutString(s, rawTextMax)
	return &rawEntry{text: text, textCut: cut}
}

// toolResult is a result: its status and the head of its text. The head is
// taken before Clean, so only the lines the page can show are cleaned, and a
// secret that starts in them is masked to their end.
func (r *reader) toolResult(rec *ccRecord, b *ccBlock) Entry {
	e := Entry{Role: RoleTool, Kind: KindToolResult, ToolID: b.ToolUseID, Status: StatusOK}
	if b.IsError {
		e.Status = StatusError
	}
	text, cutLines := headLines(string(b.Content), ResultLines)
	text, cutBytes := cutString(text, rawTextMax)
	e.raw = &rawEntry{text: text, textCut: cutLines || cutBytes}
	if p := rec.ToolUseResult.StructuredPatch; len(p) > 0 {
		e.raw.diff = &diffSrc{file: rec.ToolUseResult.FilePath, patch: p}
	}
	return e
}

// toolCall is a tool call, a plan or a todo list, as decoded. finish cleans
// it and works out its diff.
func (r *reader) toolCall(b *ccBlock) Entry {
	in := toolInput(b.Input)
	raw := &rawEntry{}
	raw.tool, _ = cutString(oneLine(b.Name), 4*toolMax)
	raw.target, _ = cutString(oneLine(target(b.Name, in)), 4*targetMax)
	e := Entry{Role: RoleAssistant, Kind: KindToolCall, ToolID: b.ID, raw: raw}
	switch b.Name {
	case "TodoWrite":
		e.Kind = KindTodos
		for _, t := range in.Todos[:min(len(in.Todos), todosMax)] {
			text, _ := cutString(oneLine(t.Content), 4*targetMax)
			status, _ := cutString(oneLine(t.Status), 4*toolMax)
			raw.todos = append(raw.todos, Todo{Text: text, Status: status})
		}
	case "ExitPlanMode":
		e.Kind = KindPlan
		raw.plan, raw.textCut = cutString(in.Plan, rawTextMax)
	case "Edit":
		raw.diff = &diffSrc{file: in.FilePath, pairs: [][2]string{{in.OldString, in.NewString}}}
	case "MultiEdit":
		pairs := make([][2]string, 0, len(in.Edits))
		for _, ed := range in.Edits {
			pairs = append(pairs, [2]string{ed.OldString, ed.NewString})
		}
		raw.diff = &diffSrc{file: in.FilePath, pairs: pairs}
	case "Write":
		raw.diff = &diffSrc{file: in.FilePath, pairs: [][2]string{{"", in.Content}}}
	}
	return e
}

// target is the one line that says what a call acts on.
func target(tool string, in ccInput) string {
	switch tool {
	case "Bash":
		return in.Command
	case "Task", "Agent":
		return in.Description
	case "WebFetch":
		return in.URL
	case "WebSearch":
		return in.Query
	case "Skill":
		return in.Skill
	case "TodoWrite", "ExitPlanMode":
		return ""
	}
	for _, s := range []string{in.FilePath, in.NotebookPath, in.Command, in.Pattern, in.URL, in.Query, in.Path, in.Description} {
		if s != "" {
			return s
		}
	}
	return ""
}

// --- Diffs.

// editDiff is the diff of one or more old and new texts of one file. Its
// line numbers count from the edited text, since the call does not say where
// in the file it is. A result's structuredPatch, folded in by foldResults,
// gives the file's own.
func (r *reader) editDiff(file string, pairs [][2]string) *Diff {
	name, _ := r.clean(oneLine(file), targetMax)
	d := &Diff{File: name}
	shown := 0
	for _, p := range pairs {
		res := lineDiff(splitLines(p[0]), splitLines(p[1]), &r.lcsLeft)
		d.Added += res.added
		d.Removed += res.removed
		d.WholeReplace = d.WholeReplace || res.whole
		d.Truncated = d.Truncated || res.cut
		for _, h := range hunks(res.ops, 3, res.skip) {
			shown = r.addHunk(d, h, shown)
		}
	}
	return d
}

// patchDiff is a structuredPatch as a diff.
func (r *reader) patchDiff(file string, patch []ccPatch) *Diff {
	name, _ := r.clean(oneLine(file), targetMax)
	d := &Diff{File: name}
	shown := 0
	for _, p := range patch {
		h := Hunk{OldStart: p.OldStart, NewStart: p.NewStart}
		for _, l := range p.Lines {
			if l == "" {
				h.Lines = append(h.Lines, DiffLine{Op: " "})
				continue
			}
			op := l[:1]
			switch op {
			case "+":
				d.Added++
			case "-":
				d.Removed++
			case " ":
			default:
				// "\ No newline at end of file" and the like.
				continue
			}
			h.Lines = append(h.Lines, DiffLine{Op: op, Text: l[1:]})
		}
		shown = r.addHunk(d, h, shown)
	}
	return d
}

// addHunk adds h to d within DiffLinesMax lines, cleaning each line, and
// returns how many lines d shows now.
func (r *reader) addHunk(d *Diff, h Hunk, shown int) int {
	if shown >= DiffLinesMax {
		d.Truncated = true
		return shown
	}
	if room := DiffLinesMax - shown; len(h.Lines) > room {
		h.Lines = h.Lines[:room]
		d.Truncated = true
	}
	for i := range h.Lines {
		h.Lines[i].Text, _ = r.clean(h.Lines[i].Text, diffLineMax)
	}
	if r.opts.CleanLines != nil && len(h.Lines) > 0 {
		// A key or an .env block spans lines, so it is masked over the
		// hunk's lines together, in the order the hunk shows them.
		texts := make([]string, len(h.Lines))
		for i := range h.Lines {
			texts[i] = h.Lines[i].Text
		}
		if r.opts.CleanLines(texts) {
			for i := range h.Lines {
				h.Lines[i].Text = texts[i]
			}
		}
	}
	d.Hunks = append(d.Hunks, h)
	return shown + len(h.Lines)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// diffOp is one line of an edit script.
type diffOp struct {
	op   byte
	line string
}

// lineDiffResult is an edit script and what it counts.
type lineDiffResult struct {
	ops            []diffOp
	added, removed int
	// whole says the changed lines were not matched: they are all removed
	// and then all added.
	whole bool
	// cut says some changed lines of a whole replace were left out of ops.
	cut bool
	// skip is how many equal lines at the start were left out of ops, so
	// the line numbers of the hunks start after them.
	skip int
}

// lineDiff is the edit script from a to b by longest common subsequence.
// budget is what is left of the call's LCSCellsPerCall, and the table's
// cells are taken from it. When the table would not fit, the changed lines
// are all removed and all added, which is still a correct diff, and at most
// DiffLinesMax/2 of each side are kept, since no more could be shown.
func lineDiff(a, b []string, budget *int) lineDiffResult {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	res := lineDiffResult{added: len(mb), removed: len(ma), skip: max(0, pre-3)}
	// Context beyond what a hunk shows is never read, so it is not kept.
	ctxA := a[max(0, pre-3):pre]
	ctxB := a[len(a)-suf : len(a)-suf+min(suf, 3)]
	ops := make([]diffOp, 0, len(ctxA)+len(ctxB)+min(len(ma)+len(mb), DiffLinesMax+2))
	for _, l := range ctxA {
		ops = append(ops, diffOp{' ', l})
	}
	cells := (len(ma) + 1) * (len(mb) + 1)
	switch {
	case len(ma) == 0 || len(mb) == 0:
		// Only added or only removed: the script needs no table.
		ops = appendSide(ops, '-', ma, DiffLinesMax, &res.cut)
		ops = appendSide(ops, '+', mb, DiffLinesMax, &res.cut)
	case cells > *budget:
		res.whole = true
		ops = appendSide(ops, '-', ma, DiffLinesMax/2, &res.cut)
		ops = appendSide(ops, '+', mb, DiffLinesMax/2, &res.cut)
	default:
		*budget -= cells
		lcs := lcsOps(ma, mb)
		res.added, res.removed = 0, 0
		for _, o := range lcs {
			switch o.op {
			case '+':
				res.added++
			case '-':
				res.removed++
			}
		}
		ops = append(ops, lcs...)
	}
	for _, l := range ctxB {
		ops = append(ops, diffOp{' ', l})
	}
	res.ops = ops
	return res
}

// appendSide appends up to keep lines of one side, setting cut when it
// leaves some out.
func appendSide(ops []diffOp, op byte, lines []string, keep int, cut *bool) []diffOp {
	if len(lines) > keep {
		lines = lines[:keep]
		*cut = true
	}
	for _, l := range lines {
		ops = append(ops, diffOp{op, l})
	}
	return ops
}

// lcsOps is the edit script of a and b by longest common subsequence. The
// lines are numbered first, equal lines the same number, so the table
// compares integers, not strings. The table is one block of uint16: the
// caller keeps it within LCSCellsPerCall cells, so the shorter side, which
// bounds every value in it, has fewer than 2048 lines.
func lcsOps(a, b []string) []diffOp {
	ids := make(map[string]int32, len(a)+len(b))
	number := func(lines []string) []int32 {
		out := make([]int32, len(lines))
		for i, l := range lines {
			id, ok := ids[l]
			if !ok {
				id = int32(len(ids))
				ids[l] = id
			}
			out[i] = id
		}
		return out
	}
	ha, hb := number(a), number(b)
	n, m := len(a), len(b)
	w := m + 1
	table := make([]uint16, (n+1)*w)
	at := func(i, j int) uint16 { return table[i*w+j] }
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if ha[i] == hb[j] {
				table[i*w+j] = at(i+1, j+1) + 1
			} else {
				table[i*w+j] = max(at(i+1, j), at(i, j+1))
			}
		}
	}
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case ha[i] == hb[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case at(i+1, j) >= at(i, j+1):
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// hunks groups an edit script into hunks with ctx lines of context. skip is
// how many equal lines came before the script's first.
func hunks(ops []diffOp, ctx, skip int) []Hunk {
	var out []Hunk
	for start := 0; start < len(ops); {
		first := start
		for first < len(ops) && ops[first].op == ' ' {
			first++
		}
		if first == len(ops) {
			break
		}
		last := first
		for k := first; k < len(ops); k++ {
			if ops[k].op != ' ' {
				last = k
				continue
			}
			if k-last > 2*ctx {
				break
			}
		}
		lo := max(first-ctx, start)
		hi := min(last+ctx+1, len(ops))
		oldLine, newLine := 1+skip, 1+skip
		for _, o := range ops[:lo] {
			if o.op != '+' {
				oldLine++
			}
			if o.op != '-' {
				newLine++
			}
		}
		h := Hunk{OldStart: oldLine, NewStart: newLine}
		oldCount, newCount := 0, 0
		for _, o := range ops[lo:hi] {
			if o.op != '+' {
				oldCount++
			}
			if o.op != '-' {
				newCount++
			}
			h.Lines = append(h.Lines, DiffLine{Op: string(o.op), Text: o.line})
		}
		// A side with no lines starts at the line before, as unified diffs
		// write it: a new file is old_start 0.
		if oldCount == 0 {
			h.OldStart--
		}
		if newCount == 0 {
			h.NewStart--
		}
		out = append(out, h)
		start = hi
	}
	return out
}
