// Package review reads what an agent changed in a git worktree and carries
// the notes a person leaves on it back to the agent.
//
// It has four parts:
//
//   - Build reads the diff of a worktree's working state, committed or not,
//     against a base or against another worktree of the same repository. The
//     working state is written as a git tree through a temporary index
//     (worktree.SnapshotTree), so the worktree's own index and files are
//     never changed: what the agent staged stays staged and nothing is added.
//   - ParseRawNumstat and the unified diff reader turn git's output into
//     files, hunks and lines, with caps on files, bytes and lines per file.
//   - Anchor and AnchorHunk find a note's line again after the diff moved.
//   - Compose writes the notes as the one message an agent receives.
//
// The package runs git and reads files. It keeps no state: the notes
// themselves are held by the daemon (internal/session).
package review

// Line ops, as a diff line reports them.
const (
	// OpContext is a line both sides have.
	OpContext = "context"
	// OpAdd is a line only the new side has.
	OpAdd = "add"
	// OpDelete is a line only the old side has.
	OpDelete = "delete"
)

// File statuses, as a changed file reports them.
const (
	StatusAdded     = "A"
	StatusModified  = "M"
	StatusDeleted   = "D"
	StatusRenamed   = "R"
	StatusUntracked = "U"
)

// Sides of a diff a note sits on.
const (
	SideNew = "new"
	SideOld = "old"
)

// Limits bound one diff. Past a limit a file is listed with its counts only
// and marked truncated.
type Limits struct {
	// Files is how many files carry hunks. The rest carry counts only.
	Files int
	// Bytes is how much unified diff text is read in all.
	Bytes int
	// LinesPerFile is how many diff lines one file may carry.
	LinesPerFile int
}

// DefaultLimits are the caps review-diff applies: 400 files, 2 MiB of diff
// text and 5000 lines in one file.
var DefaultLimits = Limits{Files: 400, Bytes: 2 << 20, LinesPerFile: 5000}

// Line is one line of a hunk. Old and New are its line numbers on each side,
// zero on the side it is not on.
type Line struct {
	Op   string `json:"op"`
	Old  int    `json:"old,omitempty"`
	New  int    `json:"new,omitempty"`
	Text string `json:"text"`
	// NoNewline marks the last line of a side that has no newline at its
	// end ("\ No newline at end of file").
	NoNewline bool `json:"no_newline,omitempty"`
}

// Hunk is one hunk of a file's diff.
type Hunk struct {
	Header   string `json:"header"`
	OldStart int    `json:"old_start"`
	OldLines int    `json:"old_lines"`
	NewStart int    `json:"new_start"`
	NewLines int    `json:"new_lines"`
	Lines    []Line `json:"lines"`
}

// File is one changed file.
type File struct {
	Path string `json:"path"`
	// OldPath is the path before a rename, empty otherwise.
	OldPath string `json:"old_path,omitempty"`
	// Status is A, M, D, R or U (untracked: new and not yet added to git).
	Status  string `json:"status"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Binary  bool   `json:"binary,omitempty"`
	// Truncated marks a file past a limit: its counts are right and its
	// hunks are left out.
	Truncated bool   `json:"truncated,omitempty"`
	Hunks     []Hunk `json:"hunks"`
}

// Totals sum a diff.
type Totals struct {
	Files   int `json:"files"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// Diff is what Build reads.
type Diff struct {
	// Base is the base as it was named, empty for a diff against another
	// worktree.
	Base string `json:"base"`
	// BaseSHA is the commit the diff runs from: the merge base of HEAD and
	// the named base, or HEAD for uncommitted changes only.
	BaseSHA string `json:"base_sha"`
	// Uncommitted says the diff runs from HEAD: asked for, or no base could
	// be found.
	Uncommitted bool `json:"uncommitted,omitempty"`
	// TreeSHA is the tree the working state was written as.
	TreeSHA string `json:"tree_sha"`
	// AgainstTree is the other worktree's tree, for a diff against one.
	AgainstTree string `json:"against_tree,omitempty"`
	Files       []File `json:"files"`
	Totals      Totals `json:"totals"`
	Truncated   bool   `json:"truncated"`
}

// FileByPath finds a file of the diff by its path, nil for none.
func (d *Diff) FileByPath(path string) *File {
	if d == nil {
		return nil
	}
	for i := range d.Files {
		if d.Files[i].Path == path {
			return &d.Files[i]
		}
	}
	return nil
}

// Note is one review note on a pane's changes.
type Note struct {
	ID string `json:"id"`
	// Path is the file, relative to the worktree's root.
	Path string `json:"path"`
	// Side is new or old: which side's numbering Line is on.
	Side string `json:"side"`
	// Line is the line the note is on. For a note on a hunk it is the hunk's
	// first line on Side.
	Line int `json:"line"`
	// Quote is the text of the line, which finds it again when it moves.
	// Empty for a note on a hunk.
	Quote string `json:"quote,omitempty"`
	// HunkHeader is the header of the hunk a note on a whole hunk is on.
	HunkHeader string `json:"hunk_header,omitempty"`
	Text       string `json:"text"`
	// By is who wrote it: human, a pane's window id, shell, or link:HOST.
	By string `json:"by"`
	// At is when it was written or last edited, Unix nanoseconds.
	At int64 `json:"at"`
	// SentAt is when send-review last handed it to the agent, zero for never.
	SentAt int64 `json:"sent_at,omitempty"`
	// Outdated says its line could not be found again.
	Outdated bool `json:"outdated,omitempty"`
}

// IsHunk reports whether the note is on a whole hunk.
func (n *Note) IsHunk() bool { return n.HunkHeader != "" }
