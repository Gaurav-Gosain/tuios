package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// MultiCopyPane is one pane's share of a multi copy mode yank: where it came
// from and exactly the lines its selection covered.
type MultiCopyPane struct {
	// Index is the pane's position in the session's window list, the same
	// number `tuios list-windows --json` reports as "index".
	Index    int
	WindowID string
	// Title is the pane's display name as the pane reported it. It is
	// untrusted: a remote shell sets it with an escape sequence, so every
	// format that prints it cleans it first. See sanitizeMultiCopyTitle.
	Title string
	Lines []string
}

// multiCopyTitleMax caps a title in a header. A title is a label; one that
// runs to kilobytes is either a mistake or an attempt to bury the content.
const multiCopyTitleMax = 200

// sanitizeMultiCopyTitle makes an untrusted pane title safe to print as a
// label in a Markdown header or a JSON string.
//
// It removes every control character (a newline in a title would end the
// Markdown header and let the title write its own document body), every
// bidirectional and zero-width formatting rune (which make a title read as
// something other than what it is), and caps the length. JSON escaping is
// the encoder's job and happens on top of this.
func sanitizeMultiCopyTitle(title string) string {
	var b strings.Builder
	n := 0
	for _, r := range title {
		if n >= multiCopyTitleMax {
			break
		}
		switch {
		case r == '\t':
			r = ' '
		case unicode.IsControl(r), session.InvisibleFormatRune(r):
			continue
		case unicode.Is(unicode.Cf, r):
			// Any other format character (soft hyphen, tag characters, the
			// interlinear annotation marks) is invisible too.
			continue
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// multiCopyFormatExt is the file extension a saved yank takes in each format.
func multiCopyFormatExt(format string) string {
	switch format {
	case config.MultiCopyFormatMarkdown:
		return "md"
	case config.MultiCopyFormatJSON:
		return "json"
	}
	return "txt"
}

// nextMultiCopyFormat is the format after f in the cycle the format key steps
// through.
func nextMultiCopyFormat(f string) string {
	for i, v := range config.MultiCopyFormats {
		if v == f {
			return config.MultiCopyFormats[(i+1)%len(config.MultiCopyFormats)]
		}
	}
	return config.MultiCopyFormats[0]
}

// FormatMultiCopy renders the panes' selections as one block of text.
//
//   - plain: each pane's lines as they are, one pane after another, each pane
//     ending with a newline. Nothing is added, for output that already carries
//     its own identifier.
//   - markdown: a header per pane naming it, then its lines in a fenced block.
//   - json: an array of {"pane", "window_id", "title", "lines"} objects.
//
// An unknown format is treated as plain.
func FormatMultiCopy(format string, panes []MultiCopyPane) string {
	switch format {
	case config.MultiCopyFormatMarkdown:
		return formatMultiCopyMarkdown(panes)
	case config.MultiCopyFormatJSON:
		return formatMultiCopyJSON(panes)
	}
	var b strings.Builder
	for _, p := range panes {
		for _, l := range p.Lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func formatMultiCopyMarkdown(panes []MultiCopyPane) string {
	var b strings.Builder
	for i, p := range panes {
		if i > 0 {
			b.WriteByte('\n')
		}
		title := sanitizeMultiCopyTitle(p.Title)
		if title == "" {
			fmt.Fprintf(&b, "## Pane %d\n\n", p.Index)
		} else {
			fmt.Fprintf(&b, "## Pane %d: %s\n\n", p.Index, title)
		}
		// The fence has to be longer than any run of backticks in the text, or
		// a line of the pane's output could close it early.
		fence := strings.Repeat("`", max(3, longestBacktickRun(p.Lines)+1))
		b.WriteString(fence)
		b.WriteByte('\n')
		for _, l := range p.Lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		b.WriteString(fence)
		b.WriteByte('\n')
	}
	return b.String()
}

func longestBacktickRun(lines []string) int {
	best := 0
	for _, l := range lines {
		run := 0
		for _, r := range l {
			if r == '`' {
				run++
				best = max(best, run)
			} else {
				run = 0
			}
		}
	}
	return best
}

// multiCopyJSONPane is the shape of one element of the json format.
type multiCopyJSONPane struct {
	Pane     int      `json:"pane"`
	WindowID string   `json:"window_id"`
	Title    string   `json:"title"`
	Lines    []string `json:"lines"`
}

func formatMultiCopyJSON(panes []MultiCopyPane) string {
	out := make([]multiCopyJSONPane, 0, len(panes))
	for _, p := range panes {
		lines := p.Lines
		if lines == nil {
			lines = []string{}
		}
		out = append(out, multiCopyJSONPane{
			Pane: p.Index, WindowID: p.WindowID,
			Title: sanitizeMultiCopyTitle(p.Title), Lines: lines,
		})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// The text goes to a clipboard or a file, not into HTML: "<" stays "<".
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		// Strings and ints cannot fail to encode.
		return "[]\n"
	}
	return buf.String()
}
