package applist

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// executable reports whether the file is one Windows would run from a bare
// name. There are no execute bits, so the extension is the whole test, and
// %PATHEXT% is the list the shell itself consults.
func executable(info fs.FileInfo) bool {
	ext := strings.ToLower(filepath.Ext(info.Name()))
	if ext == "" {
		return false
	}
	for _, want := range pathExt() {
		if ext == want {
			return true
		}
	}
	return false
}

func pathExt() []string {
	raw := os.Getenv("PATHEXT")
	if raw == "" {
		raw = ".COM;.EXE;.BAT;.CMD"
	}
	parts := strings.Split(raw, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
