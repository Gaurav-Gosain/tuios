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
		text, cutText := r.clean(b.Text, TextMax)
		return Entry{Role: role, Kind: KindText, Text: text, Truncated: cutText}, true
	case "image":
		return Entry{Role: role, Kind: KindText, Text: "[image]"}, true
	case "thinking":
		if strings.TrimSpace(b.Thinking) == "" {
			return Entry{}, false
		}
		text, cutText := r.clean(b.Thinking, TextMax)
		return Entry{Role: RoleAssistant, Kind: KindThinking, Text: text, Truncated: cutText}, true
	case "tool_use":
		return r.toolCall(b), true
	case "tool_result":
		return r.toolResult(rec, b), true
	}
	return Entry{}, false
}

// toolResult is a result: its status and the head of its text.
func (r *reader) toolResult(rec *ccRecord, b *ccBlock) Entry {
	e := Entry{Role: RoleTool, Kind: KindToolResult, ToolID: b.ToolUseID, Status: StatusOK}
	if b.IsError {
		e.Status = StatusError
	}
	text := string(b.Content)
	if r.opts.Clean != nil {
		text = r.opts.Clean(text)
	}
	text, cutLines := headLines(text, ResultLines)
	text, cutBytes := cut(text, TextMax)
	e.Text, e.Truncated = text, cutLines || cutBytes
	if p := rec.ToolUseResult.StructuredPatch; len(p) > 0 {
		e.Diff = r.patchDiff(rec.ToolUseResult.FilePath, p)
	}
	return e
}

// toolCall is a tool call, a plan or a todo list.
func (r *reader) toolCall(b *ccBlock) Entry {
	in := toolInput(b.Input)
	tool, _ := r.clean(oneLine(b.Name), toolMax)
	e := Entry{Role: RoleAssistant, Kind: KindToolCall, Tool: tool, ToolID: b.ID}
	e.Target, _ = r.clean(oneLine(target(b.Name, in)), targetMax)
	switch b.Name {
	case "TodoWrite":
		e.Kind = KindTodos
		for _, t := range in.Todos[:min(len(in.Todos), todosMax)] {
			text, _ := r.clean(oneLine(t.Content), targetMax)
			status, _ := r.clean(oneLine(t.Status), toolMax)
			e.Todos = append(e.Todos, Todo{Text: text, Status: status})
		}
	case "ExitPlanMode":
		e.Kind = KindPlan
		e.Plan, e.Truncated = r.clean(in.Plan, TextMax)
	case "Edit":
		e.Diff = r.editDiff(in.FilePath, [][2]string{{in.OldString, in.NewString}})
	case "MultiEdit":
		pairs := make([][2]string, 0, len(in.Edits))
		for _, ed := range in.Edits {
			pairs = append(pairs, [2]string{ed.OldString, ed.NewString})
		}
		e.Diff = r.editDiff(in.FilePath, pairs)
	case "Write":
		e.Diff = r.editDiff(in.FilePath, [][2]string{{"", in.Content}})
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
		ops := lineDiff(splitLines(p[0]), splitLines(p[1]))
		for _, h := range hunks(ops, 3) {
			for _, l := range h.Lines {
				switch l.Op {
				case "+":
					d.Added++
				case "-":
					d.Removed++
				}
			}
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

// lineDiff is the edit script from a to b by longest common subsequence. Two
// texts too large to compare in bounded time are all removed and all added,
// which is still a correct diff.
func lineDiff(a, b []string) []diffOp {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ops := make([]diffOp, 0, len(a)+len(b))
	for _, l := range a[:pre] {
		ops = append(ops, diffOp{' ', l})
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	if len(ma)*len(mb) > maxLineDiffOps {
		for _, l := range ma {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range mb {
			ops = append(ops, diffOp{'+', l})
		}
	} else {
		ops = append(ops, lcsOps(ma, mb)...)
	}
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, diffOp{' ', l})
	}
	return ops
}

func lcsOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	table := make([][]int32, n+1)
	for i := range table {
		table[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else {
				table[i][j] = max(table[i+1][j], table[i][j+1])
			}
		}
	}
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
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

// hunks groups an edit script into hunks with ctx lines of context.
func hunks(ops []diffOp, ctx int) []Hunk {
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
		oldLine, newLine := 1, 1
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
