package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

type lineRange struct{ from, to int }

type changes map[string][]lineRange

func (c changes) has(path string, line int) bool {
	for _, r := range c[path] {
		if line >= r.from && line <= r.to {
			return true
		}
	}
	return false
}

func (c changes) packages(cwd string) []string {
	var out []string
	for f := range c {
		slash := filepath.ToSlash(f)
		if !strings.HasSuffix(f, ".go") || strings.Contains(slash, "/testdata/") || strings.Contains(slash, "/vendor/") {
			continue
		}
		if _, err := os.Stat(f); err != nil {
			continue
		}
		rel, err := filepath.Rel(normPath(cwd), filepath.Dir(f))
		if err != nil {
			continue
		}
		if rel != "." && !strings.HasPrefix(rel, "..") {
			rel = "./" + rel
		}
		out = append(out, filepath.ToSlash(rel))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func gitChanges(dir, base string, staged bool) (changes, error) {
	out, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	top := strings.TrimSpace(out)
	rev := base
	if base == "HEAD" {
		if _, err := git(top, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
			rev = emptyTree
		}
	} else if mb, err := git(top, "merge-base", base, "HEAD"); err == nil {
		rev = strings.TrimSpace(mb)
	}
	args := []string{"-c", "core.quotePath=false", "diff", "-U0", "--no-color", "--no-ext-diff",
		"--no-renames", "--src-prefix=a/", "--dst-prefix=b/"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, rev, "--", "*.go")
	out, err = git(top, args...)
	if err != nil {
		return nil, err
	}
	c := parseDiff(out, top)
	if !staged {
		out, err := git(top, "-c", "core.quotePath=false", "ls-files", "--others", "--exclude-standard", "--", "*.go")
		if err != nil {
			return nil, err
		}
		for l := range strings.Lines(out) {
			if l = strings.TrimSpace(l); l != "" {
				c[normPath(filepath.Join(top, l))] = []lineRange{{1, math.MaxInt}}
			}
		}
	}
	return c, nil
}

func parseDiff(diff, top string) changes {
	c := changes{}
	var cur string
	for line := range strings.Lines(diff) {
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "+++ "):
			p := strings.TrimRight(strings.TrimPrefix(line, "+++ "), "\t")
			if p == "/dev/null" {
				cur = ""
				continue
			}
			cur = normPath(filepath.Join(top, strings.TrimPrefix(p, "b/")))
		case strings.HasPrefix(line, "@@ ") && cur != "":
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			start, count, ok := strings.Cut(strings.TrimPrefix(fields[2], "+"), ",")
			if !ok {
				count = "1"
			}
			s, err1 := strconv.Atoi(start)
			n, err2 := strconv.Atoi(count)
			if err1 != nil || err2 != nil || n == 0 {
				continue
			}
			c[cur] = append(c[cur], lineRange{s, s + n - 1})
		}
	}
	return c
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return string(out), nil
}
