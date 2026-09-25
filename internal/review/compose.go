package review

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// QuoteMax is how many characters of a quoted line the message carries.
const QuoteMax = 120

// TextMax is the longest note, in bytes.
const TextMax = 1000

// Message is what Compose writes.
type Message struct {
	// From names the sender as the header says it: "the person", "pane
	// build", "a script".
	From string
	// Base is the base the notes were made against, empty when unknown.
	Base string
	// Notes are the notes to send, in any order.
	Notes []Note
	// CleanQuote, when set, cleans a quoted line before it is cut to
	// QuoteMax characters. The daemon passes the cleaning it gives every
	// agent-authored text, which masks what looks like a secret.
	CleanQuote func(string) string
	// SenderBy is the sender in a note's By encoding: ByHuman, ByShell, a
	// pane's window id, or ByLinkPrefix and a host. A note whose By is set
	// and differs from it is labelled with its author, so a note one caller
	// wrote never reads as the sender's words.
	SenderBy string
	// Author names a note's author for that label, from its By. When nil,
	// or when it returns "", AuthorName is used.
	Author func(by string) string
}

// Who wrote a note, in Note.By.
const (
	// ByHuman is the person, proved by the attached client's nonce.
	ByHuman = "human"
	// ByShell is a caller outside every pane.
	ByShell = "shell"
	// ByLinkPrefix, then the host, is a caller on a linked machine.
	ByLinkPrefix = "link:"
)

// AuthorName is how a message names the author by: "the person", "a
// script", "a caller on HOST", or "pane ID" for a pane's window id.
func AuthorName(by string) string {
	switch {
	case by == ByHuman:
		return "the person"
	case by == ByShell:
		return "a script"
	case strings.HasPrefix(by, ByLinkPrefix):
		host := oneLine(strings.TrimPrefix(by, ByLinkPrefix), 40)
		if host == "" || host == "*" {
			return "a caller on a linked machine"
		}
		return "a caller on " + host
	}
	return "pane " + oneLine(by, 40)
}

// SortNotes orders notes the way they are listed and sent: by path, then
// line, then when they were written, then id.
func SortNotes(notes []Note) {
	slices.SortStableFunc(notes, func(a, b Note) int {
		if c := cmp.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Line, b.Line); c != 0 {
			return c
		}
		if c := cmp.Compare(a.At, b.At); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
}

// Compose writes notes as the one message the agent receives: a header
// naming who sent them, one numbered entry per note with where it is and the
// line it quotes, and a closing request. A note written by someone other than
// the sender says who wrote it, under its place. The same notes always give
// the same text.
func Compose(m Message) string {
	notes := slices.Clone(m.Notes)
	SortNotes(notes)
	var b strings.Builder
	b.WriteString("Review notes on your changes")
	if m.Base != "" {
		fmt.Fprintf(&b, " (vs %s)", oneLine(m.Base, 80))
	}
	from := m.From
	if from == "" {
		from = "a script"
	}
	fmt.Fprintf(&b, ", from %s:\n\n", from)
	for i, n := range notes {
		fmt.Fprintf(&b, "%d. %s\n", i+1, location(n, m.CleanQuote))
		if n.By != "" && n.By != m.SenderBy {
			fmt.Fprintf(&b, "   (written by %s", m.authorName(n.By))
			if m.SenderBy == ByHuman {
				b.WriteString(", not by the person")
			}
			b.WriteString(")\n")
		}
		for line := range strings.SplitSeq(CleanText(n.Text), "\n") {
			b.WriteString("   ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	b.WriteString("\nAddress each note, then say which you changed.")
	return b.String()
}

// authorName is how the message names the author by.
func (m Message) authorName(by string) string {
	if m.Author != nil {
		if name := oneLine(m.Author(by), 80); name != "" {
			return name
		}
	}
	return AuthorName(by)
}

// location is where a note is, as its entry's first line says it.
func location(n Note, clean func(string) string) string {
	var b strings.Builder
	b.WriteString(oneLine(n.Path, 200))
	if n.IsHunk() {
		os, ol, ns, nl, err := ParseHunkHeader(n.HunkHeader)
		start, count := ns, nl
		if n.Side == SideOld {
			start, count = os, ol
		}
		if err != nil {
			start, count = n.Line, 1
		}
		if count > 1 {
			fmt.Fprintf(&b, ":%d-%d", start, start+count-1)
		} else {
			fmt.Fprintf(&b, ":%d", start)
		}
		fmt.Fprintf(&b, " (hunk %q)", hunkRange(n.HunkHeader))
	} else {
		fmt.Fprintf(&b, ":%d", n.Line)
		if n.Side == SideOld {
			b.WriteString(" (before the change)")
		}
		if n.Quote != "" {
			q := n.Quote
			if clean != nil {
				q = clean(q)
			}
			fmt.Fprintf(&b, ", on %q", oneLine(q, QuoteMax))
		}
	}
	if n.Outdated {
		b.WriteString(" (outdated: that line has changed since the note was left)")
	}
	return b.String()
}

// hunkRange is the "@@ -a,b +c,d @@" part of a hunk header, without the
// function context git adds after it.
func hunkRange(header string) string {
	if i := strings.Index(header[min(2, len(header)):], "@@"); i >= 0 {
		return header[:i+2+2]
	}
	return oneLine(header, 80)
}

// oneLine is s with its control characters left out and its runs of white
// space made one space, cut to limit characters with "..." after a cut.
func oneLine(s string, limit int) string {
	var b strings.Builder
	space := false
	n := 0
	cut := false
	for _, r := range s {
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			space = true
			continue
		case r < 0x20 || (r >= 0x7f && r < 0xa0) || r == utf8.RuneError:
			continue
		}
		if space && b.Len() > 0 {
			if n == limit {
				cut = true
				break
			}
			b.WriteByte(' ')
			n++
		}
		space = false
		if n == limit {
			cut = true
			break
		}
		b.WriteRune(r)
		n++
	}
	out := strings.TrimSpace(b.String())
	if cut {
		return out + "..."
	}
	return out
}

// CleanText is a note's text as it is kept and sent: lines kept, every other
// control character left out, a tab made a space, trailing blanks trimmed.
// Nothing in it can end the bracketed paste the message is typed in.
func CleanText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteByte('\n')
		case r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || (r >= 0x7f && r < 0xa0) || r == utf8.RuneError:
			continue
		default:
			b.WriteRune(r)
		}
	}
	lines := strings.Split(b.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n ")
}

// CleanQuote is a quoted line as it is kept: one line, control characters
// left out, at most TextMax bytes.
func CleanQuote(s string) string {
	q := oneLine(s, TextMax)
	for len(q) > TextMax {
		_, size := utf8.DecodeLastRuneInString(q)
		q = q[:len(q)-size]
	}
	return q
}
