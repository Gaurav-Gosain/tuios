package lint

import (
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	// A go build or go install of the tuios command itself, not of
	// cmd/tuios-web or cmd/tuios-wasm.
	buildsTuios = regexp.MustCompile(`\bgo (build|install)\b.*\./cmd/tuios(\s|$|["')])`)
	// The same in Go source: the command's arguments as string literals.
	goBuildsTuios = regexp.MustCompile(`"build".*"\./cmd/tuios"`)
)

// TestEveryTuiosBuildReadsTheNoinlineList is the one-list rule for the
// release flags. A shipped tuios is built with inlining off in the packages
// scripts/noinline-packages.txt lists, which takes about 1 MB off the binary
// (docs/perf.md, "Binary size budget"). Go has no file that carries build
// flags, so each build path has to read the list itself: a shell line through
// scripts/goflags.sh, GoReleaser through TUIOS_GOFLAGS, the Nix package and Go
// code through the file. A build path that skips it builds a binary of
// another size, and a binary-size job that skips it measures one nobody ships.
//
// A build that only checks the code compiles, and ships nothing, carries
// tuios:allow-build and its reason on the line.
func TestEveryTuiosBuildReadsTheNoinlineList(t *testing.T) {
	root := repoRoot(t)
	read := func(rel string) string {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			// Dot folders other than .github are tool state and other
			// checkouts, not this repository's build paths.
			n := d.Name()
			if rel == "docs" || n == "node_modules" || n == "testdata" || n == "vendor" ||
				rel != "." && strings.HasPrefix(n, ".") && n != ".github" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		switch {
		case name == ".goreleaser.yml" || name == "tuios.nix":
			return nil // below
		case strings.HasSuffix(name, ".go"):
			if !strings.HasPrefix(rel, "internal/lint/") {
				checkGoBuilds(t, rel, read(rel))
			}
		case name == "justfile" || name == "Dockerfile" || path.Ext(name) == ".sh" ||
			path.Ext(name) == ".yml" || path.Ext(name) == ".yaml":
			checkShellBuilds(t, rel, read(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// GoReleaser: every build of ./cmd/tuios takes GOFLAGS from
	// TUIOS_GOFLAGS, and the release workflow sets it from the script.
	builds := 0
	for block := range strings.SplitSeq(read(".goreleaser.yml"), "\n  - id: ") {
		if !strings.Contains(block, "main: ./cmd/tuios\n") {
			continue
		}
		builds++
		if !strings.Contains(block, "- GOFLAGS={{ .Env.TUIOS_GOFLAGS }}") {
			id, _, _ := strings.Cut(block, "\n")
			t.Errorf(".goreleaser.yml: build %s has no GOFLAGS={{ .Env.TUIOS_GOFLAGS }} in its env", id)
		}
	}
	if builds == 0 {
		t.Error(".goreleaser.yml: no build of ./cmd/tuios found; the check no longer reads the file right")
	}
	if !strings.Contains(read(".github/workflows/release.yml"), `TUIOS_GOFLAGS=$(scripts/goflags.sh)`) {
		t.Error(`release.yml does not set TUIOS_GOFLAGS=$(scripts/goflags.sh) for GoReleaser`)
	}
	if nix := read("tuios.nix"); !strings.Contains(nix, "./scripts/noinline-packages.txt") && !strings.Contains(nix, "tuios:allow-build ") {
		t.Error("tuios.nix does not build its GOFLAGS from ./scripts/noinline-packages.txt, and has no tuios:allow-build with the reason")
	}

	// A pattern that matches nothing turns inlining off nowhere. A package
	// renamed or moved would drop out of the list without a word.
	list := exec.Command("go", "list", "-deps", "./cmd/tuios")
	list.Dir = root
	deps, err := list.Output()
	if err != nil {
		t.Fatalf("go list -deps ./cmd/tuios: %v", err)
	}
	linked := strings.Fields(string(deps))
	for line := range strings.Lines(read("scripts/noinline-packages.txt")) {
		line, _, _ = strings.Cut(line, "#")
		pattern := strings.TrimSpace(line)
		if pattern == "" {
			continue
		}
		if !matchesAny(pattern, linked) {
			t.Errorf("scripts/noinline-packages.txt: %s matches no package tuios links", pattern)
		}
	}
}

// matchesAny reports whether a go build package pattern, either an import
// path or one that ends in /..., names one of pkgs.
func matchesAny(pattern string, pkgs []string) bool {
	prefix, tree := strings.CutSuffix(pattern, "/...")
	for _, p := range pkgs {
		if p == prefix || tree && strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// checkShellBuilds holds a shell, YAML, justfile or Dockerfile to the rule:
// a command that builds ./cmd/tuios runs with GOFLAGS from
// scripts/goflags.sh. A command continued with a trailing backslash counts
// as one line.
func checkShellBuilds(t *testing.T, rel, text string) {
	t.Helper()
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		start := i
		cmd := lines[i]
		for strings.HasSuffix(strings.TrimRight(cmd, " \t"), `\`) && i+1 < len(lines) {
			i++
			cmd += "\n" + lines[i]
		}
		first, _, _ := strings.Cut(strings.TrimSpace(cmd), " ")
		if first == "#" || first == "echo" || first == "printf" || !buildsTuios.MatchString(cmd) ||
			strings.Contains(cmd, "tuios:allow-build ") {
			continue
		}
		if !strings.Contains(cmd, "scripts/goflags.sh") {
			t.Errorf(`%s:%d: builds ./cmd/tuios without GOFLAGS="$(scripts/goflags.sh)"`, rel, start+1)
		}
	}
}

// checkGoBuilds holds Go source to the rule: an exec of go build
// ./cmd/tuios is in a function that reads the list with
// hostsync.NoinlineGoFlags, or goes through the e2e suite's goBuild.
func checkGoBuilds(t *testing.T, rel, text string) {
	t.Helper()
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if !goBuildsTuios.MatchString(line) || strings.Contains(line, "tuios:allow-build ") {
			continue
		}
		start, end := i, i
		for start > 0 && !strings.HasPrefix(lines[start], "func ") {
			start--
		}
		for end < len(lines)-1 && lines[end] != "}" {
			end++
		}
		if !strings.Contains(strings.Join(lines[start:end+1], "\n"), "NoinlineGoFlags(") {
			t.Errorf("%s:%d: builds ./cmd/tuios without the noinline list: use hostsync.NoinlineGoFlags, or goBuild in e2e/tui", rel, i+1)
		}
	}
}
