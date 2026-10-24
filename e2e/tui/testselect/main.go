// Command testselect picks the end-to-end tests a change can affect, so a pull
// request runs those and not the whole suite.
//
// The suite drives a real tuios binary, so the imports of a test file say
// nothing about the code it reaches. The test map (testmap.txt) says it
// instead: a coverage run records, for each test, the source files whose code
// it ran. testselect reads the diff between two commits and keeps a test when
// the change can reach it:
//
//   - A change inside the body of a function selects the tests that ran code
//     in that file. A function body only matters to a test that runs it.
//   - Any other change to a package of the binary (a declaration, a new or
//     removed file, an embedded file, a function every process runs at
//     start-up) selects the tests that ran code in that package or in a
//     package that imports it directly.
//   - A change to a test file of the suite selects the tests it changes, or
//     every test in the file when a helper changed that only that file uses.
//   - A test the map does not know, or one the map marks with "*", runs on
//     every change that reaches the binary or the suite.
//
// Everything that cannot be judged runs the whole suite: the CI files, the
// harness and anything else in e2e/tui, go.mod, go.sum and go.work beyond
// lines that pin a tuios.dev library, a package the harness itself imports,
// and any path the rules do not name. Documentation, and Go packages that
// neither the binary nor the suite builds, run nothing.
//
// Usage:
//
//	go test -list '^Test' ./... | testselect -base BASE -head HEAD
//	    prints the selected tests, one per line, and on stderr why.
//	testselect -build DIR
//	    writes testmap.txt from the output of collect.sh in DIR.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

func main() {
	base := flag.String("base", "", "the commit the change is measured from; empty runs every test")
	head := flag.String("head", "HEAD", "the commit under test")
	mapFile := flag.String("map", "testselect/testmap.txt", "the test map")
	root := flag.String("root", "../..", "the repository root")
	build := flag.String("build", "", "write the map from the collect.sh output in this directory")
	summary := flag.String("summary", "", "append a Markdown summary of the selection to this file")
	flag.Parse()

	if *build != "" {
		if err := buildMap(*build, *mapFile, *root); err != nil {
			fail(err)
		}
		return
	}

	names, err := readNames(os.Stdin)
	if err != nil {
		fail(err)
	}
	if len(names) == 0 {
		fail(fmt.Errorf("no test names on stdin: run go test -list with TUIOS_E2E set"))
	}

	var sel *selection
	if *base == "" {
		sel = everything("no base commit was given")
	} else {
		m, err := readMap(*mapFile)
		if err != nil {
			sel = everything(fmt.Sprintf("the test map cannot be read: %v", err))
		} else {
			sel = choose(*root, *base, *head, *mapFile, m, names)
		}
	}

	chosen := names
	if !sel.all {
		chosen = nil
		for _, name := range names {
			if sel.tests[name] {
				chosen = append(chosen, name)
			}
		}
	}
	for _, name := range chosen {
		fmt.Println(name)
	}
	report := sel.report(len(chosen), len(names))
	fmt.Fprint(os.Stderr, report)
	if *summary != "" {
		f, err := os.OpenFile(*summary, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString(report)
			_ = f.Close()
		}
	}
}

func readNames(r io.Reader) ([]string, error) {
	var names []string
	scan := bufio.NewScanner(r)
	for scan.Scan() {
		if line := strings.TrimSpace(scan.Text()); strings.HasPrefix(line, "Test") {
			names = append(names, line)
		}
	}
	slices.Sort(names)
	return slices.Compact(names), scan.Err()
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "testselect:", err)
	os.Exit(2)
}
