package tml

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func readFile(t testing.TB, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func golden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want := readFile(t, path)
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden file. Run go test -update after checking the change.\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

func examples(t testing.TB) []string {
	t.Helper()
	files, err := filepath.Glob("testdata/examples/*")
	if err != nil || len(files) == 0 {
		t.Fatalf("no examples: %v", err)
	}
	return files
}

// TestExamples holds every example of the design to the validator and to
// both encodings: each one validates with no error, prints to its golden
// TML text and JSON, and each golden reads back to the other one exactly.
func TestExamples(t *testing.T) {
	for _, file := range examples(t) {
		base := filepath.Base(file)
		name := strings.TrimSuffix(base, filepath.Ext(base))
		if filepath.Ext(base) == ".json" {
			name += ".from-json"
		}
		t.Run(base, func(t *testing.T) {
			doc, problems, err := Check(readFile(t, file), nil)
			if err != nil {
				t.Fatalf("the example does not validate: %v\n%v", err, problems)
			}
			for _, p := range problems {
				t.Errorf("unexpected warning: %s", p)
			}
			xmlPath := filepath.Join("testdata", "golden", name+".tml")
			jsonPath := filepath.Join("testdata", "golden", name+".json")
			golden(t, xmlPath, FormatXML(doc))
			golden(t, jsonPath, FormatJSON(doc, true))
			if *update {
				return
			}
			fromXML, err := ParseXML(readFile(t, xmlPath), nil)
			if err != nil {
				t.Fatalf("golden TML text does not parse: %v", err)
			}
			if got := FormatJSON(fromXML, true); !bytes.Equal(got, readFile(t, jsonPath)) {
				t.Errorf("TML text to JSON is not lossless:\n%s", got)
			}
			fromJSON, err := ParseJSON(readFile(t, jsonPath), nil)
			if err != nil {
				t.Fatalf("golden JSON does not parse: %v", err)
			}
			if got := FormatXML(fromJSON); !bytes.Equal(got, readFile(t, xmlPath)) {
				t.Errorf("JSON to TML text is not lossless:\n%s", got)
			}
		})
	}
}

// TestTwins checks that the design's TML text and JSON twin are one tree.
func TestTwins(t *testing.T) {
	x, err := Parse(readFile(t, "testdata/examples/build.tml"), nil)
	if err != nil {
		t.Fatal(err)
	}
	j, err := Parse(readFile(t, "testdata/examples/build.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := FormatJSON(x, false), FormatJSON(j, false); !bytes.Equal(a, b) {
		t.Errorf("the twins differ:\n%s\n%s", a, b)
	}
}

// TestErrorGoldens records the problems of each broken document: the code,
// the JSON Pointer, the position, the suggestion and the accepted values.
func TestErrorGoldens(t *testing.T) {
	files, _ := filepath.Glob("testdata/errors/*")
	n := 0
	for _, file := range files {
		if strings.HasSuffix(file, ".golden") {
			continue
		}
		n++
		t.Run(filepath.Base(file), func(t *testing.T) {
			_, problems, err := Check(readFile(t, file), nil)
			if err == nil {
				t.Errorf("the document validates; it should not")
			}
			out, _ := json.MarshalIndent(problems, "", "  ")
			golden(t, file+".golden", append(out, '\n'))
		})
	}
	if n < 20 {
		t.Fatalf("only %d error fixtures", n)
	}
}

// TestChartBar is the case the design names: an unknown component gets the
// closest one as a suggestion.
func TestChartBar(t *testing.T) {
	_, problems, err := Check([]byte(`<tml version="1"><chart-bar data="state.x"/></tml>`), nil)
	if err == nil || len(problems) != 1 {
		t.Fatalf("want one problem, got %v", problems)
	}
	p := problems[0]
	if p.Code != CodeUnknownComponent || p.DidYouMean != "bar" || p.At != "/children/0" || p.Line != 1 || p.Col != 18 {
		t.Errorf("got %+v", p)
	}
	var e *Error
	if err, ok := err.(*Error); ok {
		e = err
	}
	b, _ := json.Marshal(e)
	if !bytes.Contains(b, []byte(`"code":"invalid_tml"`)) || !bytes.Contains(b, []byte(`"did_you_mean":"bar"`)) {
		t.Errorf("error object: %s", b)
	}
}

// TestSchemaGolden keeps the committed schema equal to the generated one.
// Run go generate after a catalog change.
func TestSchemaGolden(t *testing.T) {
	got := Schema()
	if !json.Valid(got) {
		t.Fatal("the schema is not valid JSON")
	}
	golden(t, "tml.schema.json", got)
	var s struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(got, &s); err != nil {
		t.Fatal(err)
	}
	for _, c := range Components() {
		if _, ok := s.Defs[c.Name]; !ok {
			t.Errorf("the schema has no definition for %s", c.Name)
		}
	}
}

// TestWhitespace checks the text rules that make the encodings lossless:
// white space the parser collapses is escaped by the printer.
func TestWhitespace(t *testing.T) {
	texts := []string{
		"plain", " lead", "trail ", "two  spaces", "line\nbreak", "tab\there",
		"   ", "a & b < c > d", "{state.x}", "&#10;", "mixed \n\t  all ",
	}
	for _, parent := range []string{"text", "b", "card", "code"} {
		for _, s := range texts {
			inner, _ := json.Marshal(s)
			src := `{"type":"tml","version":1,"children":[{"type":"text","children":[{"type":"` + parent + `","children":[` + string(inner) + `]}]}]}`
			if parent == "card" || parent == "code" {
				src = `{"type":"tml","version":1,"children":[{"type":"` + parent + `","children":[` + string(inner) + `]}]}`
			}
			doc, err := ParseJSON([]byte(src), nil)
			if err != nil {
				t.Fatalf("%s %q: %v", parent, s, err)
			}
			back, err := ParseXML(FormatXML(doc), nil)
			if err != nil {
				t.Fatalf("%s %q: %v\n%s", parent, s, err, FormatXML(doc))
			}
			if a, b := FormatJSON(doc, false), FormatJSON(back, false); !bytes.Equal(a, b) {
				t.Errorf("%s %q is not lossless:\n%s\n%s\n%s", parent, s, FormatXML(doc), a, b)
			}
		}
	}
}

// TestTypedAttributes checks that both parsers give an attribute the type
// the catalog names, so the encodings agree.
func TestTypedAttributes(t *testing.T) {
	x, err := ParseXML([]byte(`<tml version="1"><grid cols="3" gap="1.50"><spacer grow="1"/></grid><sparkline values="[1,2, 3]"/><card fold="true" title="7"/></tml>`), nil)
	if err != nil {
		t.Fatal(err)
	}
	j, err := ParseJSON([]byte(`{"type":"tml","version":"1","children":[{"type":"grid","cols":3,"gap":1.50,"children":[{"type":"spacer","grow":1}]},{"type":"sparkline","values":[1,2,3]},{"type":"card","fold":"true","title":7}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	a, b := FormatJSON(x, false), FormatJSON(j, false)
	if !bytes.Equal(a, b) {
		t.Errorf("the encodings disagree:\n%s\n%s", a, b)
	}
	want := `{"type":"tml","version":1,"children":[{"type":"grid","cols":3,"gap":1.50,"children":[{"type":"spacer","grow":1}]},{"type":"sparkline","values":[1,2,3]},{"type":"card","fold":true,"title":"7"}]}`
	if string(a) != want {
		t.Errorf("got  %s\nwant %s", a, want)
	}
}

// TestReadme keeps the README's component table and function list complete.
func TestReadme(t *testing.T) {
	readme := string(readFile(t, "README.md"))
	for _, c := range Components() {
		if !strings.Contains(readme, "| `"+c.Name+"` |") {
			t.Errorf("the README has no row for %s", c.Name)
		}
	}
	for _, f := range Funcs() {
		if !strings.Contains(readme, "`"+f+"(") && !strings.Contains(readme, "`"+f+"`") {
			t.Errorf("the README does not list the function %s", f)
		}
	}
	for _, a := range ActionNames() {
		if !strings.Contains(readme, "`"+a+"(") {
			t.Errorf("the README does not list the action %s", a)
		}
	}
}

// TestReadmeCode keeps each Go block of the README equal to code in
// example_test.go, which compiles and runs, so the README code compiles too.
func TestReadmeCode(t *testing.T) {
	readme := string(readFile(t, "README.md"))
	examples := string(readFile(t, "example_test.go"))
	blocks := 0
	for _, part := range strings.Split(readme, "```go\n")[1:] {
		code, _, _ := strings.Cut(part, "```")
		blocks++
		var indented strings.Builder
		for _, line := range strings.SplitAfter(code, "\n") {
			if strings.TrimSpace(line) != "" {
				indented.WriteString("\t")
			}
			indented.WriteString(line)
		}
		if !strings.Contains(examples, indented.String()) {
			t.Errorf("this README block is not in example_test.go:\n%s", code)
		}
	}
	if blocks < 2 {
		t.Errorf("found %d Go blocks in the README", blocks)
	}
}
