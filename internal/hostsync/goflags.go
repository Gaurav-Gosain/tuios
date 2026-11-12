package hostsync

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// NoinlineList is where a checkout lists the packages a shipped build of
// cmd/tuios compiles with inlining off. scripts/goflags.sh reads the same
// file for the shell build paths.
const NoinlineList = "scripts/noinline-packages.txt"

// NoinlineGoFlags returns the GOFLAGS for a build of the checkout at srcDir
// as the release builds it: base, then -gcflags=PATTERN=-l for each package
// the checkout's noinline list names. A checkout from before the list has no
// file and gets base unchanged.
func NoinlineGoFlags(srcDir, base string) (string, error) {
	data, err := os.ReadFile(filepath.Join(srcDir, NoinlineList))
	if errors.Is(err, fs.ErrNotExist) {
		return base, nil
	}
	if err != nil {
		return "", err
	}
	flags := strings.Fields(base)
	for line := range strings.Lines(string(data)) {
		line, _, _ = strings.Cut(line, "#")
		if pattern := strings.TrimSpace(line); pattern != "" {
			flags = append(flags, "-gcflags="+pattern+"=-l")
		}
	}
	return strings.Join(flags, " "), nil
}
