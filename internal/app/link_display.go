package app

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// The emulator keeps a link's address percent-encoded outside printable ASCII
// (vt.LinkURI), which is what the host terminal and the opener get. A label
// that showed that form would name https://example.com/%E6%96%87%E6%A1%A3
// where the program wrote https://example.com/文档. linkDisplayAddress is the
// form the hover label shows, and only the label.
//
// The two parts are treated differently on purpose. The host is what a user
// reads to decide whether to trust a link, and a non-ASCII host can imitate
// another one with a look-alike letter, so it is shown in its punycode form
// (xn--...), whether it arrived encoded or raw. The path is decoded, but only
// into printable characters past ASCII: an escaped "/", "?" or "#" would change
// what the address appears to be, and an escaped control or bidi character
// could reorder or hide the text around it, so those stay escaped.

// linkDisplayAddress returns raw as the hover label shows it.
func linkDisplayAddress(raw string) string {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok || scheme == "" {
		return raw
	}
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	authority, tail := rest[:end], rest[end:]
	pathEnd := strings.IndexAny(tail, "?#")
	if pathEnd < 0 {
		pathEnd = len(tail)
	}
	return scheme + "://" + displayAuthority(authority) + decodeForDisplay(tail[:pathEnd]) + tail[pathEnd:]
}

// displayAuthority returns an authority with its host in punycode. The user
// info and the port, when present, are left as they are.
func displayAuthority(a string) string {
	userinfo := ""
	if i := strings.LastIndexByte(a, '@'); i >= 0 {
		userinfo, a = a[:i+1], a[i+1:]
	}
	host, port := a, ""
	if !strings.HasPrefix(host, "[") {
		if i := strings.LastIndexByte(host, ':'); i >= 0 {
			host, port = host[:i], host[i:]
		}
	}
	host = percentDecode(host)
	if !utf8.ValidString(host) {
		return userinfo + a
	}
	labels := strings.Split(host, ".")
	for i, l := range labels {
		if asciiOnly(l) {
			continue
		}
		p, ok := punycodeEncode(strings.ToLower(l))
		if !ok {
			return userinfo + a
		}
		labels[i] = "xn--" + p
	}
	return userinfo + strings.Join(labels, ".") + port
}

// decodeForDisplay decodes the percent escapes in s that spell a printable
// character past ASCII, and keeps every other escape as it is.
func decodeForDisplay(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		// The run of escapes from i, decoded.
		j, raw := i, []byte(nil)
		for j+2 < len(s) && s[j] == '%' && isHex(s[j+1]) && isHex(s[j+2]) {
			raw = append(raw, unhex(s[j+1])<<4|unhex(s[j+2]))
			j += 3
		}
		if len(raw) == 0 {
			b.WriteByte('%')
			i++
			continue
		}
		// Each decoded rune is shown when it is printable and past ASCII, and
		// its escapes are kept otherwise.
		at := i
		for len(raw) > 0 {
			r, size := utf8.DecodeRune(raw)
			if r >= utf8.RuneSelf && r != utf8.RuneError && unicode.IsPrint(r) && !unicode.Is(unicode.Cf, r) {
				b.WriteRune(r)
			} else {
				b.WriteString(s[at : at+3*size])
			}
			at += 3 * size
			raw = raw[size:]
		}
		i = j
	}
	return b.String()
}

// percentDecode decodes every well-formed escape in s.
func percentDecode(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b = append(b, unhex(s[i+1])<<4|unhex(s[i+2]))
			i += 2
			continue
		}
		b = append(b, s[i])
	}
	return string(b)
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}

func asciiOnly(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// punycodeEncode is RFC 3492 section 6.3 for one label. It reports false when
// the label overflows, which no real label does.
func punycodeEncode(label string) (string, bool) {
	const (
		base        = 36
		tmin        = 1
		tmax        = 26
		skew        = 38
		damp        = 700
		initialBias = 72
		initialN    = 128
	)
	adapt := func(delta, numPoints int, first bool) int {
		if first {
			delta /= damp
		} else {
			delta /= 2
		}
		delta += delta / numPoints
		k := 0
		for delta > ((base-tmin)*tmax)/2 {
			delta /= base - tmin
			k += base
		}
		return k + (base-tmin+1)*delta/(delta+skew)
	}
	digit := func(d int) byte {
		if d < 26 {
			return byte('a' + d)
		}
		return byte('0' + d - 26)
	}
	runes := []rune(label)
	var out []byte
	for _, r := range runes {
		if r < 0x80 {
			out = append(out, byte(r))
		}
	}
	b := len(out)
	h := b
	if b > 0 {
		out = append(out, '-')
	}
	n, delta, bias := initialN, 0, initialBias
	const maxInt = int(^uint32(0) >> 1)
	for h < len(runes) {
		m := maxInt
		for _, r := range runes {
			if int(r) >= n && int(r) < m {
				m = int(r)
			}
		}
		if (m - n) > (maxInt-delta)/(h+1) {
			return "", false
		}
		delta += (m - n) * (h + 1)
		n = m
		for _, r := range runes {
			if int(r) < n {
				delta++
				if delta > maxInt {
					return "", false
				}
			}
			if int(r) != n {
				continue
			}
			q := delta
			for k := base; ; k += base {
				t := k - bias
				if t < tmin {
					t = tmin
				} else if t > tmax {
					t = tmax
				}
				if q < t {
					break
				}
				out = append(out, digit(t+(q-t)%(base-t)))
				q = (q - t) / (base - t)
			}
			out = append(out, digit(q))
			bias = adapt(delta, h+1, h == b)
			delta = 0
			h++
		}
		delta++
		n++
	}
	return string(out), true
}
