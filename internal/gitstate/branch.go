package gitstate

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// headLimit bounds how much of a ".git" file or HEAD is read. Each is one
// line, and a directory that puts something else under those names is not a
// checkout worth stalling on.
const headLimit = 4096

// readFirstLine is the first line of a small file, trimmed. It reads at most
// headLimit bytes.
func readFirstLine(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }() // opened read-only: nothing to flush
	data, err := io.ReadAll(io.LimitReader(f, headLimit))
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(data), "\n")
	return strings.TrimSpace(line), nil
}

// Branch is the branch checked out in the working tree that holds dir, empty
// when dir is in none. A detached HEAD reads as its short hash, which is what
// git itself shows for one. It finds the tree with Locate, so a linked worktree
// reports its own branch, and it reads one file: no cache, no subprocess. A
// repository whose refs are in reftable is the exception: its HEAD file names
// no branch, and the answer comes from ReftableHead, which asks git when the
// refs have moved.
//
// It is stricter than Read about a detached HEAD: the line must be a full
// hexadecimal object name before its first seven characters are shown.
func Branch(dir string) string {
	gitdir, commondir, root, ok := Locate(dir)
	if !ok {
		return ""
	}
	line, err := readFirstLine(filepath.Join(gitdir, "HEAD"))
	if err != nil {
		return ""
	}
	if IsReftableHead(line) {
		branch, _, _ := ReftableHead(gitdir, commondir, root)
		return branch
	}
	if ref, ok := strings.CutPrefix(line, "ref:"); ok {
		return strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/")
	}
	if len(line) >= 40 && isHex(line) {
		return line[:7]
	}
	return ""
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
