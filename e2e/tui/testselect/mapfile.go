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
	"strings"
)

// testMap is what the coverage run learned: for each test, the source files
// whose code it ran, outside the code every process runs at start-up.
type testMap struct {
	files []string            // repository-relative paths, indexed by the bitsets
	tests map[string][]int    // test name to indexes into files
	every map[string]bool     // tests to run on every change that runs any test
	init  map[string][]string // file to the functions every process runs in it
}

// packages returns the directories of the files a test ran, which are the
// packages it reached.
func (m *testMap) packages(test string) map[string]bool {
	out := map[string]bool{}
	for _, i := range m.tests[test] {
		out[path.Dir(m.files[i])] = true
	}
	return out
}

// knownPackages returns every package some test reached.
func (m *testMap) knownPackages() map[string]bool {
	out := map[string]bool{}
	for _, f := range m.files {
		out[path.Dir(f)] = true
	}
	return out
}

const mapHeader = `# The end-to-end test map: for each test, the source files whose code it ran.
# testselect reads it to run only the tests a change can reach. It is written
# by the e2e-tui test map workflow (.github/workflows/e2e-tui-map.yml), which
# runs every test against a tuios built with -cover. Do not edit it by hand.
#
#   file PATH            a source file; the files are numbered in order from 0
#   init PATH FUNC       a function every tuios process runs at start-up
#   test NAME HEX        the files the test ran, as a bitset: bit i of byte j
#                        is file 8*j+i
#   test NAME *          the test runs on every change that runs any test
`

func readMap(name string) (*testMap, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := &testMap{tests: map[string][]int{}, every: map[string]bool{}, init: map[string][]string{}}
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 1<<20), 1<<20)
	for n := 1; scan.Scan(); n++ {
		line := scan.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		switch {
		case fields[0] == "file" && len(fields) == 2:
			m.files = append(m.files, fields[1])
		case fields[0] == "init" && len(fields) == 3:
			m.init[fields[1]] = append(m.init[fields[1]], fields[2])
		case fields[0] == "test" && len(fields) == 3 && fields[2] == "*":
			m.every[fields[1]] = true
			m.tests[fields[1]] = nil
		case fields[0] == "test" && len(fields) == 3:
			bits, err := hex.DecodeString(fields[2])
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %v", name, n, err)
			}
			var idx []int
			for i := range len(bits) * 8 {
				if bits[i/8]&(1<<(i%8)) != 0 {
					if i >= len(m.files) {
						return nil, fmt.Errorf("%s:%d: file %d is not listed", name, n, i)
					}
					idx = append(idx, i)
				}
			}
			m.tests[fields[1]] = idx
		default:
			return nil, fmt.Errorf("%s:%d: cannot read %q", name, n, line)
		}
	}
	return m, scan.Err()
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
	lists, err := filepath.Glob(filepath.Join(dir, "*.files"))
	if err != nil {
		return err
	}
	if len(lists) == 0 {
		return fmt.Errorf("no .files lists in %s", dir)
	}
	index := map[string]int{}
	var files []string
	tests := map[string][]string{}
	for _, list := range lists {
		name := strings.TrimSuffix(filepath.Base(list), ".files")
		data, err := os.ReadFile(list)
		if err != nil {
			return err
		}
		var mine []string
		for _, importPath := range strings.Fields(string(data)) {
			if p, ok := rel(importPath); ok {
				mine = append(mine, p)
				if _, seen := index[p]; !seen {
					index[p] = 0
					files = append(files, p)
				}
			}
		}
		// A test that ran no tuios code at all most likely drove the binary
		// some way the coverage run could not see. It runs on every change.
		if len(mine) == 0 {
			failed[name] = true
		}
		tests[name] = mine
	}
	slices.Sort(files)
	for i, f := range files {
		index[f] = i
	}

	var b strings.Builder
	b.WriteString(mapHeader)
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
				fmt.Fprintf(&b, "init %s %s\n", p, fields[1])
			}
		}
	}
	names := make([]string, 0, len(tests))
	for name := range tests {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if failed[name] {
			fmt.Fprintf(&b, "test %s *\n", name)
			continue
		}
		bits := make([]byte, (len(files)+7)/8)
		for _, f := range tests[name] {
			i := index[f]
			bits[i/8] |= 1 << (i % 8)
		}
		end := len(bits)
		for end > 0 && bits[end-1] == 0 {
			end--
		}
		fmt.Fprintf(&b, "test %s %s\n", name, hex.EncodeToString(bits[:end]))
	}
	return os.WriteFile(out, []byte(b.String()), 0o644)
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
