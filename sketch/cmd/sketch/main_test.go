package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunDrawsARareType draws a Wardley map, a type sketch/core does not
// parse, as cells and as text: the sketch command draws every type.
func TestRunDrawsARareType(t *testing.T) {
	data, err := os.ReadFile("../../testdata/corpus/wardley-docs.json")
	if err != nil {
		t.Fatal(err)
	}
	var srcs []string
	if err := json.Unmarshal(data, &srcs); err != nil {
		t.Fatal(err)
	}
	if len(srcs) == 0 {
		t.Fatal("no Wardley source in the corpus")
	}
	src := srcs[0]
	path := filepath.Join(t.TempDir(), "map.mmd")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	// sketch runs the command with args and the source file, and gives what it
	// wrote to standard output and to standard error.
	sketch := func(t *testing.T, args ...string) (string, string) {
		t.Helper()
		name := filepath.Join(t.TempDir(), "out")
		out, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		var stderr strings.Builder
		if err := run(append(args, path), out, &stderr); err != nil {
			t.Fatalf("run: %v\nstderr:\n%s", err, stderr.String())
		}
		got, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(got), stderr.String()
	}

	t.Run("cells", func(t *testing.T) {
		stdout, _ := sketch(t, "-f", "cells", "-cols", "60")
		if strings.TrimSpace(stdout) == "" {
			t.Fatal("no cells written")
		}
		var cells struct {
			Rows [][][3]int `json:"rows"`
		}
		if err := json.Unmarshal([]byte(stdout), &cells); err != nil {
			t.Fatalf("the cells do not decode: %v\n%s", err, stdout)
		}
		if len(cells.Rows) == 0 {
			t.Fatalf("no rows of cells:\n%s", stdout)
		}
	})

	t.Run("text", func(t *testing.T) {
		stdout, stderr := sketch(t, "-f", "text", "-color", "never", "-cols", "60")
		if strings.Contains(stderr, "no text drawing") {
			t.Fatalf("the map has no text drawing:\n%s", stderr)
		}
		if strings.TrimRight(stdout, "\n") == strings.TrimRight(src, "\n") {
			t.Fatalf("the source came back instead of a drawing:\n%s", stdout)
		}
		drawn := false
		for line := range strings.SplitSeq(stdout, "\n") {
			if strings.TrimSpace(line) != "" {
				drawn = true
				break
			}
		}
		if !drawn {
			t.Fatalf("no line of the drawing has text:\n%q", stdout)
		}
	})
}
