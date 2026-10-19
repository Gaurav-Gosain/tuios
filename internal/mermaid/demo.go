package mermaid

import (
	"strings"
	"time"
)

// A Keystroke is one moment of a typing demo: the text in the editor, where
// the caret is, and how long it stays before the next one.
type Keystroke struct {
	Text   string
	Caret  int // in bytes of Text
	Hold   time.Duration
	Commit bool // the end of a step: the whole step is typed
	// Target is the step being typed, for Preview.
	Target string
}

// DemoSteps is the diagram the demo types when it is given no file: pele's
// launch video starts the same way, then this one adds a subgraph and colours.
var DemoSteps = []string{
	"flowchart LR",
	"flowchart LR\n  Idea --> Build",
	"flowchart LR\n  Idea --> Build\n  Build --> Review{Ready?}",
	"flowchart LR\n  Idea --> Build\n  Build --> Review{Ready?}\n  Review -->|Not yet| Build",
	"flowchart LR\n  Idea --> Build\n  Build --> Review{Ready?}\n  Review -->|Not yet| Build\n  Review -->|Yes| Launch([Launch])",
	"flowchart LR\n  Idea --> Build\n  Build --> Review{Ready?}\n  Review -->|Not yet| Build\n  Review -->|Yes| Launch([Launch])\n  Launch --> Users[(Users)]\n  Users -.->|feedback| Idea",
	"flowchart LR\n  subgraph Make\n  Idea --> Build\n  Build --> Review{Ready?}\n  Review -->|Not yet| Build\n  Review -->|Yes| Launch([Launch])\n  Launch --> Users[(Users)]\n  Users -.->|feedback| Idea",
	"flowchart LR\n  subgraph Make\n  Idea --> Build\n  Build --> Review{Ready?}\n  Review -->|Not yet| Build\n  end\n  Review -->|Yes| Launch([Launch])\n  Launch --> Users[(Users)]\n  Users -.->|feedback| Idea",
	"flowchart LR\n  subgraph Make\n  Idea --> Build\n  Build --> Review{Ready?}\n  Review -->|Not yet| Build\n  end\n  Review -->|Yes| Launch([Launch])\n  Launch --> Users[(Users)]\n  Users -.->|feedback| Idea\n  classDef go fill:#879a39,stroke:#66800b,color:#fffcf0\n  class Launch go",
}

// MaxDemoStep is the longest step Type is given: each keystroke holds the
// text typed so far, so a step of n characters costs about n*n/2 bytes, here
// at most 32 MB.
const MaxDemoStep = 8000

// Typing timings, from pele's video: a character every 24 ms, 300 ms on a
// finished step, 400 ms on the first.
const (
	CharDelay = 24 * time.Millisecond
	StepHold  = 300 * time.Millisecond
	FirstHold = 400 * time.Millisecond
)

// Type turns steps, each the whole source at that point, into keystrokes. From
// one step to the next it deletes what changed and types the new text a
// character at a time, the way pele's video does: the text that two steps
// share at their start and end stays, and the indentation of a new line comes
// at once.
func Type(steps []string) []Keystroke {
	if len(steps) == 0 {
		return nil
	}
	src := steps[0]
	out := []Keystroke{{Text: src, Caret: len(src), Hold: FirstHold, Commit: true}}
	for _, target := range steps[1:] {
		prefix := 0
		for prefix < len(src) && prefix < len(target) && src[prefix] == target[prefix] {
			prefix++
		}
		suffix := 0
		for suffix < len(src)-prefix && suffix < len(target)-prefix && src[len(src)-suffix-1] == target[len(target)-suffix-1] {
			suffix++
		}
		// A new line is typed the way a person adds one: at the end of the
		// line before, Enter first. Without this, a line put in above another
		// is typed onto the front of it ("subgIdea --> Build").
		if k := len(target) - len(src); k > 0 && len(src)-suffix == prefix && strings.Contains(target[prefix:prefix+k], "\n") {
			for p := prefix; p >= 0; p-- {
				if target[p] == '\n' && src[p:] == target[p+k:] {
					prefix, suffix = p, len(src)-p
					break
				}
			}
		}
		tail := src[len(src)-suffix:]
		if len(src)-suffix > prefix {
			out = append(out, Keystroke{Text: src[:prefix] + tail, Caret: prefix, Target: target})
		}
		for end := prefix + 1; end <= len(target)-suffix; end++ {
			lineStart := strings.LastIndexByte(target[:end-1], '\n') + 1
			if strings.TrimLeft(target[lineStart:end], " \t") == "" {
				for end < len(target)-suffix && (target[end] == ' ' || target[end] == '\t') {
					end++
				}
			}
			// Bytes of one character arrive together.
			for end < len(target)-suffix && end > 0 && target[end]&0xc0 == 0x80 {
				end++
			}
			out = append(out, Keystroke{Text: target[:end] + tail, Caret: end, Hold: CharDelay, Target: target})
		}
		src = target
		out = append(out, Keystroke{Text: src, Caret: len(src) - suffix, Hold: StepHold, Commit: true, Target: target})
	}
	return out
}

// Preview is the text to draw for a keystroke, as pele's video draws a
// flowchart being typed. The id under the caret is completed to the one id of
// the step it is the start of, so a node does not appear as "Rev" and then
// turn into "Review". A line that has no arrow yet is left out, so a node is
// not drawn on its own before it is joined to the rest. Brackets and quotes
// are closed.
func Preview(k Keystroke) string {
	text, caret := k.Text, min(k.Caret, len(k.Text))
	if k.Commit || k.Target == "" {
		return Balanced(text)
	}
	start := caret
	for start > 0 && isIDByte(text[start-1]) {
		start--
	}
	if word := text[start:caret]; word != "" && (caret == len(text) || !isIDByte(text[caret])) {
		var match string
		n := 0
		for _, id := range ids(k.Target) {
			if strings.HasPrefix(id, word) {
				if id != match {
					n++
				}
				match = id
			}
		}
		if n == 1 && match != word {
			text = text[:caret] + match[len(word):] + text[caret:]
		}
	}
	lineStart := strings.LastIndexByte(text[:caret], '\n') + 1
	lineEnd := strings.IndexByte(text[caret:], '\n')
	if lineEnd < 0 {
		lineEnd = len(text)
	} else {
		lineEnd += caret
	}
	line := strings.TrimSpace(text[lineStart:lineEnd])
	if lineStart > 0 && line != "" && !hasArrow(line) && !keywordLine(line) {
		text = text[:lineStart] + text[min(lineEnd+1, len(text)):]
		text = strings.TrimRight(text, "\n")
	}
	return Balanced(text)
}

func isIDByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// ids lists the words of a source outside labels: [..], {..}, (..), |..| and quotes.
func ids(src string) []string {
	var out []string
	depth, pipe, quote := 0, false, false
	start := -1
	flush := func(i int) {
		if start >= 0 {
			out = append(out, src[start:i])
			start = -1
		}
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '"':
			flush(i)
			quote = !quote
		case quote:
		case c == '[' || c == '{' || c == '(':
			flush(i)
			depth++
		case c == ']' || c == '}' || c == ')':
			depth = max(0, depth-1)
		case c == '|':
			flush(i)
			pipe = !pipe
		case depth == 0 && !pipe && isIDByte(c):
			if start < 0 {
				start = i
			}
		default:
			flush(i)
		}
	}
	flush(len(src))
	return out
}

func hasArrow(line string) bool {
	return strings.Contains(line, "--") || strings.Contains(line, "==") || strings.Contains(line, "-.") || strings.Contains(line, "~~")
}

func keywordLine(line string) bool {
	for _, k := range []string{"flowchart", "graph", "subgraph", "end", "classDef", "class ", "style", "linkStyle", "%%"} {
		if strings.HasPrefix(line, k) {
			return true
		}
	}
	return false
}

// Balanced closes the brackets and the quote a half-typed line leaves open, so
// that the diagram can be drawn while a label is typed, as an editor that
// closes brackets would show it.
func Balanced(src string) string {
	var stack []byte
	closers := map[byte]byte{'[': ']', '{': '}', '(': ')'}
	quote := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == '"' {
			quote = !quote
		}
		if quote {
			continue
		}
		if cl, ok := closers[c]; ok {
			stack = append(stack, cl)
		} else if len(stack) > 0 && c == stack[len(stack)-1] {
			stack = stack[:len(stack)-1]
		}
	}
	out := src
	if quote {
		out += `"`
	}
	for i := len(stack) - 1; i >= 0; i-- {
		out += string(stack[i])
	}
	return out
}
