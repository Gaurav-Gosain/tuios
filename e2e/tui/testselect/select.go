package main

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// selection is the outcome: every test, or the named ones, and one line per
// changed file that says what it selected.
type selection struct {
	all     bool
	tests   map[string]bool
	reasons []string
}

func everything(why string) *selection {
	return &selection{all: true, reasons: []string{why}}
}

func (s *selection) report(chosen, total int) string {
	var b strings.Builder
	switch {
	case s.all:
		fmt.Fprintf(&b, "testselect: runs all %d tests.\n", total)
	case chosen == 0:
		fmt.Fprintf(&b, "testselect: runs no tests of %d. The change cannot reach the binary or the suite.\n", total)
	default:
		fmt.Fprintf(&b, "testselect: runs %d of %d tests.\n", chosen, total)
	}
	for _, r := range s.reasons {
		fmt.Fprintf(&b, "- %s\n", r)
	}
	return b.String()
}

// pkgInfo is one workspace package of the binary or of the suite.
type pkgInfo struct {
	dir     string // relative to the repository root
	imports []string
	embeds  map[string]bool // embedded files, relative to the repository root
}

type graph struct {
	bin       map[string]*pkgInfo // by directory
	harness   map[string]bool     // directories of packages the suite itself builds
	importers map[string][]string // directory to the directories that import it
}

// The paths that can change neither the binary nor the suite. Anything not
// named here and not in a Go package runs the whole suite.
var inert = []*regexp.Regexp{
	regexp.MustCompile(`\.md$`),
	regexp.MustCompile(`^docs/`),
	regexp.MustCompile(`^examples/`),
	regexp.MustCompile(`^clienttests/`),
	regexp.MustCompile(`^assets/`),
	regexp.MustCompile(`^nix/`),
	regexp.MustCompile(`^scripts/`),
	regexp.MustCompile(`^(LICENSE|CITATION\.cff|FUNDING\.yml|Dockerfile|\.dockerignore|justfile|flake\.nix|flake\.lock|tuios\.nix|install\.sh|install-web\.sh|\.envrc|\.gitignore|\.gitattributes|\.golangci\.yml|\.goreleaser\.yml|\.markdownlint\.json|demo\.gif|demo\.avif)$`),
}

// The paths that always run the whole suite, checked before anything else.
var global = []*regexp.Regexp{
	regexp.MustCompile(`^\.github/`),
	regexp.MustCompile(`^scripts/(e2e-tested|retry-flaky)\.sh$`),
}

// buildInputs are the non-Go files the go command compiles into a package.
var buildInputs = regexp.MustCompile(`\.(s|S|c|cc|cpp|cxx|h|hh|hpp|hxx|m|f|F|for|f90|swig|swigcxx|syso)$`)

func choose(root, base, head, mapFile string, m *testMap, names []string) *selection {
	changes, err := changedFiles(root, base, head)
	if err != nil {
		return everything(fmt.Sprintf("the diff from %s cannot be read: %v", base, err))
	}
	if len(changes) == 0 {
		return &selection{tests: map[string]bool{}, reasons: []string{"no file changed"}}
	}
	g, err := loadGraph(root)
	if err != nil {
		return everything(fmt.Sprintf("go list failed: %v", err))
	}
	mapPath := filepath.ToSlash(filepath.Join("e2e/tui", mapFile))

	sel := &selection{tests: map[string]bool{}}
	bodyFiles := map[string]bool{}
	declPkgs := map[string]bool{}
	reach := false // the change reaches the binary or the suite
	for _, c := range changes {
		p := c.path
		switch {
		case p == mapPath:
			return everything(p + " is the test map")
		case matchAny(global, p):
			return everything(p + " is a CI file")
		}
		switch path.Base(p) {
		case "go.mod", "go.sum", "go.work", "go.work.sum":
			// Under go.work the binary builds every tuios.dev library from its
			// folder, and the libraries workflow holds each pin to the
			// folder's files, so a line that only moves a pin changes nothing
			// the folder diff does not already show.
			pins, err := pinOnly(root, base, head, p)
			if err != nil || !pins {
				return everything(p + " changes more than tuios.dev pins")
			}
			sel.reasons = append(sel.reasons, p+": only tuios.dev pins, which follow the library folders")
			continue
		}
		if path.Dir(p) == "e2e/tui" && strings.HasSuffix(p, ".md") {
			// The notes beside the suite. A fixture lives under testdata.
			sel.reasons = append(sel.reasons, p+": documentation or tooling")
			continue
		}
		if strings.HasPrefix(p, "e2e/tui/") {
			if path.Dir(p) != "e2e/tui" || !strings.HasSuffix(p, "_test.go") || path.Base(p) == "harness_test.go" {
				return everything(p + " is part of the e2e harness")
			}
			tests, why := testFileChange(root, base, head, c)
			if tests == nil {
				return everything(p + ": " + why)
			}
			for _, t := range tests {
				sel.tests[t] = true
			}
			reach = true
			sel.reasons = append(sel.reasons, fmt.Sprintf("%s: %s", p, why))
			continue
		}
		dir := nearestPackage(p, g)
		if dir != "" && g.harness[dir] {
			return everything(p + " is in " + dir + ", which the e2e harness imports")
		}
		if info := g.bin[dir]; info != nil {
			switch {
			case strings.HasSuffix(p, "_test.go"):
				sel.reasons = append(sel.reasons, p+": a unit test file, not in the binary")
			case strings.HasSuffix(p, ".go"):
				reach = true
				funcs, ok := bodyOnly(root, base, head, c, m.init[p])
				if ok {
					bodyFiles[p] = true
					sel.reasons = append(sel.reasons, fmt.Sprintf("%s: inside %s", p, strings.Join(funcs, ", ")))
				} else {
					declPkgs[dir] = true
					sel.reasons = append(sel.reasons, fmt.Sprintf("%s: a declaration in %s", p, dir))
				}
			case info.embeds[p] || (path.Dir(p) == dir && buildInputs.MatchString(p)):
				reach = true
				declPkgs[dir] = true
				sel.reasons = append(sel.reasons, fmt.Sprintf("%s: built into %s", p, dir))
			default:
				sel.reasons = append(sel.reasons, p+": not built into the binary")
			}
			continue
		}
		if inGoPackage(root, p) {
			sel.reasons = append(sel.reasons, p+": in a package neither the binary nor the suite builds")
			continue
		}
		if matchAny(inert, p) {
			sel.reasons = append(sel.reasons, p+": documentation or tooling")
			continue
		}
		return everything(p + " is not covered by a rule")
	}

	if !reach {
		return sel
	}

	// A declaration reaches the code that names it: its own package and the
	// packages that import it.
	near := map[string]bool{}
	for dir := range declPkgs {
		near[dir] = true
		for _, imp := range g.importers[dir] {
			near[imp] = true
		}
	}
	fileIndex := map[string]int{}
	for i, f := range m.files {
		fileIndex[f] = i
	}
	bodyIdx := map[int]bool{}
	for f := range bodyFiles {
		if i, ok := fileIndex[f]; ok {
			bodyIdx[i] = true
		}
	}
	cmdTests := testsThatBuildTheBinary(root)
	unknown, every := 0, 0
	for _, name := range names {
		files, known := m.tests[name]
		switch {
		case !known:
			sel.tests[name] = true
			unknown++
		case m.every[name] || cmdTests[name]:
			sel.tests[name] = true
			every++
		default:
			if slices.ContainsFunc(files, func(i int) bool { return bodyIdx[i] }) {
				sel.tests[name] = true
				continue
			}
			for _, i := range files {
				if near[path.Dir(m.files[i])] {
					sel.tests[name] = true
					break
				}
			}
		}
	}
	if unknown > 0 {
		sel.reasons = append(sel.reasons, fmt.Sprintf("%d tests are newer than the test map and run", unknown))
	}
	if every > 0 {
		sel.reasons = append(sel.reasons, fmt.Sprintf("%d tests run on every change to the binary", every))
	}
	return sel
}

func matchAny(res []*regexp.Regexp, p string) bool {
	return slices.ContainsFunc(res, func(re *regexp.Regexp) bool { return re.MatchString(p) })
}

// nearestPackage returns the directory of the binary or harness package that
// holds p, the closest one above it, or "".
func nearestPackage(p string, g *graph) string {
	for dir := path.Dir(p); ; dir = path.Dir(dir) {
		if g.bin[dir] != nil || g.harness[dir] {
			return dir
		}
		if dir == "." || dir == "/" {
			return ""
		}
	}
}

// inGoPackage reports whether p is in a directory, or under one, that holds
// Go files at the head.
func inGoPackage(root, p string) bool {
	for dir := path.Dir(p); dir != "." && dir != "/"; dir = path.Dir(dir) {
		matches, _ := filepath.Glob(filepath.Join(root, dir, "*.go"))
		if len(matches) > 0 {
			return true
		}
	}
	return false
}

type change struct {
	status byte // A, M or D
	path   string
}

func git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

func changedFiles(root, base, head string) ([]change, error) {
	out, err := git(root, "diff", "--no-renames", "--name-status", base, head)
	if err != nil {
		return nil, err
	}
	var changes []change
	for line := range strings.Lines(out) {
		status, p, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || status == "" {
			continue
		}
		switch status[0] {
		case 'A', 'M', 'D':
			changes = append(changes, change{status: status[0], path: p})
		default:
			// A type change or anything else git may report: judge it as a
			// file that was removed and added again.
			changes = append(changes, change{status: 'A', path: p})
		}
	}
	return changes, nil
}

// pinOnly reports whether every changed line of a go.mod, go.sum or go.work
// names a tuios.dev module.
func pinOnly(root, base, head, p string) (bool, error) {
	out, err := git(root, "diff", "-U0", base, head, "--", p)
	if err != nil {
		return false, err
	}
	for line := range strings.Lines(out) {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
			if !strings.Contains(line, "tuios.dev/") {
				return false, nil
			}
		}
	}
	return true, nil
}

// loadGraph lists the workspace packages of the binary, with their imports and
// embedded files, and the packages the suite builds: its test package, the
// helper programs under e2e/tui, and what they import.
func loadGraph(root string) (*graph, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	rel := func(dir string) (string, bool) {
		r, err := filepath.Rel(absRoot, dir)
		if err != nil || r == ".." || strings.HasPrefix(r, "../") {
			return "", false
		}
		return filepath.ToSlash(r), true
	}
	list := func(args ...string) (string, error) {
		cmd := exec.Command("go", append([]string{"list"}, args...)...)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return "", fmt.Errorf("go list %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
			}
			return "", err
		}
		return string(out), nil
	}

	out, err := list("-deps", "-f", "{{if not .Standard}}{{.ImportPath}}\t{{.Dir}}\t{{join .Imports \" \"}}\t{{join .EmbedFiles \"|\"}}{{end}}", "./cmd/tuios")
	if err != nil {
		return nil, err
	}
	g := &graph{bin: map[string]*pkgInfo{}, harness: map[string]bool{}, importers: map[string][]string{}}
	byImport := map[string]string{}
	type raw struct {
		dir     string
		imports []string
	}
	var raws []raw
	for line := range strings.Lines(out) {
		fields := strings.Split(strings.TrimRight(line, "\n"), "\t")
		if len(fields) != 4 {
			continue
		}
		dir, ok := rel(fields[1])
		if !ok {
			continue
		}
		info := &pkgInfo{dir: dir, embeds: map[string]bool{}}
		for _, e := range strings.Split(fields[3], "|") {
			if e != "" {
				info.embeds[path.Join(dir, e)] = true
			}
		}
		g.bin[dir] = info
		byImport[fields[0]] = dir
		raws = append(raws, raw{dir, strings.Fields(fields[2])})
	}
	if len(g.bin) == 0 {
		return nil, fmt.Errorf("go list found no packages of ./cmd/tuios")
	}
	for _, r := range raws {
		for _, imp := range r.imports {
			if dir, ok := byImport[imp]; ok {
				g.bin[r.dir].imports = append(g.bin[r.dir].imports, dir)
				g.importers[dir] = append(g.importers[dir], r.dir)
			}
		}
	}

	patterns := []string{"./e2e/tui/..."}
	helpers, _ := filepath.Glob(filepath.Join(root, "e2e/tui/testdata/*/*.go"))
	seen := map[string]bool{}
	for _, h := range helpers {
		dir := "./" + filepath.ToSlash(mustRel(root, filepath.Dir(h)))
		if !seen[dir] {
			seen[dir] = true
			patterns = append(patterns, dir)
		}
	}
	out, err = list(append([]string{"-deps", "-test", "-f", "{{if not .Standard}}{{.Dir}}{{end}}"}, patterns...)...)
	if err != nil {
		return nil, err
	}
	for line := range strings.Lines(out) {
		if dir, ok := rel(strings.TrimSpace(line)); ok && dir != "" {
			if !strings.HasPrefix(dir, "e2e/tui") {
				g.harness[dir] = true
			}
		}
	}
	return g, nil
}

func mustRel(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return r
}

// testsThatBuildTheBinary names the tests in files that build ./cmd/tuios
// themselves. That build is not the binary the coverage run measured, so the
// map cannot say what those tests reach.
func testsThatBuildTheBinary(root string) map[string]bool {
	out := map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(root, "e2e/tui/*_test.go"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil || !strings.Contains(string(data), `"./cmd/tuios"`) {
			continue
		}
		for _, m := range testFunc.FindAllStringSubmatch(string(data), -1) {
			out[m[1]] = true
		}
	}
	return out
}

var testFunc = regexp.MustCompile(`(?m)^func (Test\w+)\(t \*testing\.T\)`)

// hunk is one changed range of a diff: lines oldStart to oldStart+oldLen-1
// became newStart to newStart+newLen-1. A length of 0 is an insertion or a
// deletion after the start line.
type hunk struct {
	oldStart, oldLen, newStart, newLen int
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func hunks(root, base, head, p string) ([]hunk, error) {
	out, err := git(root, "diff", "-U0", base, head, "--", p)
	if err != nil {
		return nil, err
	}
	var hs []hunk
	for line := range strings.Lines(out) {
		m := hunkHeader.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		num := func(s string, def int) int {
			if s == "" {
				return def
			}
			n, _ := strconv.Atoi(s)
			return n
		}
		hs = append(hs, hunk{num(m[1], 0), num(m[2], 1), num(m[3], 0), num(m[4], 1)})
	}
	return hs, nil
}
