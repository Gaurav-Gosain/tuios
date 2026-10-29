package webshell

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Scripted coding agents in the style of Claude Code, Codex and Gemini CLI.
// Each one works through a short script: it reads files, thinks, edits, and
// for some steps asks the person first. It draws what its harness manifest
// keys on, reports each state to tuios the way a hooked agent does, so the
// rail, the title glyph and the alerts all follow it, and keeps a record of
// its run (agents.go) for the agents view. When a turn ends it waits at its
// input line, and the next line typed there is a new turn.

// AgentHarness is the harness id the claude agent reports as.
const AgentHarness = "claude-code"

const (
	agentOrange = "\x1b[38;5;209m"
	agentGrey   = "\x1b[38;5;245m"
	agentBlue   = "\x1b[38;5;75m"
	agentTeal   = "\x1b[38;5;80m"
)

var agentSpinner = []string{"·", "✢", "✳", "✶", "✻", "✽", "✻", "✶", "✳", "✢"}

var brailleSpinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const styleDark = `h1 { color: hotpink; }

[data-theme=dark] { background: #11111b; color: #cdd6f4; }
button.theme { position: fixed; top: 1rem; right: 1rem; }
`

const indexDark = `<!doctype html>
<title>hi</title>
<link rel="stylesheet" href="style.css">
<button class="theme" onclick="document.body.dataset.theme ^= 'dark'">dark mode</button>
<h1>it works</h1>
`

// persona is one pretend agent: how it looks and what its first turn does.
type persona struct {
	name    string // the command, such as "claude"
	harness string // the harness id it reports as
	product string // the name in its welcome box
	accent  string // its ink
	task    string // the task when none is given
	// you, say and result start the person's prompt, an agent message and
	// a tool's result. pick marks the chosen option of a menu.
	you, say, result, pick string
	// tool is the line for a tool call.
	tool func(name, target string) string
	// spin is the line shown while it thinks.
	spin func(i, secs int) string
	// first is the first turn. It reports false when the person stopped it.
	first func(r *agentRun, task string) bool
}

var personas map[string]*persona

func initPersonas() {
	personas = map[string]*persona{
		"claude": {
			name: "claude", harness: AgentHarness, product: "Claude Code", accent: agentOrange,
			task: "add a dark mode toggle",
			you:  agentGrey + "> " + reset, say: bold + "●" + reset + " ", result: agentGrey + "  ⎿  " + reset, pick: "❯ ",
			tool: func(name, target string) string {
				return green + "●" + reset + " " + bold + name + reset + "(" + target + ")"
			},
			spin: func(i, secs int) string {
				return agentOrange + agentSpinner[i%len(agentSpinner)] + " Thinking…" + reset +
					agentGrey + " (" + strconv.Itoa(secs) + "s · esc to interrupt)" + reset
			},
			first: claudeTurn,
		},
		"codex": {
			name: "codex", harness: "codex", product: ">_ OpenAI Codex", accent: agentTeal,
			task: "add greet tests",
			you:  bold + "› " + reset, say: "• ", result: agentGrey + "  └ " + reset, pick: "› ",
			tool: func(name, target string) string { return "• " + bold + name + reset + " " + target },
			spin: func(i, secs int) string {
				return agentTeal + "•" + reset + " " + bold + "Working" + reset +
					agentGrey + " (" + strconv.Itoa(secs) + "s • esc to interrupt)" + reset
			},
			first: codexTurn,
		},
		"gemini": {
			name: "gemini", harness: "gemini-cli", product: "✦ Gemini CLI", accent: agentBlue,
			task: "pick the theme colors",
			you:  agentBlue + "> " + reset, say: agentBlue + "✦" + reset + " ", result: agentGrey + "    " + reset, pick: "● ",
			tool: func(name, target string) string {
				return green + "✔" + reset + " " + bold + name + reset + " " + target
			},
			spin: func(i, secs int) string {
				return agentBlue + brailleSpinner[i%len(brailleSpinner)] + reset + " Thinking…" +
					agentGrey + " (esc to cancel, " + strconv.Itoa(secs) + "s)" + reset
			},
			first: geminiTurn,
		},
	}
}

// agentRun is one agent running in a pane.
type agentRun struct {
	t *TTY
	p *persona
	a *Agent
	// pending is input read and not used yet: the rest of a chunk after the
	// key that answered a menu, which is the next prompt's.
	pending []byte
	// changed says the turn edited a file, so its end takes a checkpoint.
	changed bool
	// lastFile is the file the agent last read or edited.
	lastFile string
	turns    int
	tools    int
	prompts  int
	// start is when the turn started.
	start time.Time
}

// cmdAgent runs the agent args[0] names. "agent" is another name for claude.
func cmdAgent(s *shell, args []string, _ string) int {
	t := s.t
	p := personas[args[0]]
	if p == nil {
		p = personas["claude"]
	}
	task := strings.Join(args[1:], " ")
	if task == "" {
		task = p.task
	}
	r := &agentRun{t: t, p: p, a: startAgent(t, p.name, p.harness)}
	defer r.a.end(t)
	r.setTitle(task)
	t.Lay(r.welcome)
	r.startTurn(task)
	first := p.first
	if !strings.EqualFold(task, p.task) {
		// A task of the person's own: the scripted turn is for the task
		// the agent shows off.
		first = genericTurn
	}
	if !first(r, task) {
		r.interrupted()
	}
	for {
		text, ok := r.rest()
		if !ok {
			t.Print("\r\n")
			r.report("none", "", "")
			return 0
		}
		r.startTurn(text)
		if !r.followUp(text) {
			r.interrupted()
		}
	}
}

// welcome is the box the agent opens with.
func (r *agentRun) welcome(cols int) []string {
	w := agentWidth(cols)
	ink := r.p.accent
	return []string{
		ink + "╭" + strings.Repeat("─", w-2) + "╮" + reset,
		inkBoxLine(ink, w, ink+"✻"+reset+" Welcome to "+bold+r.p.product+reset+" (tuios demo)"),
		inkBoxLine(ink, w, ""),
		inkBoxLine(ink, w, agentGrey+"  A pretend agent. "+text("Nothing leaves this tab.")+reset),
		ink + "╰" + strings.Repeat("─", w-2) + "╯" + reset,
	}
}

// setTitle names the pane after the task, in a word or two, the way a
// real agent sets the terminal title.
func (r *agentRun) setTitle(task string) {
	r.t.write("\x1b]2;" + agentTitle(task) + "\x07")
}

// titleSkip are the words a title leaves out: the small ones, and the verb
// most tasks start with.
var titleSkip map[string]bool

func initTitleSkip() {
	titleSkip = map[string]bool{
		"a": true, "an": true, "the": true, "to": true, "for": true, "of": true, "and": true, "in": true,
		"on": true, "with": true, "my": true, "this": true, "that": true, "it": true, "some": true,
		"add": true, "make": true, "write": true, "fix": true, "pick": true, "update": true, "create": true,
		"build": true, "change": true, "use": true, "set": true, "please": true,
	}
}

// agentTitle is a short name for a task: its first words that say
// something, at most two and 16 cells, so an agents view row shows it whole.
func agentTitle(task string) string {
	var words []string
	for w := range strings.FieldsSeq(strings.ToLower(task)) {
		w = strings.Trim(w, ".,;:!?\"'()")
		if w == "" || titleSkip[w] || strings.ContainsFunc(w, func(c rune) bool { return c < 0x20 || c == 0x7f }) {
			continue
		}
		if len(words) > 0 && len(strings.Join(append(words, w), " ")) > 16 {
			break
		}
		words = append(words, w)
		if len(words) == 2 {
			break
		}
	}
	if len(words) == 0 {
		return "agent"
	}
	return fit(strings.Join(words, " "), 16)
}

// report tells tuios what state this pane's agent is in. See
// app.ReportAgentState, which the browser build routes this to.
func (t *TTY) report(state, message, kind, harness string) {
	t.Emit(EventAgentReport, map[string]any{
		"state": state, "message": message, "kind": kind, "harness": harness,
	})
}

// report records the state and tells tuios.
func (r *agentRun) report(state, message, kind string) {
	r.a.change(r.t, func() { r.a.state, r.a.message = state, message })
	r.t.report(state, message, kind, r.p.harness)
}

// startTurn shows the person's prompt and records it.
func (r *agentRun) startTurn(text string) {
	r.turns++
	r.changed = false
	r.start = time.Now()
	r.t.Print("\r\n" + r.p.you + text + "\r\n\r\n")
	r.a.add(r.t, AgentEntry{Role: "user", Kind: "text", Text: text})
	r.report("working", "Reading the files", "")
}

// followUp is every turn after the first: the agent reads the file it last
// worked on, thinks, and says it did what it was asked.
func (r *agentRun) followUp(text string) bool {
	file := r.lastFile
	if file == "" {
		file = "README.md"
	}
	if !r.read(file, "Read the file") || !r.think(12, "") {
		return false
	}
	r.say("Done: " + strings.TrimRight(text, ".!? ") + ".")
	r.finish("Done: " + text)
	return true
}

// read shows and records a tool that reads a file.
func (r *agentRun) read(file, note string) bool {
	r.lastFile = file
	id := r.toolID()
	r.t.Print(r.p.tool(r.readName(), file) + "\r\n")
	r.t.Print(r.p.result + agentGrey + note + reset + "\r\n")
	r.a.add(r.t, AgentEntry{Kind: "tool_call", Tool: "Read", Target: file, Status: "ok", ToolID: id})
	r.a.add(r.t, AgentEntry{Kind: "tool_result", Status: "ok", ToolID: id})
	return r.pause(450 * time.Millisecond)
}

// readName is what the agent calls reading a file.
func (r *agentRun) readName() string {
	switch r.p.name {
	case "codex":
		return "Explored"
	case "gemini":
		return "ReadFile"
	}
	return "Read"
}

// shell shows and records a command the agent ran.
func (r *agentRun) shell(cmd, out string) bool {
	id := r.toolID()
	name := "Bash"
	if r.p.name == "codex" {
		name = "Ran"
	}
	r.t.Print(r.p.tool(name, cmd) + "\r\n")
	r.t.Print(r.p.result + agentGrey + out + reset + "\r\n")
	r.a.add(r.t, AgentEntry{Kind: "tool_call", Tool: "Bash", Target: cmd, Status: "ok", ToolID: id})
	r.a.add(r.t, AgentEntry{Kind: "tool_result", Status: "ok", ToolID: id, Text: out})
	return r.pause(400 * time.Millisecond)
}

func (r *agentRun) toolID() string {
	r.tools++
	return "t" + strconv.Itoa(r.tools)
}

// think shows the spinner for steps of 110 ms, with thought as the agent's
// hidden reasoning when it is not empty.
func (r *agentRun) think(steps int, thought string) bool {
	r.report("working", "Thinking", "")
	if thought != "" {
		r.a.add(r.t, AgentEntry{Kind: "thinking", Text: thought})
	}
	r.t.Print("\r\n")
	start := time.Now()
	// The spinner is drawn outside the log: it is gone once the agent has
	// thought, and a line that wrapped would leave half of it behind.
	for i := range steps {
		cols, _ := r.t.Size()
		r.t.write("\r\x1b[K" + fit(r.p.spin(i, int(time.Since(start).Seconds())), max(cols-1, 1)))
		if !r.pause(110 * time.Millisecond) {
			r.t.write("\r\x1b[K")
			return false
		}
	}
	r.t.write("\r\x1b[K")
	return true
}

// say shows and records a message from the agent.
func (r *agentRun) say(text string) {
	lead := r.p.say
	r.t.Lay(func(cols int) []string {
		lines := wrapWords(text, max(cols-2, 8))
		for i := range lines {
			if i == 0 {
				lines[i] = lead + lines[i]
			} else {
				lines[i] = "  " + lines[i]
			}
		}
		return append(lines, "")
	})
	r.a.add(r.t, AgentEntry{Role: "assistant", Kind: "text", Text: text})
}

// edit shows an edit and records it. With ask, the agent asks before it
// makes it, and approved says what the person chose. content, when not
// empty, is what the file holds after the edit.
func (r *agentRun) edit(file string, d AgentDiff, content string, ask bool) (approved, ok bool) {
	r.lastFile = file
	id := r.toolID()
	d.File = file
	for _, h := range d.Hunks {
		for _, l := range h.Lines {
			switch l.Op {
			case "+":
				d.Added++
			case "-":
				d.Removed++
			}
		}
	}
	name := "Update"
	if r.p.name == "gemini" {
		name = "Edit"
	}
	r.t.Print(r.p.tool(name, file) + "\r\n")
	status := "ok"
	if ask {
		status = "running"
	}
	r.a.add(r.t, AgentEntry{Kind: "tool_call", Tool: "Edit", Target: file, Status: status, ToolID: id, Diff: &d})
	if ask {
		options := []string{"Yes", "Yes, and don't ask again this session", "No, and tell " + r.p.product + " what to do differently (esc)"}
		if r.p.name == "claude" {
			options[2] = "No, and tell Claude what to do differently (esc)"
		}
		base := baseName(file)
		lines := []string{bold + "Edit file" + reset, agentGrey + file + reset, ""}
		lines = append(lines, diffLines(d)...)
		lines = append(lines, "", "Do you want to make this edit to "+base+"?")
		choice := r.menu("approval", "approve Edit: "+file, lines, options, true)
		switch choice {
		case -1:
			return false, false
		case 2:
			r.a.setStatus(r.t, id, "error")
			r.a.add(r.t, AgentEntry{Kind: "tool_result", Status: "error", ToolID: id, Text: "The person turned the edit down."})
			r.t.Print(r.p.result + "No changes made to " + base + ".\r\n\r\n")
			return false, true
		}
		r.report("working", "Editing "+base, "")
		r.a.setStatus(r.t, id, "ok")
	} else {
		r.t.Lay(func(cols int) []string {
			out := diffLines(d)
			for i := range out {
				out[i] = "    " + out[i]
			}
			return fitLines(out, cols)
		})
	}
	if content != "" {
		_ = writeFile(Home+"/"+file, content, false)
	}
	r.changed = true
	r.a.add(r.t, AgentEntry{Kind: "tool_result", Status: "ok", ToolID: id})
	words := strconv.Itoa(d.Added) + " addition"
	if d.Added != 1 {
		words += "s"
	}
	r.t.Print(r.p.result + "Updated " + baseName(file) + " with " + green + words + reset + "\r\n")
	return true, r.pause(400 * time.Millisecond)
}

// ask asks the person a question with options, and reports the index of
// the answer, or -1 when they stopped the agent instead.
func (r *agentRun) ask(question string, options []string) int {
	return r.menu("question", question, []string{bold + question + reset}, options, false)
}

// menu draws a box of lines with numbered options under it, reports
// needs_input of kind, and waits for an answer. It returns the index of the
// option chosen, or -1 when the person stopped the agent. For an approval,
// y approves and n or esc chooses the last option.
func (r *agentRun) menu(kind, summary string, lines, options []string, approval bool) int {
	t := r.t
	ink := agentGrey
	box := func(cols, sel int) []string {
		w := agentWidth(cols)
		out := []string{ink + "╭" + strings.Repeat("─", w-2) + "╮" + reset}
		for _, l := range lines {
			out = append(out, inkBoxLine(ink, w, l))
		}
		for i, o := range options {
			if i == sel {
				out = append(out, inkBoxLine(ink, w, r.p.accent+r.p.pick+strconv.Itoa(i+1)+". "+o+reset))
			} else {
				out = append(out, inkBoxLine(ink, w, strings.Repeat(" ", utf8.RuneCountInString(r.p.pick))+strconv.Itoa(i+1)+". "+o))
			}
		}
		return append(out, inkBoxLine(ink, w, ""), ink+"╰"+strings.Repeat("─", w-2)+"╯"+reset)
	}
	// The box is drawn outside the log while the menu moves. Every line fits
	// the pane, so moving up its length lands on its first line. The log
	// keeps the box with the answer marked once there is one.
	sel, drawn := 0, 0
	draw := func() {
		cols, _ := t.Size()
		var b strings.Builder
		if drawn > 0 {
			b.WriteString("\x1b[")
			b.WriteString(strconv.Itoa(drawn))
			b.WriteString("A\r")
		}
		out := box(cols, sel)
		for _, l := range out {
			b.WriteString("\x1b[K")
			b.WriteString(l)
			b.WriteString("\r\n")
		}
		drawn = len(out)
		t.write(b.String())
	}
	draw()
	r.prompts++
	plain := make([]string, 0, len(lines))
	for _, l := range lines {
		plain = append(plain, stripEscapes(l))
	}
	p := &AgentPrompt{ID: r.a.Window + "-p" + strconv.Itoa(r.prompts), Kind: kind, Summary: summary,
		Lines: plain, Options: options, Since: time.Now().UnixNano()}
	r.a.change(t, func() { r.a.prompt = p })
	message := summary
	if approval {
		message = "Edit " + baseName(strings.TrimPrefix(summary, "approve Edit: ")) + "?"
	}
	r.report("needs_input", message, kind)
	// A bell is how a real agent asks for attention.
	t.Print("\a")

	choice := -2
	for choice == -2 {
		b, resized, ok := r.next()
		if !ok {
			return -1
		}
		if resized {
			drawn = 0
			t.repaint("")
			draw()
			continue
		}
		for i := 0; i < len(b) && choice == -2; i++ {
			c := b[i]
			last := len(options) - 1
			switch {
			case c == 0x1b && i+2 < len(b) && b[i+1] == '[' && (b[i+2] == 'A' || b[i+2] == 'B'):
				if b[i+2] == 'A' {
					sel = (sel + last) % len(options)
				} else {
					sel = (sel + 1) % len(options)
				}
				i += 2
				draw()
			case c >= '1' && c <= '9' && int(c-'1') <= last:
				choice = int(c - '1')
			case c == '\r' || c == '\n':
				choice = sel
			case c == 'j' || c == 0x0e:
				sel = (sel + 1) % len(options)
				draw()
			case c == 'k' || c == 0x10:
				sel = (sel + last) % len(options)
				draw()
			case approval && (c == 'y' || c == 'Y'):
				choice = 0
			case approval && (c == 0x1b || c == 'n' || c == 'N'):
				choice = last
			case c == 0x1b || c == 0x03:
				choice = -1
			}
			if choice != -2 {
				r.pending = append(r.pending[:0], b[i+1:]...)
			}
		}
	}
	r.a.change(t, func() { r.a.prompt = nil })
	if choice < 0 {
		drawn = 0
		t.write("\r\n")
		return -1
	}
	sel = choice
	draw()
	t.keepLay(func(cols int) []string { return box(cols, choice) })
	t.Print("\r\n")
	return choice
}

// next is the next input: what a menu left, else the next chunk from the
// pane. resized says the pane changed size instead.
func (r *agentRun) next() (b []byte, resized, ok bool) {
	if len(r.pending) > 0 {
		b, r.pending = r.pending, nil
		return b, false, true
	}
	select {
	case in, ok := <-r.t.In:
		return in, false, ok
	case <-r.t.Resized():
		return nil, true, true
	}
}

// finish ends a turn: the summary, a checkpoint when the turn changed a
// file, and done.
func (r *agentRun) finish(summary string) {
	if r.changed {
		r.a.change(r.t, func() {
			n := len(r.a.checkpoints) + 1
			r.a.checkpoints = append(r.a.checkpoints, AgentCheckpoint{N: n, Kind: "turn", Label: summary, State: "done", At: time.Now().UnixNano()})
		})
	}
	r.report("done", summary, "")
}

// interrupted ends a turn the person stopped.
func (r *agentRun) interrupted() {
	r.t.Print("\r\n" + agentGrey + "  ⎿  Interrupted by user" + reset + "\r\n")
	r.report("idle", "Interrupted", "")
}

// restHint is the line under the input while the agent waits.
const restHint = "Type the next task. ctrl+d goes back to the shell."

// rest waits at the input line for the next task, and reports false when
// the person leaves the agent.
func (r *agentRun) rest() (string, bool) {
	t := r.t
	r.a.change(t, func() { r.a.resting, r.a.rests = true, r.a.rests+1 })
	defer r.a.change(t, func() { r.a.resting = false })
	t.Print("\r\n" + agentGrey + "  " + restHint + reset + "\r\n")
	var line []rune
	draw := func() {
		cols, _ := t.Size()
		t.write("\r\x1b[K" + fit(r.p.accent+"> "+reset+string(line), max(cols-1, 1)))
	}
	draw()
	for {
		b, resized, ok := r.next()
		if !ok {
			return "", false
		}
		if resized {
			t.repaint("")
			draw()
			continue
		}
		for i := 0; i < len(b); i++ {
			c := b[i]
			switch {
			case c == '\r' || c == '\n':
				text := strings.TrimSpace(string(line))
				if text == "" {
					continue
				}
				r.pending = append(r.pending[:0], b[i+1:]...)
				t.write("\r\x1b[K")
				if text == "/exit" || text == "exit" {
					return "", false
				}
				return text, true
			case c == 0x03 || c == 0x04:
				if len(line) == 0 {
					t.write("\r\x1b[K")
					return "", false
				}
				line = line[:0]
			case c == 0x7f || c == 0x08:
				if len(line) > 0 {
					line = line[:len(line)-1]
				}
			case c == 0x1b:
				// An escape sequence moves nothing here: skip it whole.
				if i+1 < len(b) && (b[i+1] == '[' || b[i+1] == 'O') {
					i += 2
					for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
						i++
					}
				} else {
					line = line[:0]
				}
			case c >= 0x20:
				ru, size := utf8.DecodeRune(b[i:])
				line = append(line, ru)
				i += size - 1
			}
		}
		draw()
	}
}

// pause waits, and reports false when Ctrl+C or Esc interrupted it.
func (r *agentRun) pause(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return true
		case <-r.t.Resized():
			if r.t.relaidNeeded() {
				r.t.repaint("")
			}
		case b, ok := <-r.t.In:
			if !ok {
				return false
			}
			for _, c := range b {
				if c == 0x03 || c == 0x1b {
					return false
				}
			}
		}
	}
}

// claudeTurn adds a dark mode toggle to the website, and asks before it
// edits style.css.
func claudeTurn(r *agentRun, _ string) bool {
	if !r.read("projects/website/index.html", "Read 4 lines") || !r.read("projects/website/style.css", "Read 1 line") {
		return false
	}
	if !r.think(28, "The page has no theme switch. It needs a button and a dark palette.") {
		return false
	}
	r.say("I'll add a toggle button to index.html and a dark theme to style.css.")
	styleDiff := AgentDiff{Hunks: []AgentHunk{{OldStart: 1, NewStart: 1, Lines: []AgentDiffLine{
		{" ", "h1 { color: hotpink; }"},
		{"+", ""},
		{"+", "[data-theme=dark] { background: #11111b; color: #cdd6f4; }"},
		{"+", "button.theme { position: fixed; top: 1rem; right: 1rem; }"},
	}}}}
	approved, ok := r.edit("projects/website/style.css", styleDiff, styleDark, true)
	if !ok {
		return false
	}
	if !approved {
		r.say("I left style.css as it was. Tell me what to do differently.")
		r.report("idle", "Waiting for your note", "")
		return true
	}
	indexDiff := AgentDiff{Hunks: []AgentHunk{{OldStart: 3, NewStart: 3, Lines: []AgentDiffLine{
		{" ", `<link rel="stylesheet" href="style.css">`},
		{"+", `<button class="theme" onclick="document.body.dataset.theme ^= 'dark'">dark mode</button>`},
		{" ", "<h1>it works</h1>"},
	}}}}
	if _, ok := r.edit("projects/website/index.html", indexDiff, indexDark, false); !ok {
		return false
	}
	r.say("Done. The site has a dark mode toggle now. Try cat projects/website/style.css in a shell.")
	r.finish("Added a dark mode toggle")
	return true
}

// darkPalettes are the colours geminiTurn offers.
var darkPalettes = []struct{ name, bg, fg, accent string }{
	{"Catppuccin Mocha", "#1e1e2e", "#cdd6f4", "#cba6f7"},
	{"Tokyo Night", "#1a1b26", "#c0caf5", "#7aa2f7"},
	{"Gruvbox Dark", "#282828", "#ebdbb2", "#fabd2f"},
}

// geminiTurn picks the colours of the dark theme, and asks which ones.
func geminiTurn(r *agentRun, _ string) bool {
	if !r.read("projects/website/style.css", "Read 1 line") || !r.think(14, "") {
		return false
	}
	r.say("The site has no dark palette yet. I can write one of three.")
	names := make([]string, len(darkPalettes))
	for i, p := range darkPalettes {
		names[i] = p.name
	}
	choice := r.ask("Which colors should the dark theme use?", names)
	if choice < 0 {
		return false
	}
	p := darkPalettes[choice]
	r.report("working", "Writing theme.css", "")
	css := []string{
		"/* " + p.name + " */",
		"[data-theme=dark] {",
		"  --bg: " + p.bg + ";",
		"  --fg: " + p.fg + ";",
		"  --accent: " + p.accent + ";",
		"}",
	}
	d := AgentDiff{Hunks: []AgentHunk{{OldStart: 0, NewStart: 1}}}
	for _, l := range css {
		d.Hunks[0].Lines = append(d.Hunks[0].Lines, AgentDiffLine{"+", l})
	}
	if _, ok := r.edit("projects/website/theme.css", d, strings.Join(css, "\n")+"\n", false); !ok {
		return false
	}
	r.say("Done. theme.css has the " + p.name + " colors.")
	r.finish("Wrote the " + p.name + " colors")
	return true
}

// codexTurn adds a test to the Go project, and runs go vet.
func codexTurn(r *agentRun, _ string) bool {
	if !r.read("projects/hello/greet.go", "Read greet.go") || !r.read("projects/hello/greet_test.go", "Read greet_test.go") {
		return false
	}
	if !r.think(12, "") {
		return false
	}
	d := AgentDiff{Hunks: []AgentHunk{{OldStart: 8, NewStart: 8, Lines: []AgentDiffLine{
		{" ", "}"},
		{"+", ""},
		{"+", "func TestGreetName(t *testing.T) {"},
		{"+", `	if got := greet("gopher"); got != "hello, gopher" {`},
		{"+", `		t.Fatalf("greet = %q", got)`},
		{"+", "	}"},
		{"+", "}"},
	}}}}
	if _, ok := r.edit("projects/hello/greet_test.go", d, "", false); !ok {
		return false
	}
	if !r.shell("go vet ./...", "(no output)") {
		return false
	}
	r.say("I added TestGreetName to greet_test.go. go vet finds no problems.")
	r.finish("Added a test for greet")
	return true
}

// planSteps are the lines genericTurn writes, a few of them for each task.
var planSteps = []string{
	"Find the files the change touches.",
	"Keep the change small, one thing at a time.",
	"Add a test that fails without the change.",
	"Update the README where it says how it works.",
	"Run the tests and the build.",
	"List what is left for a later change.",
	"Ask before anything that deletes a file.",
}

// genericTurn is the first turn for a task the agent has no script for: it
// reads the README, thinks, and writes a short plan for the task to a notes
// file. Each pane picks its own steps, so the attempts of a fan differ.
func genericTurn(r *agentRun, task string) bool {
	if !r.read("projects/website/index.html", "Read 4 lines") || !r.think(16, "") {
		return false
	}
	var hash uint32
	for _, c := range r.a.Window + task {
		hash = hash*31 + uint32(c)
	}
	seed := int(hash % 1000)
	slug := strings.ReplaceAll(agentTitle(task), " ", "-")
	file := "projects/website/notes/" + slug + ".md"
	lines := []string{"# " + task, ""}
	n := 3 + seed%3
	for i := range n {
		lines = append(lines, "- "+planSteps[(seed/3+i*2)%len(planSteps)])
	}
	d := AgentDiff{Hunks: []AgentHunk{{OldStart: 0, NewStart: 1}}}
	for _, l := range lines {
		d.Hunks[0].Lines = append(d.Hunks[0].Lines, AgentDiffLine{"+", l})
	}
	r.say("I'll write down the steps first, in " + baseName(file) + ".")
	if _, ok := r.edit(file, d, "", false); !ok {
		return false
	}
	r.say("Done. " + baseName(file) + " has a plan in " + strconv.Itoa(n) + " steps.")
	r.finish("Wrote a plan in " + strconv.Itoa(n) + " steps")
	return true
}

// diffLines draws a diff as the edit box shows it: the line number, the
// sign and the text, added lines in green.
func diffLines(d AgentDiff) []string {
	var out []string
	for _, h := range d.Hunks {
		n := max(h.NewStart, 1)
		for _, l := range h.Lines {
			num := strconv.Itoa(n)
			num = strings.Repeat(" ", max(3-len(num), 0)) + num
			switch l.Op {
			case "+":
				out = append(out, green+num+" + "+l.Text+reset)
				n++
			case "-":
				out = append(out, red+"    - "+l.Text+reset)
			default:
				out = append(out, num+"   "+l.Text)
				n++
			}
		}
	}
	return out
}

// baseName is the last element of a slash-separated path.
func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// stripEscapes removes the CSI escapes from s.
func stripEscapes(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			j := i + 1
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e || s[j] == '[') {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// agentWidth is the width of the agent's boxes in a pane cols cells wide:
// the pane, up to 60 cells.
func agentWidth(cols int) int { return max(min(cols, 60), 4) }

// inkBoxLine draws one row of a box in ink, padded to width w.
func inkBoxLine(ink string, w int, content string) string {
	return ink + "│" + reset + " " + padVisible(content, w-4) + " " + ink + "│" + reset
}

// padVisible pads or cuts a string with escapes in it to width visible cells.
func padVisible(s string, width int) string {
	n := visibleLen(s)
	if n <= width {
		return s + strings.Repeat(" ", width-n)
	}
	// Cut: walk the runes, copying escapes whole, until width cells are out.
	var b strings.Builder
	cells := 0
	for i := 0; i < len(s) && cells < width; {
		if s[i] == 0x1b {
			j := i + 1
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e || s[j] == '[') {
				j++
			}
			b.WriteString(s[i:min(j+1, len(s))])
			i = j + 1
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		b.WriteString(s[i : i+size])
		i += size
		cells++
	}
	return b.String() + reset
}

// visibleLen counts the cells a string takes, skipping CSI escapes.
func visibleLen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e || s[j] == '[') {
				j++
			}
			i = j + 1
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n++
	}
	return n
}
