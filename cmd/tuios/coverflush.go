//go:build coverflush

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime/coverage"
	"strings"
	"time"
)

func init() {
	dir := os.Getenv("GOCOVERDIR")
	if dir == "" {
		return
	}
	go flushCoverage(dir)
}

func flushCoverage(dir string) {
	if coverage.WriteMetaDir(dir) != nil {
		return
	}
	metas, _ := filepath.Glob(filepath.Join(dir, "covmeta.*"))
	if len(metas) != 1 {
		return
	}
	hash := strings.TrimPrefix(filepath.Base(metas[0]), "covmeta.")
	name := filepath.Join(dir, fmt.Sprintf("covcounters.%s.%d.1", hash, os.Getpid()))
	var buf bytes.Buffer
	for {
		time.Sleep(500 * time.Millisecond)
		buf.Reset()
		if coverage.WriteCounters(&buf) != nil {
			return
		}
		tmp := filepath.Join(dir, fmt.Sprintf("tmp.%d", os.Getpid()))
		if os.WriteFile(tmp, buf.Bytes(), 0o644) == nil {
			_ = os.Rename(tmp, name)
		}
	}
}
