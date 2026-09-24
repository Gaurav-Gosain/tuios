package review

import (
	"bufio"
	"io"
	"strings"
)

// unifiedReader fills in the hunks of a listing from git's unified diff of
// the same files, in the same order: git writes both from one comparison,
// so the n-th "diff --git" block is the n-th file of the listing. Matching by
// order rather than by the paths in the block headers means no quoting rule
// or prefix setting of the repository changes which file a hunk lands in.
type unifiedReader struct {
	files []File
	lim   Limits

	idx       int
	cur       *File
	inHunk    bool
	oldLeft   int
	newLeft   int
	oldLine   int
	newLine   int
	fileLines int
	skipping  bool
	total     int
	truncated bool
}

// readUnified reads r into files and reports whether any file was truncated
// and whether it stopped before the end of r. Past lim.Files, or once
// lim.Bytes of text is read, every file left keeps its counts only; a file
// past lim.LinesPerFile does too.
func readUnified(r io.Reader, files []File, lim Limits) (truncated, stopped bool, err error) {
	u := &unifiedReader{files: files, lim: lim, idx: -1}
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		raw, rerr := br.ReadString('\n')
		if raw != "" {
			if stop := u.line(raw); stop {
				return u.truncated, true, nil
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return u.truncated, false, nil
			}
			return u.truncated, false, rerr
		}
	}
}

// cutRest leaves every file from i on with its counts only.
func (u *unifiedReader) cutRest(i int) {
	for j := i; j < len(u.files); j++ {
		if !u.files[j].Binary {
			u.files[j].Truncated = true
			u.files[j].Hunks = nil
			u.truncated = true
		}
	}
}

// line takes one line of the diff, and reports whether reading stops.
func (u *unifiedReader) line(raw string) bool {
	u.total += len(raw)
	text := strings.TrimSuffix(raw, "\n")
	text = strings.TrimSuffix(text, "\r")

	if !u.inHunk && strings.HasPrefix(text, "diff --git ") {
		u.idx++
		if u.idx >= len(u.files) {
			return true
		}
		if u.idx >= u.lim.Files || u.total > u.lim.Bytes {
			u.cutRest(u.idx)
			return true
		}
		u.cur = &u.files[u.idx]
		u.fileLines = 0
		u.skipping = u.cur.Binary
		return false
	}
	if u.cur == nil {
		return false
	}
	if u.total > u.lim.Bytes {
		u.cutRest(u.idx)
		return true
	}

	if !u.inHunk {
		switch {
		case strings.HasPrefix(text, "@@ "):
			os, ol, ns, nl, err := ParseHunkHeader(text)
			if err != nil {
				return false
			}
			u.inHunk = ol > 0 || nl > 0
			u.oldLeft, u.newLeft = ol, nl
			u.oldLine, u.newLine = os, ns
			if !u.skipping {
				u.cur.Hunks = append(u.cur.Hunks, Hunk{Header: text, OldStart: os, OldLines: ol, NewStart: ns, NewLines: nl, Lines: []Line{}})
			}
		case strings.HasPrefix(text, "\\"):
			u.markNoNewline()
		case strings.HasPrefix(text, "Binary files ") || text == "GIT binary patch":
			u.cur.Binary = true
			u.cur.Hunks = nil
			u.skipping = true
		}
		return false
	}

	var op string
	switch {
	case strings.HasPrefix(text, "\\"):
		u.markNoNewline()
		return false
	case strings.HasPrefix(text, "-"):
		op = OpDelete
	case strings.HasPrefix(text, "+"):
		op = OpAdd
	default:
		op = OpContext
	}
	body := text
	if body != "" {
		// A context line starts with a space; an empty one may have lost
		// it (diff.suppressBlankEmpty).
		body = body[1:]
	}
	if !u.skipping {
		u.fileLines++
		if u.fileLines > u.lim.LinesPerFile {
			u.cur.Truncated = true
			u.cur.Hunks = nil
			u.truncated = true
			u.skipping = true
		}
	}
	ln := Line{Op: op, Text: body}
	switch op {
	case OpDelete:
		ln.Old = u.oldLine
		u.oldLine++
		u.oldLeft--
	case OpAdd:
		ln.New = u.newLine
		u.newLine++
		u.newLeft--
	default:
		ln.Old, ln.New = u.oldLine, u.newLine
		u.oldLine++
		u.newLine++
		u.oldLeft--
		u.newLeft--
	}
	if !u.skipping {
		h := &u.cur.Hunks[len(u.cur.Hunks)-1]
		h.Lines = append(h.Lines, ln)
	}
	if u.oldLeft <= 0 && u.newLeft <= 0 {
		u.inHunk = false
	}
	return false
}

// markNoNewline marks the line a "\ No newline at end of file" marker
// follows, which is always the last line read into the file.
func (u *unifiedReader) markNoNewline() {
	if u.skipping || u.cur == nil || len(u.cur.Hunks) == 0 {
		return
	}
	h := &u.cur.Hunks[len(u.cur.Hunks)-1]
	if len(h.Lines) > 0 {
		h.Lines[len(h.Lines)-1].NoNewline = true
	}
}
