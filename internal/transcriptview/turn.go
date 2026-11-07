package transcriptview

import (
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/plural"
)

// Turns: a thread read as the person's prompts and the work each one set
// off. A turn is a prompt and every entry after it up to the next prompt. A
// finished turn folds its work, the tool calls, the thinking and the words in
// between, to one sentence such as "Edited 3 files, ran 4 commands and read 6
// files". The prompt and the final answer always show.
//
// This part draws nothing. The TUI's thread and guios both fold turns with
// it, so they fold the same turns and say the same sentence.

// Action is what a tool call did, as a turn's summary counts it. The order
// is the summary's: an edit is named first, then a command, a read, a search,
// and any other tool last.
type Action int

// Actions, in the summary's order.
const (
	ActionEdit Action = iota
	ActionCommand
	ActionRead
	ActionSearch
	ActionOther
	// NumActions is how many actions there are.
	NumActions
)

// ToolAction is the action of a call to tool. edit says the call carries a
// diff, which makes it an edit whatever its name.
func ToolAction(tool string, edit bool) Action {
	if edit {
		return ActionEdit
	}
	// Claude Code's names, then those of the other harnesses whose
	// transcripts this package reads.
	switch strings.ToLower(tool) {
	case "edit", "multiedit", "write", "notebookedit",
		"apply_patch", "replace", "write_file", "edit_file", "search_replace", "create", "str_replace":
		return ActionEdit
	case "bash", "shell", "exec_command", "local_shell", "run_shell_command", "run_terminal_cmd", "powershell":
		return ActionCommand
	case "read", "ls", "notebookread",
		"read_file", "read_many_files", "list_directory", "view", "view_image":
		return ActionRead
	case "grep", "glob", "websearch",
		"grep_search", "search_file_content", "google_web_search", "web_search":
		return ActionSearch
	}
	return ActionOther
}

// Step is what the turn fold reads of one entry. A client builds it from the
// entry it holds.
type Step struct {
	Role, Kind, Tool, Status string
	// Text is read only to tell a user entry that marks an interrupt from a
	// prompt.
	Text string
	// Edit says the call carries a diff.
	Edit bool
	At   int64
}

// interrupted reports whether a user entry marks an interrupt rather than a
// prompt.
func (s *Step) interrupted() bool {
	return Interrupt(s.Role, s.Kind, s.Text)
}

// Interrupt reports whether an entry marks where the person stopped the
// agent: an interrupt entry, or, from a daemon older than that kind, the text
// Claude Code writes for it as the person's words.
func Interrupt(role, kind, text string) bool {
	return kind == KindInterrupt || role == RoleUser && kind == KindText && InterruptText(text)
}

// prompt reports whether the step starts a turn.
func (s *Step) prompt() bool {
	return s.Role == RoleUser && s.Kind == KindText && !s.interrupted()
}

// Turn is a run of steps: a prompt and the work after it.
type Turn struct {
	// Start and End bound the turn's steps, steps[Start:End]. Start is the
	// prompt, or the first step for a thread that starts inside a turn.
	Start, End int
	// Answer is the final answer, the last assistant text, -1 for none.
	Answer int
	// Finished says the work is over: a later prompt exists, or the agent
	// rests and the turn ends on its answer.
	Finished bool
	// Failed counts the failed calls and the errors. Interrupted says the
	// person stopped the turn, and Declined counts the calls they declined.
	Failed      int
	Interrupted bool
	Declined    int
	// Counts are the tool calls by action, and Thoughts the thinking.
	Counts   [NumActions]int
	Thoughts int
	// StartAt and EndAt are the times of the first and the last step, in
	// Unix milliseconds, 0 when a step has none.
	StartAt, EndAt int64
}

// Folds reports whether the turn folds when the person has not opened or
// closed it: it is finished, nothing in it failed, and it has work to fold.
func (t *Turn) Folds() bool {
	return t.Finished && t.Failed == 0 && !t.Interrupted && t.Declined == 0 && t.Work() > 0
}

// Work is how many tool calls and thoughts the turn holds.
func (t *Turn) Work() int {
	n := t.Thoughts
	for _, c := range t.Counts {
		n += c
	}
	return n
}

// Hidden reports whether a folded turn hides step i: its prompt, its final
// answer and a plan stay, every other step hides.
func (t *Turn) Hidden(steps []Step, i int) bool {
	return i != t.Answer && !(i == t.Start && steps[i].prompt()) && steps[i].Kind != KindPlan
}

// Turns splits steps into turns. resting says the agent is not working, so
// the last turn is finished once it ends on an answer.
func Turns(steps []Step, resting bool) []Turn {
	var out []Turn
	for i := 0; i < len(steps); {
		t := Turn{Start: i, Answer: -1}
		j := i
		for ; j < len(steps) && (j == i || !steps[j].prompt()); j++ {
			s := &steps[j]
			if s.At > 0 {
				if t.StartAt == 0 {
					t.StartAt = s.At
				}
				t.EndAt = s.At
			}
			switch {
			case s.interrupted():
				t.Interrupted = true
			case s.Kind == KindError:
				t.Failed++
			case s.Kind == KindThinking:
				t.Thoughts++
			case s.Kind == KindToolCall:
				t.Counts[ToolAction(s.Tool, s.Edit)]++
				switch s.Status {
				case StatusError:
					t.Failed++
				case StatusDeclined:
					t.Declined++
				}
			case s.Kind == KindText && s.Role == RoleAssistant:
				t.Answer = j
			}
		}
		t.End = j
		if j < len(steps) {
			t.Finished = true
		} else if resting {
			// The last turn is over when its last entry, results aside, is
			// the answer, or the person stopped the agent or declined its
			// call there.
			last := j - 1
			for last > i && steps[last].Kind == KindToolResult {
				last--
			}
			s := &steps[last]
			t.Finished = last > i && (t.Answer == last || s.interrupted() || s.Kind == KindToolCall && s.Status == StatusDeclined)
		}
		out = append(out, t)
		i = j
	}
	return out
}

// Summary is the sentence that stands for a folded turn's work, such as
// "Edited 3 files, ran 4 commands and read 6 files (1 failed)". Up to three
// actions are named, by the order of Action. With more, two are named and
// the rest are "N other actions". A turn of thinking alone says so.
func (t *Turn) Summary() string {
	type part struct {
		a Action
		n int
	}
	var parts []part
	for a, n := range t.Counts {
		if n > 0 {
			parts = append(parts, part{Action(a), n})
		}
	}
	var words []string
	if len(parts) > 3 {
		rest := 0
		for _, p := range parts[2:] {
			rest += p.n
		}
		for _, p := range parts[:2] {
			words = append(words, actionWords(p.a, p.n))
		}
		words = append(words, plural.CountAs(rest, "other action", "other actions"))
	} else {
		for _, p := range parts {
			words = append(words, actionWords(p.a, p.n))
		}
	}
	var s string
	switch len(words) {
	case 0:
		if t.Thoughts == 0 {
			s = "No actions"
		} else {
			s = "Thought " + times(t.Thoughts)
		}
	case 1:
		s = words[0]
	default:
		s = strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	switch {
	case t.Failed > 0:
		s += " (" + strconv.Itoa(t.Failed) + " failed)"
	case t.Interrupted:
		s += " (stopped)"
	case t.Declined > 0:
		s += " (declined)"
	}
	return s
}

// actionWords says n calls of action a, in lower case.
func actionWords(a Action, n int) string {
	switch a {
	case ActionEdit:
		return "edited " + plural.CountAs(n, "file", "files")
	case ActionCommand:
		return "ran " + plural.CountAs(n, "command", "commands")
	case ActionRead:
		return "read " + plural.CountAs(n, "file", "files")
	case ActionSearch:
		return "searched " + times(n)
	}
	return "used " + plural.CountAs(n, "tool", "tools")
}

// times is "once" or "n times".
func times(n int) string {
	if n == 1 {
		return "once"
	}
	return strconv.Itoa(n) + " times"
}

// Span is how long the turn took, as "42s", "3m 12s" or "1h 5m", "" when the
// steps carry no times.
func (t *Turn) Span() string {
	return spanText(t.StartAt, t.EndAt)
}

// ThoughtFor is how long the agent thought before thinking step i, as Span
// says it: from the step before it. It is "" when a time is missing, and for
// a thought right after another, which the first one's time covers.
func ThoughtFor(steps []Step, i int) string {
	if i <= 0 || i >= len(steps) || steps[i].Kind != KindThinking || steps[i-1].Kind == KindThinking {
		return ""
	}
	return spanText(steps[i-1].At, steps[i].At)
}

// spanText says the time from one Unix millisecond to another, at least a
// second, "" when either is missing.
func spanText(from, to int64) string {
	if from <= 0 || to-from < 1000 {
		return ""
	}
	sec := (to - from) / 1000
	switch {
	case sec < 60:
		return strconv.FormatInt(sec, 10) + "s"
	case sec < 3600:
		return strconv.FormatInt(sec/60, 10) + "m " + strconv.FormatInt(sec%60, 10) + "s"
	}
	return strconv.FormatInt(sec/3600, 10) + "h " + strconv.FormatInt(sec%3600/60, 10) + "m"
}
