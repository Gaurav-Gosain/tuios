package transcriptview

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/linediff"
)

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

type ccBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   ccResultContent `json:"content"`
	IsError   bool            `json:"is_error"`
	Source    struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
	} `json:"source"`
}

type ccContent []ccBlock

func (c *ccContent) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || b[0] == 'n':
		*c = nil
		return nil
	case b[0] == '"':
		var s string
		if err := jsonx.Unmarshal(b, &s); err != nil {
			return err
		}
		*c = ccContent{{Type: "text", Text: s}}
		return nil
	}
	var blocks []ccBlock
	err := jsonx.Unmarshal(b, &blocks)
	*c = blocks
	return err
}

type ccResultContent struct {
	text   string
	images []ccImageSource
}

type ccImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	at        int
}

func (c *ccResultContent) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || b[0] == 'n':
		return nil
	case b[0] == '"':
		return jsonx.Unmarshal(b, &c.text)
	case b[0] != '[':
		return nil
	}
	var parts []struct {
		Type   string        `json:"type"`
		Text   string        `json:"text"`
		Source ccImageSource `json:"source"`
	}
	_ = jsonx.Unmarshal(b, &parts)
	var sb strings.Builder
	for i, p := range parts {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		switch p.Type {
		case "text":
			sb.WriteString(p.Text)
		case "image":
			sb.WriteString("[image]")
			p.Source.at = i
			c.images = append(c.images, p.Source)
		}
	}
	c.text = sb.String()
	return nil
}

type ccToolUseResult struct {
	FilePath        string    `json:"filePath"`
	Type            string    `json:"type"`
	StructuredPatch []ccPatch `json:"structuredPatch"`
	File            struct {
		Dimensions struct {
			OriginalWidth  int `json:"originalWidth"`
			OriginalHeight int `json:"originalHeight"`
		} `json:"dimensions"`
	} `json:"file"`
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
	_ = jsonx.Unmarshal(b, &p)
	*t = ccToolUseResult(p)
	return nil
}

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

func toolInput(raw json.RawMessage) ccInput {
	var in ccInput
	if len(raw) > 0 && raw[0] == '{' {
		_ = jsonx.Unmarshal(raw, &in)
	}
	return in
}

func unmarshalRecord(line []byte, rec *ccRecord) error {
	err := jsonx.Unmarshal(line, rec)
	if _, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return nil
	}
	return err
}

func claudeLine(r *reader, line []byte, off int64) (decoded, bool) {
	var rec ccRecord
	if err := unmarshalRecord(line, &rec); err != nil {
		return decoded{}, false
	}
	return decoded{entries: r.recordEntries(&rec, off)}, true
}

func (r *reader) recordEntries(rec *ccRecord, off int64) []Entry {
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
		if b.Type == "tool_result" {
			out = resultImages(out, rec, &b, e.ID, at)
		}
	}
	return out
}

const resultImagesMax = 8

func resultImages(out []Entry, rec *ccRecord, b *ccBlock, id string, at int64) []Entry {
	imgs := b.Content.images
	for _, src := range imgs[:min(len(imgs), resultImagesMax)] {
		e := Entry{ID: id + "." + strconv.Itoa(src.at), At: at, Role: RoleTool, Kind: KindText, Text: "[image]", ToolID: b.ToolUseID}
		if src.Type == "base64" && imageType(src.MediaType) {
			e.Image = &Image{MediaType: src.MediaType}
			if dim := rec.ToolUseResult.File.Dimensions; len(imgs) == 1 && dim.OriginalWidth > 0 && dim.OriginalHeight > 0 {
				e.Image.Width, e.Image.Height = dim.OriginalWidth, dim.OriginalHeight
			}
		}
		out = append(out, e)
	}
	return out
}

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
		if role == RoleUser && InterruptText(b.Text) {
			return Entry{Role: role, Kind: KindInterrupt}, true
		}
		return Entry{Role: role, Kind: KindText, raw: rawText(b.Text)}, true
	case "image":
		e := Entry{Role: role, Kind: KindText, Text: "[image]"}
		if b.Source.Type == "base64" && imageType(b.Source.MediaType) {
			e.Image = &Image{MediaType: b.Source.MediaType}
		}
		return e, true
	case "thinking", "redacted_thinking":
		return Entry{Role: RoleAssistant, Kind: KindThinking, raw: rawText(b.Thinking)}, true
	case "tool_use":
		return r.toolCall(b), true
	case "tool_result":
		return r.toolResult(rec, b), true
	}
	return Entry{}, false
}

//go:noinline
func rawText(s string) *rawEntry {
	text, cut := cutString(s, rawTextMax)
	return &rawEntry{text: text, textCut: cut}
}

func (r *reader) toolResult(rec *ccRecord, b *ccBlock) Entry {
	e := Entry{Role: RoleTool, Kind: KindToolResult, ToolID: b.ToolUseID, Status: StatusOK}
	content := b.Content.text
	if b.IsError {
		e.Status = StatusError
		if reason, ok := declined(content); ok {
			e.Status, content = StatusDeclined, reason
		}
	}
	text, cutLines := headLines(content, ResultLines)
	text, cutBytes := cutString(text, rawTextMax)
	e.raw = &rawEntry{text: text, textCut: cutLines || cutBytes}
	if p := rec.ToolUseResult.StructuredPatch; len(p) > 0 {
		e.raw.diff = &diffSrc{file: rec.ToolUseResult.FilePath, patch: p}
	}
	return e
}

// InterruptText reports whether a person's text is the mark Claude Code
// writes where the person stopped the agent.
func InterruptText(s string) bool {
	switch strings.TrimSpace(s) {
	case "[Request interrupted by user]", "[Request interrupted by user for tool use]":
		return true
	}
	return false
}

const (
	declineText = "The user doesn't want to proceed with this tool use."
	declineSaid = "the user said:\n"
)

func declined(s string) (string, bool) {
	if !strings.HasPrefix(s, declineText) {
		return "", false
	}
	_, said, ok := strings.Cut(s, declineSaid)
	if !ok {
		return "", true
	}
	return strings.TrimSpace(said), true
}

func (r *reader) toolCall(b *ccBlock) Entry {
	in := toolInput(b.Input)
	raw := &rawEntry{}
	raw.tool, _ = cutString(oneLine(b.Name), 4*toolMax)
	raw.target, _ = cutString(oneLine(target(b.Name, in)), 4*targetMax)
	e := Entry{Role: RoleAssistant, Kind: KindToolCall, ToolID: b.ID, raw: raw}
	var args jobj
	if strings.Contains(b.Name, "tuios_") {
		args, _ = parseObj(b.Input)
	}
	raw.calls = AgentCallsOf(b.Name, in.Command, args)
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

func (r *reader) editDiff(file string, pairs [][2]string) *Diff {
	name, _ := r.clean(oneLine(file), targetMax)
	d := &Diff{File: name}
	shown := 0
	for _, p := range pairs {
		res := lineDiff(linediff.Split(p[0]), linediff.Split(p[1]), &r.lcsLeft)
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
				continue
			}
			h.Lines = append(h.Lines, DiffLine{Op: op, Text: l[1:]})
		}
		shown = r.addHunk(d, h, shown)
	}
	return d
}

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

type diffOp = linediff.Op

type lineDiffResult struct {
	ops            []diffOp
	added, removed int
	whole          bool
	cut            bool
	skip           int
}

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
	ctxA := a[max(0, pre-3):pre]
	ctxB := a[len(a)-suf : len(a)-suf+min(suf, 3)]
	ops := make([]diffOp, 0, len(ctxA)+len(ctxB)+min(len(ma)+len(mb), DiffLinesMax+2))
	for _, l := range ctxA {
		ops = append(ops, diffOp{Op: ' ', Line: l})
	}
	cells := (len(ma) + 1) * (len(mb) + 1)
	switch {
	case len(ma) == 0 || len(mb) == 0:
		ops = appendSide(ops, '-', ma, DiffLinesMax, &res.cut)
		ops = appendSide(ops, '+', mb, DiffLinesMax, &res.cut)
	case cells > *budget:
		res.whole = true
		ops = appendSide(ops, '-', ma, DiffLinesMax/2, &res.cut)
		ops = appendSide(ops, '+', mb, DiffLinesMax/2, &res.cut)
	default:
		*budget -= cells
		lcs := linediff.LCS(ma, mb)
		res.added, res.removed = 0, 0
		for _, o := range lcs {
			switch o.Op {
			case '+':
				res.added++
			case '-':
				res.removed++
			}
		}
		ops = append(ops, lcs...)
	}
	for _, l := range ctxB {
		ops = append(ops, diffOp{Op: ' ', Line: l})
	}
	res.ops = ops
	return res
}

func appendSide(ops []diffOp, op byte, lines []string, keep int, cut *bool) []diffOp {
	if len(lines) > keep {
		lines = lines[:keep]
		*cut = true
	}
	for _, l := range lines {
		ops = append(ops, diffOp{Op: op, Line: l})
	}
	return ops
}

func hunks(ops []diffOp, ctx, skip int) []Hunk {
	var out []Hunk
	for start := 0; start < len(ops); {
		first := start
		for first < len(ops) && ops[first].Op == ' ' {
			first++
		}
		if first == len(ops) {
			break
		}
		last := first
		for k := first; k < len(ops); k++ {
			if ops[k].Op != ' ' {
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
			if o.Op != '+' {
				oldLine++
			}
			if o.Op != '-' {
				newLine++
			}
		}
		h := Hunk{OldStart: oldLine, NewStart: newLine}
		oldCount, newCount := 0, 0
		for _, o := range ops[lo:hi] {
			if o.Op != '+' {
				oldCount++
			}
			if o.Op != '-' {
				newCount++
			}
			h.Lines = append(h.Lines, DiffLine{Op: string(o.Op), Text: o.Line})
		}
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
