//go:build ignore

// gen_lexers copies the chroma lexer definitions the review highlights into this
// directory, gzipped. Run it after changing the chroma version in go.mod or the
// list below:
//
//	go run ./internal/diffview/lexers/gen.go
//
// chroma's lexers package embeds every one of its 279 definitions, about
// 2.4 MB of XML, and registers them all at start. The review needs the
// languages people keep in a repository, so tuios carries those, compressed,
// and registers them on the first highlight. A lexer that hands part of its
// text to another one (HTML to CSS and JavaScript, for one) needs that one
// too: the list is closed over those references here, so a missing one fails
// the generator rather than a highlight.
package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// wanted is the list of chroma definitions to carry, by file name. Go and
// Markdown are not here: chroma defines them in Go rather than XML, and
// coded.go carries a copy.
var wanted = []string{
	// Shells and build files.
	"bash", "fish", "powershell", "batchfile", "makefile", "cmake", "docker",
	"meson", "awk", "sed", "nix",
	// Systems languages.
	"c", "c++", "c#", "objective-c", "rust", "zig", "d", "nim", "odin", "v",
	"swift", "java", "kotlin", "scala", "groovy", "dart",
	// Scripting languages.
	"python", "ruby", "php", "perl", "lua", "tcl", "r", "julia", "crystal",
	// The web.
	"javascript", "typescript", "react", "html", "css", "scss", "sass", "vue",
	"graphql", "templ",
	// Functional languages.
	"haskell", "ocaml", "fsharp", "elixir", "erlang", "clojure", "elm", "gleam",
	// Data and configuration.
	"json", "yaml", "toml", "ini", "xml", "dtd", "properties", "hcl",
	"terraform", "protocol_buffer", "thrift", "cue", "jsonnet", "kdl", "rego",
	"systemd", "nginx_configuration_file",
	// Other.
	"sql", "diff", "bash_session", "vhs", "tex", "typst", "viml", "matlab", "fortran", "solidity",
	"glsl", "hlsl", "verilog", "systemverilog", "vhdl",
	// Go's text/template, which the Go lexer uses for raw strings.
	"go_template",
}

// coded names the lexers chroma defines in Go, which coded.go carries. A
// reference to one of them is resolved without a file.
var coded = map[string]bool{"go": true, "golang": true, "markdown": true, "md": true}

var (
	nameRe  = regexp.MustCompile(`<name>(.*?)</name>`)
	aliasRe = regexp.MustCompile(`<alias>(.*?)</alias>`)
	usingRe = regexp.MustCompile(`using lexer="([^"]*)"`)
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen_lexers:", err)
		os.Exit(1)
	}
}

func run() error {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/alecthomas/chroma/v2").Output()
	if err != nil {
		return fmt.Errorf("finding chroma: %w", err)
	}
	src := filepath.Join(strings.TrimSpace(string(out)), "lexers", "embedded")

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	byName := map[string]string{} // lower-case name or alias -> file stem
	for _, e := range entries {
		stem, ok := strings.CutSuffix(e.Name(), ".xml")
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if m := nameRe.FindSubmatch(data); m != nil {
			byName[strings.ToLower(string(m[1]))] = stem
		}
		for _, m := range aliasRe.FindAllSubmatch(data, -1) {
			byName[strings.ToLower(string(m[1]))] = stem
		}
	}

	have := map[string]bool{}
	todo := append([]string(nil), wanted...)
	for len(todo) > 0 {
		stem := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if have[stem] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, stem+".xml"))
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte("usingbygroup")) {
			return fmt.Errorf("%s picks a lexer by name at run time; carry it only with a check that every name it can pick is here", stem)
		}
		have[stem] = true
		for _, m := range usingRe.FindAllSubmatch(data, -1) {
			ref := strings.ToLower(string(m[1]))
			if coded[ref] {
				continue
			}
			dep, ok := byName[ref]
			if !ok {
				return fmt.Errorf("%s uses lexer %q, which chroma does not define", stem, ref)
			}
			todo = append(todo, dep)
		}
	}

	dst := "internal/diffview/lexers"
	old, _ := filepath.Glob(filepath.Join(dst, "*.xml.gz"))
	for _, f := range old {
		if err := os.Remove(f); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	stems := make([]string, 0, len(have))
	for s := range have {
		stems = append(stems, s)
	}
	sort.Strings(stems)
	for _, stem := range stems {
		data, err := os.ReadFile(filepath.Join(src, stem+".xml"))
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			return err
		}
		// No name and no time in the header, so a rerun on the same chroma
		// writes the same bytes.
		if _, err := zw.Write(data); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, stem+".xml.gz"), buf.Bytes(), 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("wrote %d lexers to %s\n", len(stems), dst)
	return nil
}
