package session

import (
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
)

// What an agent's own edits say about the lines a turn is credited with
// (review_origins.go). A checkpoint holds the whole work tree, so a line the
// person writes while a turn runs is credited to the turn. The agent's
// transcript lists the edits it made with its edit tools, each with its
// lines, so a line no edit of the turn holds is not the agent's from an edit.
//
// An agent also changes files through commands, a formatter or a code
// generator, and those show in the transcript as the command only. So a line
// that no edit holds is the person's only when the turn ran no tool that can
// write. When it did, the line stays the turn's, marked unsure. A pane with no
// transcript has nothing to say, and its turns' lines stay as they are.
//
// The transcript is read inside the daemon and only this verdict leaves it.

// reviewEditPages bounds how far back one read walks a transcript, in pages
// of transcriptview.MaxLimit entries.
const reviewEditPages = 20

// reviewReadOnlyTools are the tools that cannot write a file. Every other
// tool call counts as one that may have.
var reviewReadOnlyTools = []string{"Read", "Grep", "Glob", "LS", "WebFetch", "WebSearch", "TodoWrite", "NotebookRead", "BashOutput"}

// reviewEdit is one tool call of a transcript, as the judge needs it.
type reviewEdit struct {
	at int64 // Unix milliseconds
	// path is the file an edit tool changed, as the transcript names it,
	// and lines the lines it added. all says the edit's diff was cut, so
	// any line of the file counts as the edit's.
	path  string
	lines map[string]bool
	all   bool
	// writes marks a call of a tool that is not an edit and may write.
	writes bool
}

// reviewEditCache keeps each transcript's tool calls while the file stays
// the same size, so a live read of the review reads it again only when the
// agent wrote to it.
type reviewEditCache struct {
	mu    sync.Mutex
	files map[string]reviewEditFile
}

type reviewEditFile struct {
	size  int64
	from  int64 // the oldest time the calls cover, Unix milliseconds
	edits []reviewEdit
}

// editsOf are the tool calls of the transcript of the pane, back to from
// (Unix milliseconds), and whether the pane has a transcript to read.
func (d *Daemon) editsOf(sess *Session, pane string, from int64) ([]reviewEdit, bool) {
	path, harnessID, format, tsess, leaf, joined := sess.transcriptViewOf(pane)
	if !joined {
		return nil, false
	}
	reg := d.agentMatcher.registry
	if reg == nil {
		return nil, false
	}
	if tr := reg.TranscriptFor(harnessID); tr == nil || tr.Reader != format || !transcriptview.Known(format) {
		return nil, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	c := &d.reviewEditCache
	c.mu.Lock()
	f, ok := c.files[path]
	c.mu.Unlock()
	if ok && f.size == info.Size() && f.from <= from {
		return f.edits, true
	}
	opts := transcriptview.Options{Limit: transcriptview.MaxLimit, Format: format, Session: tsess, Leaf: leaf}
	var edits []reviewEdit
	oldest := int64(1<<63 - 1)
	for range reviewEditPages {
		page, err := transcriptview.Read(path, opts)
		if err != nil {
			return nil, false
		}
		for _, e := range page.Entries {
			if e.At > 0 && e.At < oldest {
				oldest = e.At
			}
			if e.Kind != transcriptview.KindToolCall {
				continue
			}
			switch {
			case e.Diff != nil:
				ed := reviewEdit{at: e.At, path: e.Diff.File, lines: map[string]bool{}, all: e.Diff.Truncated}
				for _, h := range e.Diff.Hunks {
					for _, l := range h.Lines {
						if l.Op == "+" {
							ed.lines[l.Text] = true
						}
					}
				}
				edits = append(edits, ed)
			case !slices.Contains(reviewReadOnlyTools, e.Tool):
				edits = append(edits, reviewEdit{at: e.At, writes: true})
			}
		}
		if page.Older == "" || oldest < from {
			break
		}
		opts.Before = page.Older
	}
	c.mu.Lock()
	if c.files == nil {
		c.files = map[string]reviewEditFile{}
	}
	c.files[path] = reviewEditFile{size: info.Size(), from: oldest, edits: edits}
	c.mu.Unlock()
	return edits, true
}

// reviewJudge is the judge of the lines a pane's turn added between from and
// to (Unix nanoseconds), nil when the pane has no transcript to judge by.
func (d *Daemon) reviewJudge(sess *Session, pane, root string, from, to int64) func(path, text string) review.Verdict {
	if sess == nil {
		return nil
	}
	fromMs, toMs := from/1e6, to/1e6
	edits, ok := d.editsOf(sess, pane, fromMs)
	if !ok {
		return nil
	}
	var in []reviewEdit
	writes := false
	for _, e := range edits {
		if e.at != 0 && (e.at < fromMs || e.at > toMs) {
			continue
		}
		if e.writes {
			writes = true
			continue
		}
		in = append(in, e)
	}
	return func(path, text string) review.Verdict {
		for _, e := range in {
			p := e.path
			if filepath.IsAbs(p) {
				if rel, err := filepath.Rel(root, p); err == nil {
					p = filepath.ToSlash(rel)
				}
			}
			if p == path && (e.all || e.lines[text]) {
				return review.Made
			}
		}
		if writes {
			return review.Unsure
		}
		return review.NotMade
	}
}
