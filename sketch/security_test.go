package sketch

// Ported from pele tests/support/inert.ts, tests/support/xml.ts and the flowchart
// part of tests/render/security.test.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
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
	inertElements = setOf("svg", "g", "rect", "circle", "ellipse", "polygon", "path", "text", "tspan", "a", "title", "desc", "image")
	inertAttrs    = setOf("xmlns", "class", "viewBox", "width", "height", "style", "font-family", "font-size", "font-weight", "font-style",
		"fill", "fill-opacity", "opacity", "stroke", "stroke-width", "stroke-dasharray", "stroke-linecap", "stroke-linejoin",
		"role", "aria-roledescription", "aria-labelledby", "aria-describedby", "id", "data-id", "data-icon", "data-href",
		"transform", "x", "y", "rx", "ry", "r", "cx", "cy", "d", "points", "text-anchor", "xml:space",
		"href", "target", "rel", "preserveAspectRatio")
	styleProps = setOf("fill", "fill-opacity", "stroke", "stroke-width", "stroke-dasharray", "stroke-dashoffset", "stroke-linecap",
		"stroke-linejoin", "stroke-opacity", "opacity", "rx", "ry", "font-size", "font-family", "font-weight", "font-style",
		"text-decoration", "letter-spacing", "word-spacing", "max-width")
	rootStyles   = setOf("height:auto", "color:var(--_fg)", "text-transform:none", "direction:ltr")
	reRootFont   = regexp.MustCompile(`^font:\d+(?:\.\d+)?(?:e[-+]?\d+)?px sans-serif$`)
	numericAttrs = setOf("x", "y", "width", "height", "r", "cx", "cy", "rx", "ry")

	reNumber      = regexp.MustCompile(`^(?:-?\d+(?:\.\d+)?(?:e[-+]?\d+)?|100%)$`)
	reScriptURL   = regexp.MustCompile(`(?i)^[` + js.S + `\x00-\x1f]*(?:javascript|vbscript|data)[` + js.S + `]*:`)
	reAllowedHead = regexp.MustCompile(`(?i)^(?:https?|mailto|tel):`)
	reBadHead     = regexp.MustCompile(`(?i)^(?:[\\/]{2}|[a-z][a-z0-9+.-]*:)`)
	reTarget      = regexp.MustCompile(`^_(?:self|blank|parent|top)$`)
	reUnsafeStyle = regexp.MustCompile(`(?i)url[` + js.S + `]*\(|expression|@import|javascript:|[<>{}\\]`)
	reUnsafePaint = regexp.MustCompile(`(?i)url[` + js.S + `]*\(|javascript:`)
	reTabsBreaks  = regexp.MustCompile(`[\t\n\r]`)
	reLeadCtrl    = regexp.MustCompile(`^[\x00-\x20]+`)
)

// inertProblem is the first thing about the markup that is not inert, or "" when there is none.
func inertProblem(svg string) string {
	if err := assertWellFormed(svg); err != nil {
		return err.Error()
	}
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
			case "fill", "stroke", "font-family", "rx":
				if reUnsafePaint.MatchString(value) {
					return fmt.Sprintf("%s=%q", name, value)
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
}
