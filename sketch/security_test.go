package sketch

// Ported from pele tests/support/inert.ts, tests/support/xml.ts and the flowchart
// part of tests/render/security.test.ts, and the internal links of tests/util/url.test.ts, at 8aff183be57021b533f319a29341a529be934b08.

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
	"tuios.dev/sketch/internal/util"
)

var (
	reXMLTag  = regexp.MustCompile(`^<(/?)([A-Za-z][\w:.-]*)((?:[` + js.S + `]+[\w:.-]+="[^"<]*")*)[` + js.S + `]*(/?)>`)
	reXMLAttr = regexp.MustCompile(`([^` + js.S + `=]+)="[^"]*"`)
	reAllTags = regexp.MustCompile(`<([A-Za-z][\w:.-]*)((?:[` + js.S + `]+[\w:.-]+="[^"<]*")*)[` + js.S + `]*/?>`)
	reOneAttr = regexp.MustCompile(`([\w:.-]+)="([^"<]*)"`)
	reEntOK   = regexp.MustCompile(`^(?:amp|lt|gt|quot|apos|#\d+|#x[0-9a-fA-F]+);`)
	reHexRef  = regexp.MustCompile(`&#x([0-9a-fA-F]+);`)
	reDecRef  = regexp.MustCompile(`&#(\d+);`)
)

// badAmp reports an & that does not start a character reference.
func badAmp(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '&' && !reEntOK.MatchString(s[i+1:]) {
			return true
		}
	}
	return false
}

// assertWellFormed checks that a string is well-formed XML: balanced tags, quoted attributes, escaped text.
func assertWellFormed(xml string) error {
	if i := strings.IndexFunc(xml, func(r rune) bool {
		return r <= 0x08 || r == 0x0b || r == 0x0c || (r >= 0x0e && r <= 0x1f) || r == 0x7f || r == 0xfffe || r == 0xffff
	}); i >= 0 {
		r, _ := utf8.DecodeRuneInString(xml[i:])
		return fmt.Errorf("character U+%04X near %q", r, xml[max(0, i-20):min(len(xml), i+20)])
	}
	var stack []string
	i := 0
	for i < len(xml) {
		lt := strings.IndexByte(xml[i:], '<')
		var text string
		if lt == -1 {
			text = xml[i:]
		} else {
			lt += i
			text = xml[i:lt]
		}
		if strings.Contains(text, ">") {
			return fmt.Errorf("unescaped > in text near %q", text[:min(40, len(text))])
		}
		if badAmp(text) {
			return fmt.Errorf("unescaped & in text near %q", text[:min(40, len(text))])
		}
		if lt == -1 {
			break
		}
		m := reXMLTag.FindStringSubmatchIndex(xml[lt:])
		if m == nil {
			return fmt.Errorf("malformed tag at %d: %q", lt, xml[lt:min(len(xml), lt+60)])
		}
		closing := xml[lt+m[2] : lt+m[3]]
		name := xml[lt+m[4] : lt+m[5]]
		attrs := xml[lt+m[6] : lt+m[7]]
		selfClosing := xml[lt+m[8] : lt+m[9]]
		if badAmp(attrs) {
			return fmt.Errorf("unescaped & in attributes of <%s>", name)
		}
		seen := map[string]bool{}
		for _, a := range reXMLAttr.FindAllStringSubmatch(attrs, -1) {
			if seen[a[1]] {
				return fmt.Errorf("<%s> has %s twice", name, a[1])
			}
			seen[a[1]] = true
		}
		if closing != "" {
			if len(stack) == 0 || stack[len(stack)-1] != name {
				return fmt.Errorf("</%s> closes the wrong element", name)
			}
			stack = stack[:len(stack)-1]
		} else if selfClosing == "" {
			stack = append(stack, name)
		}
		i = lt + m[1]
	}
	if len(stack) > 0 {
		return fmt.Errorf("unclosed <%s>", stack[len(stack)-1])
	}
	return nil
}

func codePointOf(v float64) string {
	return string(rune(int(math.Min(v, 0x10ffff))))
}

// decodeAttr is an attribute value as a browser reads it, with character references turned into their characters.
func decodeAttr(value string) string {
	value = reHexRef.ReplaceAllStringFunc(value, func(m string) string {
		v, _ := strconv.ParseUint(reHexRef.FindStringSubmatch(m)[1], 16, 64)
		return codePointOf(float64(v))
	})
	value = reDecRef.ReplaceAllStringFunc(value, func(m string) string {
		return codePointOf(js.Number(reDecRef.FindStringSubmatch(m)[1]))
	})
	r := strings.NewReplacer("&quot;", `"`, "&apos;", "'", "&lt;", "<", "&gt;", ">")
	return strings.ReplaceAll(r.Replace(value), "&amp;", "&")
}

type xmlElement struct {
	name  string
	attrs [][2]string
}

func xmlElements(xml string) []xmlElement {
	var out []xmlElement
	for _, m := range reAllTags.FindAllStringSubmatch(xml, -1) {
		el := xmlElement{name: m[1]}
		for _, a := range reOneAttr.FindAllStringSubmatch(m[2], -1) {
			v := a[2]
			if strings.Contains(v, "&") {
				v = decodeAttr(v)
			}
			el.attrs = append(el.attrs, [2]string{a[1], v})
		}
		out = append(out, el)
	}
	return out
}

func setOf(list ...string) map[string]bool {
	m := map[string]bool{}
	for _, s := range list {
		m[s] = true
	}
	return m
}

// pele's output is inserted into pages as markup, so it must be inert whatever the diagram says:
// only drawing elements, only presentation attributes, no script-capable URLs, no way to load a stylesheet.
var (
	inertElements = setOf("svg", "g", "rect", "circle", "ellipse", "polygon", "path", "text", "tspan", "a", "title", "desc", "image",
		// The gradients of sankey links, with an idPrefix (pele 1.9.0).
		"defs", "linearGradient", "stop")
	inertAttrs = setOf("xmlns", "class", "viewBox", "width", "height", "style", "font-family", "font-size", "font-weight", "font-style",
		"fill", "fill-opacity", "opacity", "stroke", "stroke-width", "stroke-dasharray", "stroke-linecap", "stroke-linejoin",
		"role", "aria-roledescription", "aria-labelledby", "aria-describedby", "id", "data-id", "data-icon", "data-href",
		"transform", "x", "y", "rx", "ry", "r", "cx", "cy", "d", "points", "text-anchor", "xml:space",
		"href", "target", "rel", "preserveAspectRatio", "pointer-events",
		// <sub>, <sup>, <u> and <ins> in labels, and sankey gradients (pele 1.9.0).
		"baseline-shift", "text-decoration", "gradientUnits", "x1", "x2", "offset", "stop-color")
	styleProps = setOf("fill", "fill-opacity", "stroke", "stroke-width", "stroke-dasharray", "stroke-dashoffset", "stroke-linecap",
		"stroke-linejoin", "stroke-opacity", "opacity", "rx", "ry", "font-size", "font-family", "font-weight", "font-style",
		"text-decoration", "letter-spacing", "word-spacing", "max-width")
	rootStyles   = setOf("height:auto", "color:var(--_fg)", "text-transform:none", "direction:ltr")
	reRootFont   = regexp.MustCompile(`^font:\d+(?:\.\d+)?(?:e[-+]?\d+)?px sans-serif$`)
	numericAttrs = setOf("x", "y", "width", "height", "r", "cx", "cy", "rx", "ry", "x1", "x2", "offset")
	// The only values pele writes for the label attributes of pele 1.9.0.
	fixedValues = map[string]map[string]bool{
		"baseline-shift":  setOf("sub", "super"),
		"text-decoration": setOf("underline"),
		"gradientUnits":   setOf("userSpaceOnUse"),
	}
	// A sankey link points at its gradient with a fragment in this document, then a fallback colour.
	reGradientFill = regexp.MustCompile(`^url\(#([\w-]+)\) `)

	reNumber      = regexp.MustCompile(`^(?:-?\d+(?:\.\d+)?(?:e[-+]?\d+)?|100%)$`)
	reScriptURL   = regexp.MustCompile(`(?i)^[` + js.S + `\x00-\x1f]*(?:javascript|vbscript|data)[` + js.S + `]*:`)
	reAllowedHead = regexp.MustCompile(`(?i)^(?:https?|mailto|tel):`)
	reBadHead     = regexp.MustCompile(`(?i)^(?:[\\/]{2}|[a-z][a-z0-9+.-]*:)`)
	reTarget      = regexp.MustCompile(`^_(?:self|blank|parent|top)$`)
	reUnsafeStyle = regexp.MustCompile(`(?i)url[` + js.S + `]*\(|expression|@import|javascript:|[<>{}\\]`)
	// A backslash is refused too: a CSS escape such as \75 rl( spells url(.
	reUnsafePaint = regexp.MustCompile(`(?i)url[` + js.S + `]*\(|javascript:|\\`)
	reTabsBreaks  = regexp.MustCompile(`[\t\n\r]`)
	reLeadCtrl    = regexp.MustCompile(`^[\x00-\x20]+`)
)

// inertProblem is the first thing about the markup that is not inert, or "" when there is none.
func inertProblem(svg string) string {
	if err := assertWellFormed(svg); err != nil {
		return err.Error()
	}
	// A gradient comes before the links that fill with it.
	gradientIDs := map[string]bool{}
	for _, el := range xmlElements(svg) {
		if !inertElements[el.name] {
			return "element <" + el.name + ">"
		}
		for _, a := range el.attrs {
			name, value := a[0], a[1]
			if !inertAttrs[name] {
				return "attribute " + name + " on <" + el.name + ">"
			}
			if numericAttrs[name] && !reNumber.MatchString(value) {
				return fmt.Sprintf("%s=%q is not a number", name, value)
			}
			switch name {
			case "href":
				if el.name != "a" && el.name != "image" {
					return "href on <" + el.name + ">"
				}
				if reScriptURL.MatchString(value) {
					return fmt.Sprintf("href=%q can run script", value)
				}
				// What a browser makes of the value: tabs and line breaks dropped, then leading spaces and controls.
				seen := reLeadCtrl.ReplaceAllString(reTabsBreaks.ReplaceAllString(value, ""), "")
				if !reAllowedHead.MatchString(seen) && reBadHead.MatchString(seen) {
					return fmt.Sprintf("href=%q has a scheme that is not allowed", value)
				}
			case "target":
				if !reTarget.MatchString(value) {
					return fmt.Sprintf("target=%q", value)
				}
			case "style":
				if reUnsafeStyle.MatchString(value) {
					return fmt.Sprintf("style=%q", value)
				}
				for _, decl := range strings.Split(value, ";") {
					trimmed := js.Trim(decl)
					if trimmed == "" {
						continue
					}
					cut := strings.IndexByte(decl, ':')
					if cut < 0 {
						cut = len(decl) - 1
					}
					prop := js.Trim(decl[:cut])
					root := el.name == "svg" && (rootStyles[trimmed] || reRootFont.MatchString(trimmed))
					if !root && !styleProps[prop] && !strings.HasPrefix(prop, "--_") {
						return "style property " + prop
					}
				}
			case "fill":
				if m := reGradientFill.FindStringSubmatch(value); m != nil {
					if !gradientIDs[m[1]] {
						return fmt.Sprintf("fill=%q points at no gradient of the diagram", value)
					}
					value = value[len(m[0]):]
				}
				if reUnsafePaint.MatchString(value) {
					return fmt.Sprintf("%s=%q", name, value)
				}
			case "stroke", "font-family", "rx", "stop-color":
				if reUnsafePaint.MatchString(value) {
					return fmt.Sprintf("%s=%q", name, value)
				}
			case "baseline-shift", "text-decoration", "gradientUnits":
				if !fixedValues[name][value] {
					return fmt.Sprintf("%s=%q", name, value)
				}
			case "id":
				if el.name == "linearGradient" {
					gradientIDs[value] = true
				}
			}
		}
	}
	return ""
}

// PAYLOADS is text an attacker might put anywhere a diagram accepts a string.
var PAYLOADS = []string{
	"<script>alert(1)</script>",
	`"><script>alert(1)</script>`,
	"<img src=x onerror=alert(1)>",
	"<svg onload=alert(1)>",
	`<a href="javascript:alert(1)">x</a>`,
	"<foreignObject><iframe src=javascript:alert(1)></iframe></foreignObject>",
	"</text></g></svg><script>alert(1)</script>",
	"javascript:alert(1)",
	"JaVaScRiPt:alert(1)",
	" javascript:alert(1)",
	"jav&#x09;ascript:alert(1)",
	"data:text/html,<script>alert(1)</script>",
	"vbscript:msgbox(1)",
	"' onload='alert(1)",
	`" onmouseover="alert(1)`,
	`x" onclick="alert(1)" y="`,
	"#60;script#62;alert(1)#60;/script#62;",
	"#106;avascript:alert(1)",
	"#34; onload=#34;alert(1)",
	"url#40;javascript:alert#40;1#41;#41;",
	"red#59;background:url#40;//evil.example/x#41;",
	"&lt;script&gt;alert(1)&lt;/script&gt;",
	"&#60;script&#62;",
	"url(javascript:alert(1))",
	"expression(alert(1))",
	"red;background:url(//evil.example/x)",
	`red" onload="alert(1)`,
	"]]><script>alert(1)</script>",
	"--><script>alert(1)</script>",
	"\u0000\u0001\u0008\u000b\u001f",
	// pele writes lone surrogates here. Go strings hold UTF-8, so the port takes the bytes
	// of an encoded surrogate, which are not valid UTF-8 and become U+FFFD on the way in.
	"a\ufffeb\uffffc\xed\xa0\x80d\xed\xb0\x80e#65534;f",
	"u\u0001rl(//evil.example/x)",
	"u\ufffeRL(//evil.example/x)",
	"u\xed\xa0\x80rl(//evil.example/x)",
	"java\ufffescript:alert(1)",
	"java\xed\xb0\x80script:alert(1)",
	"file\uffff:///etc/passwd",
	"__proto__",
	"constructor",
	"toString",
	"hasOwnProperty",
	"{{7*7}}",
	"${alert(1)}",
	"%0ajavascript:alert(1)",
	"file:///etc/passwd",
	"//evil.example/share",
	`\\evil.example\share`,
	"#92;#92;nevil.example/share",
	"\t/#92;nevil.example/share",
	"smb://evil.example/share",
	"obsidian://open?vault=x",
	`\"><script>`,
}

var flowchartTemplates = []func(p string) string{
	func(p string) string { return "flowchart TD\n  A[" + p + "] --> B" },
	func(p string) string { return "flowchart TD\n  A[\"" + p + "\"] --> B" },
	func(p string) string { return "flowchart TD\n  A[\"`" + p + "`\"] --> B" },
	func(p string) string { return "flowchart TD\n  A(" + p + ") --> B{" + p + "}" },
	func(p string) string { return "flowchart TD\n  " + p + " --> B" },
	func(p string) string { return "flowchart TD\n  A -->|" + p + "| B" },
	func(p string) string { return "flowchart TD\n  A -- " + p + " --> B" },
	func(p string) string { return "flowchart TD\n  A -- \"" + p + "\" --> B" },
	func(p string) string { return "flowchart TD\n  subgraph " + p + "\n  A\n  end" },
	func(p string) string { return "flowchart TD\n  subgraph s [\"" + p + "\"]\n  A\n  end" },
	func(p string) string { return "flowchart TD\n  A:::" + p + " --> B" },
	func(p string) string { return "flowchart TD\n  A --> B\n  class A " + p },
	func(p string) string { return "flowchart TD\n  A --> B\n  classDef " + p + " fill:red\n  class A " + p },
	func(p string) string { return "flowchart TD\n  A --> B\n  classDef c fill:" + p + "\n  class A c" },
	func(p string) string { return "flowchart TD\n  A --> B\n  style A fill:" + p },
	func(p string) string { return "flowchart TD\n  A --> B\n  style A " + p + ":red" },
	func(p string) string {
		return "flowchart TD\n  A --> B\n  style A color:" + p + ",stroke:" + p + ",font-family:" + p
	},
	func(p string) string { return "flowchart TD\n  A --> B\n  linkStyle 0 stroke:" + p },
	func(p string) string { return "flowchart TD\n  A --> B\n  click A \"" + p + "\"" },
	func(p string) string {
		return "flowchart TD\n  A --> B\n  click A href \"" + p + "\" \"" + p + "\" _blank"
	},
	func(p string) string { return "flowchart TD\n  A --> B\n  click A \"x\" \"" + p + "\"" },
	func(p string) string { return "flowchart TD\n  A --> B\n  click A call " + p + "()" },
	func(p string) string { return "flowchart TD\n  A --> B\n  click A \"https://example.com\" " + p },
	func(p string) string { return "flowchart TD\n  A[\"" + p + "\"] --> B\n  class A internal-link" },
	func(p string) string { return "flowchart TD\n  A[" + p + "]:::internal-link --> B" },
	func(p string) string { return "flowchart TD\n  accTitle: " + p + "\n  accDescr: " + p + "\n  A --> B" },
	func(p string) string { return "flowchart TD\n  accDescr {\n " + p + "\n }\n  A --> B" },
	func(p string) string { return "flowchart TD\n  A@{ label: \"" + p + "\" } --> B" },
	func(p string) string { return "flowchart TD\n  A@{ shape: " + p + " } --> B" },
	func(p string) string {
		return "flowchart TD\n  A@{ icon: \"" + p + "\", form: \"" + p + "\", pos: \"" + p + "\", label: \"x\" } --> B"
	},
	func(p string) string {
		return "flowchart TD\n  A@{ img: \"" + p + "\", label: \"x\", w: \"" + p + "\", h: \"" + p + "\", constraint: \"" + p + "\" } --> B"
	},
	func(p string) string { return "flowchart TD\n  A@{ " + p + ": 1 } --> B" },
	func(p string) string {
		return "flowchart TD\n  A e1@--> B\n  e1@{ animate: \"" + p + "\", curve: \"" + p + "\" }"
	},
	func(p string) string { return "flowchart TD\n  A[fa:fa-" + p + " x] --> B" },
	func(p string) string {
		return "---\ntitle: \"" + p + "\"\nconfig:\n  flowchart:\n    curve: \"" + p + "\"\n---\nflowchart TD\n  A --> B"
	},
	func(p string) string {
		return "%%{init: {\"flowchart\": {\"curve\": \"" + p + "\"}, \"themeVariables\": {\"fontFamily\": \"" + p + "\"}}}%%\nflowchart TD\n  A --> B"
	},
	func(p string) string { return "flowchart " + p + "\n  A --> B" },
	func(p string) string { return "flowchart TD\n  %% " + p + "\n  A --> B" },
}

// checkInert renders src and reports output that is not inert. A diagram that does not
// parse is fine, as long as it fails with a PeleError rather than a panic in the port.
func checkInert(t *testing.T, src string, opt RenderOptions) {
	t.Helper()
	out, err := Render(src, opt)
	if err != nil {
		var pe *Error
		if !errors.As(err, &pe) || strings.HasPrefix(pe.Message, "sketch: internal error") {
			t.Fatalf("%q: %v", src, err)
		}
		return
	}
	if problem := inertProblem(out.Data); problem != "" {
		t.Fatalf("%q: output is not inert: %s", src, problem)
	}
}

func TestFlowchartOutputIsInert(t *testing.T) {
	for _, c := range loadGoldens(t) {
		if done[c.name] {
			checkInert(t, c.source, RenderOptions{})
		}
	}
	for _, template := range flowchartTemplates {
		for _, payload := range PAYLOADS {
			checkInert(t, template(payload), RenderOptions{})
		}
	}
	for _, payload := range PAYLOADS {
		checkInert(t, "flowchart TD\n  accTitle: t\n  A --> B", RenderOptions{IDPrefix: types.Ptr(payload), FontFamily: payload})
	}
}

// labelTags are the inline tags a label styles since pele 1.9.0, with the two it had before.
var labelTags = []string{"b", "i", "sub", "sup", "code", "kbd", "samp", "tt", "u", "ins", "var", "cite", "dfn"}

// labelTagTemplates put a label with inline HTML in each kind of place that lays one out.
var labelTagTemplates = []func(p string) string{
	func(p string) string { return "flowchart TD\n  A[\"" + p + "\"] --> B" },
	func(p string) string { return "flowchart TD\n  A[\"`" + p + "`\"] --> B" },
	func(p string) string { return "flowchart TD\n  A -->|\"" + p + "\"| B" },
	func(p string) string { return "sequenceDiagram\n  Alice->>Bob: " + p + "\n  Note over Alice: " + p },
	func(p string) string { return "stateDiagram-v2\n  s1 : " + p + "\n  note right of s1 : " + p },
	func(p string) string {
		return "classDiagram\n  class A[\"" + p + "\"]\n  A : +" + p + "\n  A --> B : " + p
	},
	func(p string) string { return "kanban\n  todo[" + p + "]\n    t1[" + p + "]" },
	func(p string) string { return "mindmap\n  root((" + p + "))\n    " + p },
	func(p string) string { return "erDiagram\n  A ||--o{ B : \"" + p + "\"" },
	func(p string) string { return "block\n  a[\"" + p + "\"]" },
	func(p string) string {
		return "requirementDiagram\n  requirement r {\n  id: 1\n  text: \"" + p + "\"\n  }"
	},
}

// TestLabelTagsAreInert puts the inline tags of pele 1.9.0 around attack text, with
// attributes, unclosed and nested, in every place that lays out a label. A tag a label
// styles turns into an attribute pele chooses, a tag it drops leaves its text, and any
// other text shaped like a tag stays text: none of it may reach the SVG as markup.
func TestLabelTagsAreInert(t *testing.T) {
	var labels []string
	for _, tag := range labelTags {
		for _, payload := range PAYLOADS {
			labels = append(labels, "<"+tag+">"+payload+"</"+tag+">", "x<"+tag+">"+payload)
		}
		labels = append(labels,
			"<"+tag+` onclick="alert(1)" style="background:url(javascript:alert(1))">x</`+tag+">",
			"<"+tag+` href=javascript:alert(1)>x</`+tag+">",
			"<"+tag+"><script>alert(1)</script></"+tag+">",
			"<"+tag+"><svg onload=alert(1)>x</svg></"+tag+">",
			"<"+tag+"><foo onload=alert(1)>x</foo></"+tag+">",
			"<"+tag+">a<br/>b<"+tag+">c</"+tag+"></"+tag+">",
			"</"+tag+">x<"+tag+"/>",
			"<"+strings.ToUpper(tag)+">x</"+strings.ToUpper(tag)+">",
			"<sup><"+tag+">a<sub>b<"+tag+">c</sup>d</"+tag+">",
		)
	}
	for _, template := range labelTagTemplates {
		for _, label := range labels {
			checkInert(t, template(label), RenderOptions{})
		}
	}
	// Text shaped like a tag that is not HTML is text: it comes out escaped.
	for _, label := range []string{"<svg onload=alert(1)>", "<foo onload=alert(1)>", "List<T>", "<T>"} {
		out, err := Render("flowchart TD\n  A[\""+label+"\"]", RenderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if want := strings.NewReplacer("<", "&lt;", ">", "&gt;").Replace(label); !strings.Contains(out.Data, want) {
			t.Errorf("%q: the label is not kept as escaped text", label)
		}
	}
}

// TestSankeyGradientsAreInert renders sankey links with gradients, which an idPrefix
// turns on, from node colours and names that carry attack text.
func TestSankeyGradientsAreInert(t *testing.T) {
	for _, payload := range PAYLOADS {
		quoted := `"` + strings.ReplaceAll(payload, `"`, `""`) + `"`
		color := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(payload)
		src := "---\nconfig:\n  sankey:\n    nodeColors:\n      a: \"" + color + "\"\n      b: \"" + color + "\"\n---\nsankey\na,b,5\na," + quoted + ",3\n"
		for _, prefix := range []string{"p1", "p_1-x", payload} {
			checkInert(t, src, RenderOptions{IDPrefix: types.Ptr(prefix)})
		}
	}
	out, err := Render("sankey\na,b,5\n", RenderOptions{IDPrefix: types.Ptr("p1")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Data, "<linearGradient") {
		t.Fatal("an idPrefix of word characters gives no gradient")
	}
}

// TestInternalLinkHref: a note name goes into the href as written, so it gets the
// check of a URL, and the check of the name as a browser reads it after Esc().
func TestInternalLinkHref(t *testing.T) {
	about := RenderOptions{LinkSchemes: []string{"about"}}
	for _, head := range []string{"flowchart TD", "agentflow-beta TB", "erDiagram"} {
		out, err := Render(head+"\n  A[\"javascript:alert(1)\"]:::internal-link", about)
		if err != nil || strings.Contains(out.Data, " href=") {
			t.Errorf("%s: an internal link to a script with about allowed: %v\n%s", head, err, out.Data)
		}
	}
	for _, name := range []string{"Note", `Folder\note`, "Note%23heading", "👩‍💻", "Note#heading", "  Note  "} {
		if !util.InternalLink(name, RenderOptions{}) {
			t.Errorf("%q: not linked", name)
		}
	}
	for _, name := range []string{"javascript:alert(1)", "java\ufffescript:alert(1)", `\\nevil.example`, "\t/\\nevil.example", "/\t/evil.example"} {
		if util.InternalLink(name, about) {
			t.Errorf("%q: linked", name)
		}
	}
}

// TestPercentEscapes: SanitizeUrl keeps percent escapes, as pele 1.12.0 does, and an escape
// still cannot hide a scheme or a network path.
func TestPercentEscapes(t *testing.T) {
	for _, u := range []string{"obsidian://open?vault=V&file=A%26B%23C", "https://example.com/?q=a%26b", "mailto:a@example.com?subject=a%20b", "Note%20name", "./a%23b.html"} {
		if got := util.SanitizeUrl(u); got != u {
			t.Errorf("%q: got %q", u, got)
		}
	}
	if got := util.SanitizeUrl("%6A%61%76%61%73%63%72%69%70%74:alert(1)"); got != "about:blank" {
		t.Errorf("an escaped javascript: scheme gives %q", got)
	}
	for _, u := range []string{"fi%6ce:///etc/passwd", "%2F%2Fevil.example/share", "obs%69dian://open"} {
		if got, ok := util.LinkUrl(util.SanitizeUrl(u), RenderOptions{}); ok {
			t.Errorf("%q: linked as %q", u, got)
		}
	}
}

func TestFlowchartLimits(t *testing.T) {
	// One short line asks for 100 x 100 edges; the default limit is 5,000.
	var starts, ends []string
	for i := range 100 {
		starts = append(starts, "a"+strconv.Itoa(i))
		ends = append(ends, "b"+strconv.Itoa(i))
	}
	src := "flowchart TD\n" + strings.Join(starts, " & ") + " --> " + strings.Join(ends, " & ") + "\n"
	_, err := Render(src, RenderOptions{})
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != CodeLimit {
		t.Fatalf("10,000 edges: got %v, want a limit error", err)
	}
	long := "flowchart TD\n  A --> B\n" + strings.Repeat("%% filler\n", 6000)
	if _, err := Render(long, RenderOptions{}); !errors.As(err, &pe) || pe.Code != CodeLimit {
		t.Fatalf("source over 50,000 characters: got %v, want a limit error", err)
	}
	if _, err := Render("flowchart TD\n  A --> B\n", RenderOptions{OutputLimit: 100}); !errors.As(err, &pe) || pe.Code != CodeLimit {
		t.Fatalf("output limit of 100: got %v, want a limit error", err)
	}
	longRunsStayLinear(t)
}

// longRunsStayLinear renders statements of 200,000 characters, past the
// default limit, that a parser reads as one long run of tokens or lines. JavaScript
// ropes make s += token linear in pele. In Go each += copies the string, so a
// run built that way allocates gigabytes. The bound is on bytes allocated, which
// does not depend on the speed of the machine.
func longRunsStayLinear(t *testing.T) {
	const n = 200_000
	const bound = 512 << 20
	for name, src := range map[string]string{
		"quadrant axis text":    "quadrantChart\n x-axis " + strings.Repeat("a1-", n/3),
		"quadrant point style":  "quadrantChart\n a: [0.1, 0.2] color: " + strings.Repeat("a1:", n/3),
		"quadrant classDef id":  "quadrantChart\n classDef " + strings.Repeat("a1", n/2) + " color: #fff",
		"xychart axis text":     "xychart\n x-axis " + strings.Repeat("word ", n/5) + "[a]\n bar [1]",
		"class name":            "classDiagram\nclass " + strings.Repeat("a.", n/2) + "a",
		"class style":           "classDiagram\nclass A\nstyle A fill:" + strings.Repeat("a:", n/2),
		"er subgraph title":     "erDiagram\nsubgraph s [" + strings.Repeat("a ", n/2) + "]\nA\nend",
		"er class names":        "erDiagram\nA\nclass A " + strings.Repeat("b,", n/2) + "b",
		"state descriptions":    "stateDiagram-v2\nA\n" + strings.Repeat("A : x\n", n/6),
		"git cherry-pick chain": cherryPickChain(2 * n),
		"requirement style":     "requirementDiagram\nelement A {\n}\nstyle A fill:" + strings.Repeat("a:", n/2),
	} {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, _ = Render(src, RenderOptions{Limit: 1 << 20, OutputLimit: 1 << 30})
		runtime.ReadMemStats(&after)
		if got := after.TotalAlloc - before.TotalAlloc; got > bound {
			t.Errorf("%s of %d characters: %d MB allocated, want at most %d MB", name, len(src), got>>20, bound>>20)
		}
	}
}

// cherryPickChain is a gitGraph of about n characters in which each commit
// cherry-picks the one before it, between two branches in turn. pele names each
// pick "cherry-picked <message of the source> into <branch>", so the names nest.
// The chain is twice as long as the other cases: at 200,000 characters the
// nested names cost about 250 MB, under the bound.
func cherryPickChain(n int) string {
	var b strings.Builder
	b.WriteString("gitGraph\n commit id: \"c\"\n branch a\n commit\n branch b\n commit\n")
	prev, seq := "c", 3
	for i := 0; b.Len() < n; i++ {
		b.WriteString(" checkout " + string(rune('a'+i%2)) + "\n cherry-pick id: \"" + prev + "\"\n")
		// The id gitBuilder.nextId gives the pick.
		h := strconv.FormatUint(uint64(uint32(seq+1)*0x9e3779b1), 16)
		prev = strconv.Itoa(seq) + "-" + (strings.Repeat("0", max(0, 8-len(h))) + h)[:7]
		seq++
	}
	return b.String()
}
