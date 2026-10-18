package tml

import (
	"strings"
	"testing"
)

// TestNoEscapes checks the security boundary: no control character, C1
// control or bidi override survives parsing, in either encoding, from a
// literal or from a character reference.
func TestNoEscapes(t *testing.T) {
	docs := []string{
		"<tml version=\"1\"><text>red \x1b[31mtext\x07</text></tml>",
		`<tml version="1"><text>&#27;]52;c;aGk=&#7; &#x9b;2J &#x202e;evil</text></tml>`,
		"<tml version=\"1\"><card title=\"a\x1bb&#155;c&#x2066;d\"/></tml>",
		"{\"type\":\"tml\",\"version\":1,\"title\":\"\\u001b[2J\",\"children\":[{\"type\":\"text\",\"children\":[\"\\u009b31m\\u202ex\\u0000\"]}]}",
		"<tml version=\"1\"><code>\x1bP\x9c raw\r\n</code></tml>",
	}
	for _, src := range docs {
		doc, err := Parse([]byte(src), nil)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		checkTree(t, doc)
		out := string(FormatXML(doc)) + string(FormatJSON(doc, false))
		for _, bad := range []string{"\x1b", "\x07", "\x9b", "\u009b", "\u202e", "\u2066", "\x00", "\r"} {
			if strings.Contains(out, bad) {
				t.Errorf("%q survived in %q", bad, out)
			}
		}
	}
	if got := Sanitize("ok\ttab\nline\xffbad"); got != "ok\ttab\nline\ufffdbad" {
		t.Errorf("Sanitize kept or broke something: %q", got)
	}
}

// TestParseLimits checks the size, node and depth limits in both parsers.
func TestParseLimits(t *testing.T) {
	lim := DefaultLimits()
	big := "<tml version=\"1\"><text>" + strings.Repeat("x", lim.DocBytes) + "</text></tml>"
	if _, err := ParseXML([]byte(big), nil); err == nil {
		t.Error("a document past the size limit parsed")
	}
	if _, err := ParseXML([]byte(big), &ParseOptions{Limits: ptr(PersonLimits())}); err != nil {
		t.Errorf("the person's limit refused %d bytes: %v", len(big), err)
	}
	many := "<tml version=\"1\"><col>" + strings.Repeat("<spacer/>", lim.Nodes) + "</col></tml>"
	if _, err := ParseXML([]byte(many), nil); err == nil {
		t.Error("a document past the node limit parsed")
	}
	manyJSON := `{"type":"col","children":[` + strings.TrimSuffix(strings.Repeat(`{"type":"spacer"},`, lim.Nodes), ",") + `]}`
	if _, err := ParseJSON([]byte(manyJSON), nil); err == nil {
		t.Error("a JSON document past the node limit parsed")
	}
	deep := strings.Repeat(`{"type":"col","children":[`, lim.Depth+1) + strings.Repeat(`]}`, lim.Depth+1)
	if _, err := ParseJSON([]byte(deep), nil); err == nil {
		t.Error("a JSON document past the depth limit parsed")
	}
	ok := strings.Repeat(`{"type":"col","children":[`, lim.Depth-1) + `{"type":"spacer"}` + strings.Repeat(`]}`, lim.Depth-1)
	if _, err := ParseJSON([]byte(ok), nil); err != nil {
		t.Errorf("a document at the depth limit did not parse: %v", err)
	}
}

// TestValidateLimits checks the limits only the validator can see.
func TestValidateLimits(t *testing.T) {
	images := `<tml version="1"><col>` + strings.Repeat(`<image src="file:a.png" alt="a"/>`, 5) + `</col></tml>`
	rows := make([]string, 2001)
	for i := range rows {
		rows[i] = "1"
	}
	each := `<tml version="1"><state>{"xs":[` + strings.Join(rows, ",") + `]}</state><col><text each="state.xs">{item}</text></col></tml>`
	for name, src := range map[string]string{"images": images, "each": each} {
		_, problems, err := Check([]byte(src), nil)
		if err == nil || problems[len(problems)-1].Code != CodeLimit {
			t.Errorf("%s: want a limit problem, got %v", name, problems)
		}
	}
	var sb strings.Builder
	sb.WriteString(`<tml version="1"><col>`)
	for range 30 {
		sb.WriteString(`<nope/>`)
	}
	sb.WriteString(`</col></tml>`)
	_, problems, _ := Check([]byte(sb.String()), nil)
	if len(problems) != DefaultLimits().Problems {
		t.Errorf("reported %d problems, want the cap of %d", len(problems), DefaultLimits().Problems)
	}
}

func ptr[T any](v T) *T { return &v }
