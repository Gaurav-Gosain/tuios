// Ported from pele src/util/url.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// `new URL()` is approximated for http and https: the scheme and host are
// lowercased and an empty path becomes "/". decodeURIComponent is ported as is.

package util

import (
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/lazyre"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

const BLANK = "about:blank"

var (
	reInvalidProtocol = lazyre.New(`(?im)^([^\w]*)(javascript|data|vbscript)`)
	reHTMLEntities    = lazyre.New(`&#(\w+)(;)?`)
	reHTMLCtrlEntity  = lazyre.New(`(?i)&(newline|tab);`)
	// pele also strips lone surrogates, which valid UTF-8 cannot hold.
	reCtrl        = lazyre.New(`[\x{0}-\x{1F}\x{7F}-\x{9F}\x{2000}-\x{200D}\x{FEFF}\x{FFFE}\x{FFFF}]`)
	reScheme      = lazyre.New(`(?im)^` + js.Dot + `+(:|&colon;)`)
	reWSEscape    = lazyre.New(`(\\|%5[cC])((%(6[eE]|72|74))|[nrt])`)
	reSchemeName  = lazyre.New(`(?i)^([a-z][a-z0-9+.-]*):`)
	reNetworkPath = lazyre.New(`^[\\/]{2}`)
)

// decodeURIComponent, or false where JavaScript throws a URIError.
func decodeURIComponent(s string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, true
	}
	var out []byte
	for i := 0; i < len(s); {
		if s[i] != '%' {
			out = append(out, s[i])
			i++
			continue
		}
		b, ok := hexByte(s, i)
		if !ok {
			return "", false
		}
		i += 3
		if b < 0x80 {
			out = append(out, b)
			continue
		}
		n := 0
		switch {
		case b&0xe0 == 0xc0:
			n = 2
		case b&0xf0 == 0xe0:
			n = 3
		case b&0xf8 == 0xf0:
			n = 4
		default:
			return "", false
		}
		seq := []byte{b}
		for k := 1; k < n; k++ {
			if i >= len(s) || s[i] != '%' {
				return "", false
			}
			c, ok := hexByte(s, i)
			if !ok || c&0xc0 != 0x80 {
				return "", false
			}
			seq = append(seq, c)
			i += 3
		}
		r, size := utf8.DecodeRune(seq)
		if r == utf8.RuneError || size != n {
			return "", false
		}
		out = append(out, seq...)
	}
	return string(out), true
}

func hexByte(s string, i int) (byte, bool) {
	if i+3 > len(s) {
		return 0, false
	}
	v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
	if err != nil {
		return 0, false
	}
	return byte(v), true
}

func decode(uri string) string {
	if v, ok := decodeURIComponent(uri); ok {
		return v
	}
	return uri
}

// unwrap removes the control characters, entity codes and escapes that could hide a scheme, and
// with `percent`, percent escapes too. ok is false for a URL wrapped too many times to be a real one.
func unwrap(u string, percent bool) (out string, ok bool) {
	out = js.Trim(u)
	if percent {
		out = decode(out)
	}
	rounds := 0
	for {
		rounds++
		if rounds > 32 {
			return "", false
		}
		out = reCtrl.ReplaceAllString(out, "")
		out = reHTMLEntities.ReplaceAllStringFunc(out, func(m string) string {
			sub := reHTMLEntities.FindStringSubmatch(m)
			// String.fromCharCode(dec): the number mod 2^16, or 0 for one that is not a number.
			n := js.Number(sub[1])
			code := 0
			if !math.IsNaN(n) && !math.IsInf(n, 0) {
				// ToUint16: the truncated number mod 2^16, done in float64 so a
				// number past the int64 range does not hit an
				// implementation-defined conversion.
				m := math.Mod(math.Trunc(n), 65536)
				if m < 0 {
					m += 65536
				}
				code = int(m)
			}
			return string(rune(code))
		})
		out = reHTMLCtrlEntity.ReplaceAllString(out, "")
		out = reCtrl.ReplaceAllString(out, "")
		out = js.Trim(reWSEscape.ReplaceAllString(out, ""))
		if percent {
			out = decode(out)
		}
		more := reCtrl.MatchString(out) || reHTMLEntities.MatchString(out) ||
			reHTMLCtrlEntity.MatchString(out) || reWSEscape.MatchString(out)
		if !more {
			return out, true
		}
	}
}

// SanitizeUrl refuses what @braintree/sanitize-url refuses, the check Mermaid applies to click
// targets. Unlike it, it keeps percent escapes, so `file=A%26B` still names the note `A&B`.
func SanitizeUrl(u string) string {
	if u == "" {
		return BLANK
	}
	decoded, ok := unwrap(u, true)
	kept, ok2 := unwrap(u, false)
	if !ok || !ok2 || decoded == "" || kept == "" {
		return BLANK
	}
	if decoded[0] == '.' || decoded[0] == '/' {
		return kept
	}

	scheme := reScheme.FindString(js.TrimStart(decoded))
	if scheme == "" {
		return kept
	}

	protocol := js.Trim(strings.ToLower(scheme))
	if reInvalidProtocol.MatchString(protocol) {
		return BLANK
	}

	normalized := strings.ReplaceAll(js.TrimStart(kept), "\\", "/")
	if protocol == "mailto:" || strings.Contains(protocol, "://") {
		return normalized
	}

	if protocol == "http:" || protocol == "https:" {
		parsed, err := url.Parse(normalized)
		if err != nil || parsed.Host == "" {
			return BLANK
		}
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		if parsed.Path == "" && parsed.Opaque == "" {
			parsed.Path = "/"
		}
		return parsed.String()
	}
	return normalized
}

// SafeUrl is a URL as written in a diagram: entity codes are read first, so they cannot hide a scheme.
func SafeUrl(u string) string {
	return SanitizeUrl(text.DecodeEntities(u))
}

var (
	linkSchemes  = []string{"http", "https", "mailto", "tel"}
	imageSchemes = []string{"http", "https"}
)

// Stricter than Mermaid, which lets any scheme but three through: a diagram in a shared note
// could otherwise open a local file, a network share, or another application.
func allowed(u string, schemes []string) bool {
	if reNetworkPath.MatchString(u) {
		return false
	}
	scheme := reSchemeName.FindStringSubmatch(u)
	return scheme == nil || slices.Contains(schemes, strings.ToLower(scheme[1]))
}

// allow checks the decoded URL too, for an app that decodes the href before opening it.
func allow(u string, schemes []string) (string, bool) {
	decoded, ok := unwrap(u, true)
	if ok && allowed(u, schemes) && allowed(decoded, schemes) {
		return u, true
	}
	return "", false
}

// LinkUrl and ImageUrl both take a URL that SanitizeUrl has passed, and keep it only when it is relative or its
// scheme is one the host allows. One that is refused is left out of the drawing: written as
// `about:blank`, a link would still take the page it is in somewhere when clicked.
func LinkUrl(u string, options types.RenderOptions) (string, bool) {
	if options.Links != nil && !*options.Links {
		return "", false
	}
	schemes := options.LinkSchemes
	if schemes == nil {
		schemes = linkSchemes
	}
	return allow(u, schemes)
}

func ImageUrl(u string, options types.RenderOptions) (string, bool) {
	if options.Images != nil && !*options.Images {
		return "", false
	}
	schemes := options.ImageSchemes
	if schemes == nil {
		schemes = imageSchemes
	}
	return allow(u, schemes)
}

// InternalLink reports whether a note name may be written as an href. The href keeps
// the original name, so sanitizing alone is not enough.
func InternalLink(name string, options types.RenderOptions) bool {
	u := SanitizeUrl(name)
	if u == BLANK {
		return false
	}
	if _, ok := LinkUrl(u, options); !ok {
		return false
	}
	// Match character removal by Esc() and the browser: Esc() drops what XML does not
	// allow, a browser drops tabs and line breaks and then leading C0 controls and spaces.
	seen := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || r <= 0x08 || r == 0x0b || r == 0x0c ||
			(r >= 0x0e && r <= 0x1f) || r == 0x7f || r == 0xfffe || r == 0xffff {
			return -1
		}
		return r
	}, name)
	seen = strings.TrimLeft(seen, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f ")
	_, ok := LinkUrl(seen, options)
	return ok
}

func Linked(body, u, id string, links *[]types.LinkInfo, options types.RenderOptions, attrs string) string {
	href, ok := LinkUrl(u, options)
	if !ok {
		return body
	}
	*links = append(*links, types.LinkInfo{ID: id, Href: href, Internal: false})
	return `<a href="` + svg.Esc(href) + `"` + attrs + RelAttr(options) + `>` + body + `</a>`
}

func RelAttr(options types.RenderOptions) string {
	rel := types.Or(options.LinkRel, "noopener")
	if rel == "" {
		return ""
	}
	return ` rel="` + svg.Esc(rel) + `"`
}
