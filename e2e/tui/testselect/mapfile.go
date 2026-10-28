package main

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// testMap is what the coverage run learned: for each function, the tests that
// ran it, outside the code every process runs at start-up.
type testMap struct {
	files []string            // repository-relative paths
	tests []string            // test names; the func lines refer to them by index
	index map[string]int      // test name to index
	every map[string]bool     // tests to run on every change that reaches the binary
	init  map[string][]string // file to the functions every process runs in it
	funcs map[funcRef][]int   // function to the tests that ran it
	// harness is true when the map holds the suite's test process too, not
	// only the tuios processes it starts.
	harness bool
}

// funcRef names a function the way go tool cover -func does: its file and
// its name, without the receiver. Two methods of the same name in one file
// share a ref, which only selects more.
type funcRef struct {
	file, name string
}

// testsOf returns the tests that ran a function.
func (m *testMap) testsOf(f funcRef) []int {
	return m.funcs[f]
}

const mapHeader = `# The end-to-end test map: for each function, the tests that ran it.
# testselect reads it to run only the tests a change can reach. It is written
# by the e2e-tui test map workflow (.github/workflows/e2e-tui-map.yml), which
# runs every test against a tuios built with -cover. Do not edit it by hand.
#
#   cover harness          the test process was measured too, not only tuios
#   file PATH              a source file; the files are numbered from 0
#   init PATH FUNC         a function every tuios process runs at start-up
#   test NAME              a test; the tests are numbered from 0
#   test NAME *            a test that runs on every change to the binary
#   set TESTS              a set of tests; the sets are numbered from 0.
#                          TESTS is test numbers joined by commas, or x and a
#                          bitset in hex, bit i of byte j for test 8*j+i
#   func FILE NAME SET     the set of tests that ran the function NAME in
#                          file number FILE
`

func readMap(name string) (*testMap, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := &testMap{index: map[string]int{}, every: map[string]bool{}, init: map[string][]string{}, funcs: map[funcRef][]int{}}
	var sets [][]int
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 1<<20), 1<<20)
	for n := 1; scan.Scan(); n++ {
		line := scan.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		bad := func(err error) error { return fmt.Errorf("%s:%d: %v: %q", name, n, err, line) }
		fields := strings.Fields(line)
		switch {
		case fields[0] == "cover" && len(fields) == 2 && fields[1] == "harness":
			m.harness = true
		case fields[0] == "file" && len(fields) == 2:
			m.files = append(m.files, fields[1])
		case fields[0] == "init" && len(fields) == 3:
			m.init[fields[1]] = append(m.init[fields[1]], fields[2])
		case fields[0] == "test" && (len(fields) == 2 || len(fields) == 3 && fields[2] == "*"):
			m.index[fields[1]] = len(m.tests)
			m.tests = append(m.tests, fields[1])
			if len(fields) == 3 {
				m.every[fields[1]] = true
			}
		case fields[0] == "set" && len(fields) == 2:
			tests, err := decodeTests(fields[1], len(m.tests))
			if err != nil {
				return nil, bad(err)
			}
			sets = append(sets, tests)
		case fields[0] == "func" && len(fields) == 4:
			fi, err := strconv.Atoi(fields[1])
			if err != nil || fi < 0 || fi >= len(m.files) {
				return nil, bad(fmt.Errorf("no file %s", fields[1]))
			}
			si, err := strconv.Atoi(fields[3])
			if err != nil || si < 0 || si >= len(sets) {
				return nil, bad(fmt.Errorf("no set %s", fields[3]))
			}
			ref := funcRef{m.files[fi], fields[2]}
			m.funcs[ref] = append(m.funcs[ref], sets[si]...)
		default:
			return nil, bad(fmt.Errorf("unknown line"))
		}
	}
	return m, scan.Err()
}

func decodeTests(s string, count int) ([]int, error) {
	var out []int
	if bits, ok := strings.CutPrefix(s, "x"); ok {
		b, err := hex.DecodeString(bits)
		if err != nil {
			return nil, err
		}
		for i := range len(b) * 8 {
			if b[i/8]&(1<<(i%8)) != 0 {
				out = append(out, i)
			}
		}
	} else {
		for _, part := range strings.Split(s, ",") {
			i, err := strconv.Atoi(part)
			if err != nil {
				return nil, err
			}
			out = append(out, i)
		}
	}
	for _, i := range out {
		if i < 0 || i >= count {
			return nil, fmt.Errorf("no test %d", i)
		}
	}
	return out, nil
}

func encodeTests(tests []int) string {
	parts := make([]string, len(tests))
	for i, t := range tests {
		parts[i] = strconv.Itoa(t)
	}
	list := strings.Join(parts, ",")
	bits := make([]byte, tests[len(tests)-1]/8+1)
	for _, t := range tests {
		bits[t/8] |= 1 << (t % 8)
	}
	if hexed := "x" + hex.EncodeToString(bits); len(hexed) < len(list) {
		return hexed
	}
	return list
}

// buildMap turns the output of collect.sh into the map file. The profiles
// name files by import path, which is turned into a path in this repository
// with the module directories go list reports for the workspace.
func buildMap(dir, out, root string) error {
	modules, err := moduleDirs(root)
	if err != nil {
		return err
	}
	rel := func(importPath string) (string, bool) {
		best := ""
		for mod := range modules {
			if (importPath == mod || strings.HasPrefix(importPath, mod+"/")) && len(mod) > len(best) {
				best = mod
			}
		}
		if best == "" {
			return "", false
		}
		return path.Join(modules[best], strings.TrimPrefix(strings.TrimPrefix(importPath, best), "/")), true
	}

	// collect.sh writes failed.txt. The workflow renames it per shard.
	failed := map[string]bool{}
	failedLists, _ := filepath.Glob(filepath.Join(dir, "failed*.txt"))
	for _, list := range failedLists {
		data, err := os.ReadFile(list)
		if err != nil {
			return err
		}
		for _, name := range strings.Fields(string(data)) {
			failed[name] = true
		}
	}
	lists, err := filepath.Glob(filepath.Join(dir, "*.funcs"))
	if err != nil {
		return err
	}
	var names []string
	for _, list := range lists {
		if name := strings.TrimSuffix(filepath.Base(list), ".funcs"); strings.HasPrefix(name, "Test") {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return fmt.Errorf("no .funcs lists in %s", dir)
	}
	slices.Sort(names)

	ran := map[funcRef][]int{}
	fileSet := map[string]bool{}
	for ti, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name+".funcs"))
		if err != nil {
			return err
		}
		count := 0
		for line := range strings.Lines(string(data)) {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			p, ok := rel(fields[0])
			if !ok {
				continue
			}
			ref := funcRef{p, funcName(fields[1])}
			if l := ran[ref]; len(l) == 0 || l[len(l)-1] != ti {
				ran[ref] = append(l, ti)
			}
			fileSet[p] = true
			count++
		}
		// A test that ran no tuios code at all most likely drove the binary
		// some way the coverage run could not see. It runs on every change.
		if count == 0 {
			failed[name] = true
		}
	}
	files := slices.Sorted(func(yield func(string) bool) {
		for f := range fileSet {
			if !yield(f) {
				return
			}
		}
	})
	fileIndex := map[string]int{}
	for i, f := range files {
		fileIndex[f] = i
	}

	var b strings.Builder
	b.WriteString(mapHeader)
	// collect.sh leaves harness.cover when it measured the test process.
	if _, err := os.Stat(filepath.Join(dir, "harness.cover")); err == nil {
		b.WriteString("cover harness\n")
	}
	for _, f := range files {
		fmt.Fprintf(&b, "file %s\n", f)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "init.funcs")); err == nil {
		for line := range strings.Lines(string(data)) {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			if p, ok := rel(fields[0]); ok {
				fmt.Fprintf(&b, "init %s %s\n", p, funcName(fields[1]))
			}
		}
	}
	for _, name := range names {
		if failed[name] {
			fmt.Fprintf(&b, "test %s *\n", name)
		} else {
			fmt.Fprintf(&b, "test %s\n", name)
		}
	}
	refs := make([]funcRef, 0, len(ran))
	for ref := range ran {
		refs = append(refs, ref)
	}
	slices.SortFunc(refs, func(a, b funcRef) int {
		if c := strings.Compare(a.file, b.file); c != 0 {
			return c
		}
		return strings.Compare(a.name, b.name)
	})
	// Many functions ran in the same tests, so each set is written once.
	setIndex := map[string]int{}
	var funcLines strings.Builder
	for _, ref := range refs {
		enc := encodeTests(ran[ref])
		si, ok := setIndex[enc]
		if !ok {
			si = len(setIndex)
			setIndex[enc] = si
			fmt.Fprintf(&b, "set %s\n", enc)
		}
		fmt.Fprintf(&funcLines, "func %d %s %d\n", fileIndex[ref.file], ref.name, si)
	}
	b.WriteString(funcLines.String())
	return os.WriteFile(out, []byte(b.String()), 0o644)
}

// funcName drops type parameters, which go tool cover may print.
func funcName(s string) string {
	name, _, _ := strings.Cut(s, "[")
	return name
}

// moduleDirs maps each workspace module path to its directory relative to the
// repository root.
func moduleDirs(root string) (map[string]string, error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Path}} {{.Dir}}")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -m: %w", err)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	mods := map[string]string{}
	for line := range strings.Lines(string(out)) {
		mod, dir, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		r, err := filepath.Rel(absRoot, dir)
		if err != nil || strings.HasPrefix(r, "..") {
			continue
		}
		mods[mod] = filepath.ToSlash(r)
	}
	return mods, nil
}
