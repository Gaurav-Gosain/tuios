//go:build coverflush

package main

// This file is only in a binary built with -tags coverflush, which the e2e
// test map job builds together with -cover. A coverage build writes its
// counters when the program returns from main or calls os.Exit. The suite ends
// most clients by closing their terminal or by a signal, and those exits write
// nothing, so a test would look like it never ran the client code. Here the
// counters go to one file per process every half second, which the next write
// replaces. The runtime still writes its own file on a normal exit; the reader
// only asks whether a block ran, so the overlap does no harm.

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
	// The runtime writes the meta-data file only on exit, so write it now. Its
	// name carries the hash a counter file must repeat to be read with it.
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
