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
	// bin is every workspace package of the binary or of the suite's test
	// process, by directory. The coverage run measures both processes.
	bin       map[string]*pkgInfo
	harness   map[string]bool     // directories of packages the suite itself builds
	importers map[string][]string // directory to the directories that import it; the suite is suiteDir
}

// suiteDir is the suite's own package. It is an importer in the graph, so a
// changed name reaches the suite's code that names it (see reach).
const suiteDir = "e2e/tui"

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

// committedMap is the map in the repository. scripts/e2e-select.sh may pass
// a newer one from the map workflow instead.
const committedMap = "e2e/tui/testselect/testmap.txt"

// buildInputs are the non-Go files the go command compiles into a package.
var buildInputs = regexp.MustCompile(`\.(s|S|c|cc|cpp|cxx|h|hh|hpp|hxx|m|f|F|for|f90|swig|swigcxx|syso)$`)

func choose(root, base, head, mapFile string, m *testMap, testNames []string) *selection {
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
	var sg *suiteGraph
	suite := func() (*suiteGraph, error) {
		if sg == nil {
			var err error
			if sg, err = loadSuiteGraph(root); err != nil {
				return nil, err
			}
		}
		return sg, nil
	}
	bodyFuncs := map[funcRef]bool{} // functions whose body alone changed
	declPkgs := map[string]bool{}   // packages changed in a way only their importers bound
	var names []name                // declarations that changed
	reaches := false                // the change reaches the binary or the suite
	for _, c := range changes {
		p := c.path
		switch {
		case p == mapPath || p == committedMap:
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
			if !suiteFile(p) {
				return everything(p + " is part of the e2e harness")
			}
			sg, err := suite()
			if err != nil {
				return everything(fmt.Sprintf("the suite's test files cannot be read: %v", err))
			}
			tests, why := testFileChange(root, base, head, c, sg)
			if tests == nil {
				return everything(p + ": " + why)
			}
			for _, t := range tests {
				sel.tests[t] = true
			}
			reaches = true
			sel.reasons = append(sel.reasons, fmt.Sprintf("%s: %s", p, why))
			continue
		}
		dir := nearestPackage(p, g)
		if dir != "" && g.harness[dir] && !m.harness {
			// A map without the suite's own coverage cannot say which
			// tests the harness code reaches.
			return everything(p + " is in " + dir + ", which the e2e harness imports, and the test map has no coverage of the harness")
		}
		if info := g.bin[dir]; info != nil {
			switch {
			case strings.HasSuffix(p, "_test.go"):
				sel.reasons = append(sel.reasons, p+": a unit test file, not in the binary")
			case strings.HasSuffix(p, ".go"):
				gc := analyzeGoFile(root, base, head, dir, c, m.init[p])
				switch {
				case !gc.built:
					sel.reasons = append(sel.reasons, p+": not in the linux build")
				case gc.global != "":
					reaches = true
					declPkgs[dir] = true
					sel.reasons = append(sel.reasons, fmt.Sprintf("%s: %s", p, gc.global))
				default:
					reaches = true
					var parts []string
					if len(gc.bodies) > 0 {
						for _, f := range gc.funcs {
							bodyFuncs[funcRef{p, f}] = true
						}
						parts = append(parts, "inside "+strings.Join(gc.bodies, ", "))
					}
					if len(gc.names) > 0 {
						var ids []string
						for _, n := range gc.names {
							ids = append(ids, n.ident)
						}
						names = append(names, gc.names...)
						parts = append(parts, "declares "+strings.Join(ids, ", "))
					}
					if len(parts) == 0 {
						parts = append(parts, "comments only")
					}
					sel.reasons = append(sel.reasons, fmt.Sprintf("%s: %s", p, strings.Join(parts, "; ")))
				}
			case info.embeds[p]:
				reaches = true
				vars := embedVars(root, dir)
				if len(vars) == 0 {
					declPkgs[dir] = true
				}
				for _, v := range vars {
					names = append(names, name{dir, v, false, ""})
				}
				sel.reasons = append(sel.reasons, fmt.Sprintf("%s: embedded in %s", p, dir))
			case path.Dir(p) == dir && buildInputs.MatchString(p):
				reaches = true
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

	if !reaches {
		return sel
	}
	// Code of a package the harness builds runs in every test process when
	// it changes in a way no name carries: an init function, a build
	// constraint, cgo, or an embedded file with no variable.
	for dir := range declPkgs {
		if g.harness[dir] {
			return everything(dir + " changed in a way no name carries, and the e2e harness imports it")
		}
	}

	// A changed declaration reaches the code that names it (see reach). When
	// that walk cannot finish, or the change has effects no name carries, the
	// package and every package that imports it count.
	// A method whose name some interface uses changes its type. Any other
	// method is reached through its name like a function.
	if slices.ContainsFunc(names, func(n name) bool { return n.recv != "" }) {
		methods, err := interfaceMethods(root)
		if err != nil {
			return everything(fmt.Sprintf("the interfaces of the binary cannot be listed: %v", err))
		}
		for i, n := range names {
			if n.recv != "" && methods[n.ident] {
				names[i] = name{n.dir, n.recv, true, ""}
			}
		}
	}
	refFuncs, ok := reach(root, g, names)
	if !ok {
		sel.reasons = append(sel.reasons, "the changed declarations reach too far to follow, so their packages and importers count")
		refFuncs = map[funcRef]bool{}
		for _, n := range names {
			declPkgs[n.dir] = true
		}
	}
	if len(refFuncs) > 0 {
		var list []string
		for f := range refFuncs {
			list = append(list, f.file+":"+f.name)
		}
		slices.Sort(list)
		if len(list) > 8 {
			list = append(list[:8], fmt.Sprintf("and %d more", len(refFuncs)-8))
		}
		sel.reasons = append(sel.reasons, "the changed declarations are named in "+strings.Join(list, ", "))
	}

	// The tests that ran a changed function, or one that names a changed
	// declaration, or any code of a package that counts whole.
	near := map[string]bool{}
	for dir := range declPkgs {
		near[dir] = true
		for _, imp := range g.importers[dir] {
			near[imp] = true
		}
	}
	// The suite's code is not in the map. A function of the suite that names
	// a changed declaration selects the tests whose code reaches it.
	var suiteDecls []string
	for f := range refFuncs {
		if path.Dir(f.file) == suiteDir {
			suiteDecls = append(suiteDecls, f.name)
		}
	}
	if len(suiteDecls) > 0 {
		sg, err := suite()
		if err != nil {
			return everything(fmt.Sprintf("the suite's test files cannot be read: %v", err))
		}
		for _, t := range sg.testsUsing(suiteDecls) {
			sel.tests[t] = true
		}
	}
	hit := map[int]bool{}
	for f := range bodyFuncs {
		for _, t := range m.testsOf(f) {
			hit[t] = true
		}
	}
	for f := range refFuncs {
		for _, t := range m.testsOf(f) {
			hit[t] = true
		}
	}
	if len(near) > 0 {
		for f, tests := range m.funcs {
			if near[path.Dir(f.file)] {
				for _, t := range tests {
					hit[t] = true
				}
			}
		}
	}
	cmdTests := testsThatBuildTheBinary(root)
	unknown, every := 0, 0
	for _, test := range testNames {
		i, known := m.index[test]
		switch {
		case !known:
			sel.tests[test] = true
			unknown++
		case m.every[test] || cmdTests[test]:
			sel.tests[test] = true
			every++
		case hit[i]:
			sel.tests[test] = true
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
			// A blank line moves with a replace block when the pin
			// changes, and means nothing to the go command.
			if strings.TrimSpace(line[1:]) == "" {
				continue
			}
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

	// The suite's test process: its own package, the helper programs under
	// e2e/tui, and what they import. Its workspace packages join the graph,
	// since the coverage run measures the test process too, and the suite's
	// package is an importer of what it imports directly.
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
	out, err = list(append([]string{"-deps", "-test", "-f", "{{if not .Standard}}{{.ImportPath}}\t{{.Dir}}\t{{join .Imports \" \"}}\t{{join .EmbedFiles \"|\"}}{{end}}"}, patterns...)...)
	if err != nil {
		return nil, err
	}
	raws = raws[:0]
	var suiteImports []string
	for line := range strings.Lines(out) {
		fields := strings.Split(strings.TrimRight(line, "\n"), "\t")
		if len(fields) != 4 {
			continue
		}
		importPath, _, _ := strings.Cut(fields[0], " ")
		dir, ok := rel(fields[1])
		if !ok || dir == "" {
			continue
		}
		if dir == suiteDir {
			suiteImports = append(suiteImports, strings.Fields(fields[2])...)
			continue
		}
		if strings.HasPrefix(dir, suiteDir+"/") {
			continue
		}
		g.harness[dir] = true
		if g.bin[dir] != nil {
			continue
		}
		info := &pkgInfo{dir: dir, embeds: map[string]bool{}}
		for e := range strings.SplitSeq(fields[3], "|") {
			if e != "" {
				info.embeds[path.Join(dir, e)] = true
			}
		}
		g.bin[dir] = info
		byImport[importPath] = dir
		raws = append(raws, raw{dir, strings.Fields(fields[2])})
	}
	for _, r := range raws {
		for _, imp := range r.imports {
			if dir, ok := byImport[imp]; ok {
				g.bin[r.dir].imports = append(g.bin[r.dir].imports, dir)
				g.importers[dir] = append(g.importers[dir], r.dir)
			}
		}
	}
	for _, imp := range suiteImports {
		if dir, ok := byImport[imp]; ok && !slices.Contains(g.importers[dir], suiteDir) {
			g.importers[dir] = append(g.importers[dir], suiteDir)
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
