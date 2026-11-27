package review

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Reverting a hunk: taking one hunk of the diff against the base out of the
// working file, so the file has the base's lines there again, and putting it
// back. Unlike staging, this writes the working tree, and nothing else: the
// index is not touched.
//
// The patch is the hunk reversed, built forward against the working file
// (SelectionPatch with every change selected and unstage set, which builds a
// selection reversed). Applied, it reverts the hunk. Applied reversed, it
// puts the hunk back. git apply writes all of a patch or none of it, and
// refuses when the file around the hunk is not as the patch expects, so
// neither ever writes over an edit made in between.

// Sum is a short digest of h's lines, which says whether a hunk read again
// is the one a caller saw. Two hunks with the same header can differ in their
// lines, so the header alone does not say it.
func (h *Hunk) Sum() string {
	s := sha256.New()
	for _, l := range h.Lines {
		fmt.Fprintf(s, "%s %d %d %t %s\n", l.Op, l.Old, l.New, l.NoNewline, l.Text)
	}
	return hex.EncodeToString(s.Sum(nil)[:8])
}

// RevertPatch builds the patch that reverts hunk h of f, a file of a diff
// against a base, in the working tree. A file the base does not have, with h
// its only hunk, is deleted by it, and one the working tree does not have is
// made again.
func RevertPatch(f *File, h *Hunk) (string, error) {
	if f.Binary || f.Truncated || f.Status == StatusRenamed {
		return "", errors.New("only a hunk of text in a file that is not renamed is reverted")
	}
	p, err := SelectionPatch(f, h, h.Changes(), true)
	if err != nil {
		return "", err
	}
	if (f.Status == StatusAdded || f.Status == StatusUntracked) && len(f.Hunks) == 1 && h.OldLines == 0 {
		mode := f.newMode
		if mode == "" || strings.Trim(mode, "0") == "" {
			mode = "100644"
		}
		a, b := quotePatchPath("a/"+f.Path), quotePatchPath("b/"+f.Path)
		old := fmt.Sprintf("--- %s\n+++ %s\n", a, b)
		p = strings.Replace(p, old, fmt.Sprintf("deleted file mode %s\n--- %s\n+++ /dev/null\n", mode, a), 1)
	}
	return p, nil
}

// ApplyToWork applies patch to the working tree of the worktree at dir, or
// with reverse takes it back out. The index is not written.
func ApplyToWork(ctx context.Context, dir, patch string, reverse bool) error {
	args := []string{"apply", "--recount", "--whitespace=nowarn"}
	if reverse {
		args = append(args, "-R")
	}
	_, err := gitInput(ctx, dir, patch, append(args, "-")...)
	return err
}
