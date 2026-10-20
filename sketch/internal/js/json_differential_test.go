//go:build differential

package js

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const jsonScript = `
const fs = require('fs');
const [,, input, output] = process.argv;
const texts = JSON.parse(fs.readFileSync(input, 'utf8'));
const out = texts.map((t) => { try { return 'V:' + JSON.stringify(JSON.parse(t)); } catch (e) { return 'E:' + e.message; } });
fs.writeFileSync(output, JSON.stringify(out));
`

// stringify is JSON.stringify of a value ParseJSON gives.
func stringify(b *strings.Builder, v any) {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(t))
	case float64:
		if math.IsInf(t, 0) || math.IsNaN(t) {
			b.WriteString("null")
		} else {
			b.WriteString(Num(t))
		}
	case string:
		quote(b, t)
	case []any:
		b.WriteByte('[')
		for k, e := range t {
			if k > 0 {
				b.WriteByte(',')
			}
			stringify(b, e)
		}
		b.WriteByte(']')
	case *JSONObject:
		b.WriteByte('{')
		for k, key := range t.Keys {
			if k > 0 {
				b.WriteByte(',')
			}
			quote(b, key)
			b.WriteByte(':')
			stringify(b, t.Values[key])
		}
		b.WriteByte('}')
	}
}

func quote(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00` + strconv.FormatInt(int64(r)>>4, 16) + strconv.FormatInt(int64(r)&15, 16))
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// jsonInputs are JSON texts and mutations of them, short ones and long ones, so that
// every message and every form of the context in one is reached.
func jsonInputs() []string {
	seeds := []string{`{}`, `{"a":1}`, `{"a": [1, 2, {"b": null}], "c": true, "d": false}`, `{"name": "x", "nested": {"deep": {"deeper": [1.5, -2e3, 0.25E-2]}}}`,
		`{"2": "b", "1": "a", "x": 1, "1": "c", "10": 0, "4294967295": 1, "01": 2}`, `{"s": "a\"b\\c\/d\b\f\n\r\t\u00e9\u0041"}`, `[1, [2, [3]]]`, `"str"`, `-0`, `12345678901234567890`,
		`{"long key that goes on and on for a while": "and a long value as well, long enough to need context"}`, "{\r\n  \"a\": 1,\r\n  \"b\": [\n 2\n ]\n}", `{"é": "😀", "k": "中文"}`, `"\\`, `"\\u12`, `tr`, `[1,`, `1e+`, `"\\ÿ"`, `"\\Ā"`}
	frags := []string{"{", "}", "[", "]", `"`, ",", ":", "0", "1", "9", "-", "+", ".", "e", "E", "t", "r", "u", "f", "a", "l", "s", "n", " ", "\n", "\r", "\t", "\\", "/", "u", "x", "\x01", "é", "😀", "true", "null", "\\u12", "\\uZZ", "01", "-a", "1.", "1e", "'"}
	rng := rand.New(rand.NewPCG(5, 6))
	out := append([]string{}, seeds...)
	for range 200_000 {
		s := []rune(seeds[rng.IntN(len(seeds))])
		for range 1 + rng.IntN(3) {
			pos := rng.IntN(len(s) + 1)
			f := []rune(frags[rng.IntN(len(frags))])
			switch rng.IntN(3) {
			case 0:
				s = append(s[:pos], append(f, s[pos:]...)...)
			case 1:
				if pos < len(s) {
					s = append(s[:pos], s[pos+1:]...)
				}
			default:
				if pos < len(s) {
					s = append(s[:pos], append(f, s[pos+1:]...)...)
				}
			}
		}
		out = append(out, string(s))
	}
	return out
}

// TestParseJSONAgainstNode checks ParseJSON, its values and its messages, against node.
// Run it with node on the path: go test -tags differential -run TestParseJSONAgainstNode ./internal/js/
func TestParseJSONAgainstNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on the path")
	}
	texts := jsonInputs()
	dir := t.TempDir()
	in, out, script := filepath.Join(dir, "in"), filepath.Join(dir, "out"), filepath.Join(dir, "json.cjs")
	data, _ := json.Marshal(texts)
	os.WriteFile(in, data, 0o600)
	os.WriteFile(script, []byte(jsonScript), 0o600)
	if msg, err := exec.Command(node, script, in, out).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, msg)
	}
	data, err = os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	bad, errs := 0, 0
	for i, text := range texts {
		v, err := ParseJSON(UTF16(text))
		var got string
		if err != nil {
			got = "E:" + err.Error()
			errs++
		} else {
			var b strings.Builder
			stringify(&b, v)
			got = "V:" + b.String()
		}
		if got != want[i] {
			bad++
			if bad <= 10 {
				t.Errorf("%q:\n got %s\nwant %s", text, got, want[i])
			}
		}
	}
	t.Logf("%d texts, %d rejected, %d differ", len(texts), errs, bad)
}
