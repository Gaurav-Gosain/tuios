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

const multiCopyTitleMax = 200

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
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

func multiCopyFormatExt(format string) string {
	switch format {
	case config.MultiCopyFormatMarkdown:
		return "md"
	case config.MultiCopyFormatJSON:
		return "json"
	}
	return "txt"
}

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
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return "[]\n"
	}
	return buf.String()
}
