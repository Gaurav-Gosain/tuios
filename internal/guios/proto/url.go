package proto

import (
	"net"
	"net/url"
	"strings"
)

// BlankURL is the page of a browser pane that has been told none.
const BlankURL = "about:blank"

// BrowserURL makes what the user typed in a browser's address bar, or gave
// kero split --url, a URL to load: one with a scheme stays; a host, as
// "localhost:3000" or "example.com/docs", gets http for a local one and
// https for another; anything else is searched for. "" is BlankURL.
func BrowserURL(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return BlankURL
	}
	if u, ok := address(text); ok {
		return u
	}
	return "https://www.google.com/search?q=" + url.QueryEscape(text)
}

// IsAddress reports whether text is a URL or a host, as BrowserURL goes to,
// rather than words it would search for.
func IsAddress(text string) bool {
	_, ok := address(strings.TrimSpace(text))
	return ok
}

func address(text string) (string, bool) {
	if hasScheme(text) {
		return text, true
	}
	if text == "" || strings.ContainsAny(text, " \t") {
		return "", false
	}
	host := text
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	switch {
	case localHost(host):
		return "http://" + text, true
	case strings.Contains(host, ".") && !strings.HasPrefix(host, ".") && !strings.HasSuffix(host, "."):
		return "https://" + text, true
	}
	return "", false
}

// hasScheme tells a URL with a scheme, as "https://", "file:///" or
// "about:blank", from a host and port, as "localhost:3000".
func hasScheme(text string) bool {
	u, err := url.Parse(text)
	if err != nil || u.Scheme == "" {
		return false
	}
	if strings.HasPrefix(text[len(u.Scheme)+1:], "//") {
		return true
	}
	// "about:blank", "data:…", "mailto:…": not a port after a host.
	switch strings.ToLower(u.Scheme) {
	case "about", "data", "mailto", "javascript", "blob", "view-source":
		return true
	}
	return false
}

func localHost(host string) bool {
	host = strings.Trim(host, "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".test") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified())
}

// URLHost is the host of a URL to show for its page, as "localhost:3000",
// or the URL itself when it has none.
func URLHost(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return strings.TrimPrefix(u.Host, "www.")
	}
	return raw
}
