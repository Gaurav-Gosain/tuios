package webshell

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// A scripted coding agent in the style of Claude Code. It reads two files,
// thinks, proposes an edit, asks for approval with three options, and applies
// the edit or not. It draws what the claude-code harness manifest keys on (the
// spinner line and the "Do you want" menu), and it reports each state to tuios
// the way a hooked agent does, so the rail, the title glyph and the alerts all
// react to it.

// AgentHarness is the harness id the fake agent reports as.
const AgentHarness = "claude-code"

const (
	agentOrange = "\x1b[38;5;209m"
	agentGrey   = "\x1b[38;5;245m"
)

var agentSpinner = []string{"·", "✢", "✳", "✶", "✻", "✽", "✻", "✶", "✳", "✢"}

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

// report tells tuios what state this pane's agent is in. See
// app.ReportAgentState, which the browser build routes this to.
func (t *TTY) report(state, message, kind string) {
	t.Emit(EventAgentReport, map[string]any{
		"state": state, "message": message, "kind": kind, "harness": AgentHarness,
	})
}

func cmdAgent(s *shell, args []string, _ string) int {
	t := s.t
	task := strings.Join(args[1:], " ")
	if task == "" {
		task = "add a dark mode toggle to the website"
	}
	cols, _ := t.Size()
	w := min(max(cols-2, 30), 60)

	t.Print(agentOrange + "╭" + strings.Repeat("─", w-2) + "╮" + reset + "\r\n")
	t.Print(boxLine(w, agentOrange+"✻"+reset+" Welcome to "+bold+"Claude Code"+reset+" (tuios demo)"))
	t.Print(boxLine(w, ""))
	t.Print(boxLine(w, agentGrey+"  A pretend agent. Nothing leaves this tab."+reset))
	t.Print(agentOrange + "╰" + strings.Repeat("─", w-2) + "╯" + reset + "\r\n\r\n")
	t.Print(agentGrey + "> " + reset + task + "\r\n\r\n")

	t.report("working", "Reading the website", "")
	for _, f := range []struct{ name, note string }{
		{"projects/website/index.html", "Read 4 lines"},
		{"projects/website/style.css", "Read 1 line"},
	} {
		t.Print(green + "●" + reset + " " + bold + "Read" + reset + "(" + f.name + ")\r\n")
		t.Print(agentGrey + "  ⎿  " + f.note + reset + "\r\n")
		if !agentPause(t, 450*time.Millisecond) {
			return agentInterrupted(t)
		}
	}
	t.Print("\r\n")

	t.report("working", "Thinking", "")
	start := time.Now()
	for i := 0; i < 28; i++ {
		secs := int(time.Since(start).Seconds())
		t.Print("\r\x1b[K" + agentOrange + agentSpinner[i%len(agentSpinner)] + reset + " " +
			agentOrange + "Thinking…" + reset + agentGrey + " (" + strconv.Itoa(secs) + "s · esc to interrupt)" + reset)
		if !agentPause(t, 110*time.Millisecond) {
			t.Print("\r\n")
			return agentInterrupted(t)
		}
	}
	t.Print("\r\x1b[K")
	t.Print(bold + "●" + reset + " I'll add a toggle button to index.html and a dark theme to style.css.\r\n\r\n")
	if !agentPause(t, 300*time.Millisecond) {
		return agentInterrupted(t)
	}

	t.Print(green + "●" + reset + " " + bold + "Update" + reset + "(projects/website/style.css)\r\n")
	t.Print(agentGrey + "╭" + strings.Repeat("─", w-2) + "╮" + reset + "\r\n")
	t.Print(greyBoxLine(w, bold+"Edit file"+reset))
	t.Print(greyBoxLine(w, agentGrey+"projects/website/style.css"+reset))
	t.Print(greyBoxLine(w, ""))
	for _, l := range []string{
		"  1   h1 { color: hotpink; }",
		green + "  2 + [data-theme=dark] { background: #11111b; }" + reset,
		green + "  3 + button.theme { position: fixed; }" + reset,
	} {
		t.Print(greyBoxLine(w, l))
	}
	t.Print(greyBoxLine(w, ""))
	t.Print(greyBoxLine(w, "Do you want to make this edit to style.css?"))
	options := []string{
		"Yes",
		"Yes, and don't ask again this session",
		"No, and tell Claude what to do differently (esc)",
	}
	sel := 0
	drawMenu := func(first bool) {
		if !first {
			t.Print("\x1b[" + strconv.Itoa(len(options)+2) + "A\r")
		}
		for i, o := range options {
			if i == sel {
				t.Print(greyBoxLine(w, agentOrange+"❯ "+strconv.Itoa(i+1)+". "+o+reset))
			} else {
				t.Print(greyBoxLine(w, "  "+strconv.Itoa(i+1)+". "+o))
			}
		}
		t.Print(greyBoxLine(w, ""))
		t.Print(agentGrey + "╰" + strings.Repeat("─", w-2) + "╯" + reset + "\r\n")
	}
	drawMenu(true)
	t.report("needs_input", "Edit style.css?", "approval")
	// A bell is how a real agent asks for attention.
	t.Print("\a")

	choice := -1
	for choice < 0 {
		b, ok := <-t.In
		if !ok {
			return 0
		}
		for i := 0; i < len(b) && choice < 0; i++ {
			switch c := b[i]; {
			case c == 0x1b && i+2 < len(b) && b[i+1] == '[' && (b[i+2] == 'A' || b[i+2] == 'B'):
				if b[i+2] == 'A' {
					sel = (sel + len(options) - 1) % len(options)
				} else {
					sel = (sel + 1) % len(options)
				}
				i += 2
				drawMenu(false)
			case c == 0x1b || c == 'n' || c == 'N' || c == '3' || c == 0x03:
				choice = 2
			case c == 'y' || c == 'Y' || c == '1':
				choice = 0
			case c == '2':
				choice = 1
			case c == '\r' || c == '\n':
				choice = sel
			case c == 'j' || c == 0x0e:
				sel = (sel + 1) % len(options)
				drawMenu(false)
			case c == 'k' || c == 0x10:
				sel = (sel + len(options) - 1) % len(options)
				drawMenu(false)
			}
		}
	}
	sel = choice
	drawMenu(false)
	t.Print("\r\n")

	if choice == 2 {
		t.Print(agentGrey + "  ⎿  " + reset + "No changes made. Tell me what to do differently next time.\r\n")
		t.report("done", "Left everything as it was", "")
		return 1
	}

	t.report("working", "Editing style.css", "")
	_ = writeFile(Home+"/projects/website/style.css", styleDark, false)
	_ = writeFile(Home+"/projects/website/index.html", indexDark, false)
	t.Print(agentGrey + "  ⎿  " + reset + "Updated style.css with " + green + "2 additions" + reset + "\r\n")
	if !agentPause(t, 400*time.Millisecond) {
		return agentInterrupted(t)
	}
	t.Print(green + "●" + reset + " " + bold + "Update" + reset + "(projects/website/index.html)\r\n")
	t.Print(agentGrey + "  ⎿  " + reset + "Updated index.html with " + green + "1 addition" + reset + "\r\n\r\n")
	if !agentPause(t, 300*time.Millisecond) {
		return agentInterrupted(t)
	}
	t.Print(bold + "●" + reset + " Done. The site has a dark mode toggle now. Try " + bold + "cat projects/website/style.css" + reset + "\r\n")
	t.report("done", "Added a dark mode toggle", "")
	return 0
}

// agentPause waits, and reports false when Ctrl+C or Esc interrupted it.
func agentPause(t *TTY, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return true
		case b, ok := <-t.In:
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

func agentInterrupted(t *TTY) int {
	t.Print("\r\n" + agentGrey + "  ⎿  Interrupted by user" + reset + "\r\n")
	t.report("idle", "Interrupted", "")
	return 130
}

// boxLine draws one row of the orange welcome box, padded to width w.
func boxLine(w int, content string) string {
	return agentOrange + "│" + reset + " " + padVisible(content, w-4) + " " + agentOrange + "│" + reset + "\r\n"
}

// greyBoxLine draws one row of the grey edit box.
func greyBoxLine(w int, content string) string {
	return agentGrey + "│" + reset + " " + padVisible(content, w-4) + " " + agentGrey + "│" + reset + "\r\n"
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
