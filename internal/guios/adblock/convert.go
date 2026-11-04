// Package adblock blocks ads and trackers in browser panes: it downloads
// EasyList and EasyPrivacy, and converts their Adblock Plus filters into
// WebKit's content blocker rules, which mygo.CompileContentRules compiles.
package adblock

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
)

// MaxRules is the most rules a list Lists makes holds: WebKit takes
// 50,000 a list in its older versions, 150,000 in later ones.
const MaxRules = 50000

// Rule is a content blocker rule, as WebKit takes it.
type Rule struct {
	Trigger Trigger `json:"trigger"`
	Action  Action  `json:"action"`
}

// Trigger chooses the loads, and pages, a rule applies to.
type Trigger struct {
	URLFilter     string   `json:"url-filter"`
	CaseSensitive bool     `json:"url-filter-is-case-sensitive,omitempty"`
	IfDomain      []string `json:"if-domain,omitempty"`
	UnlessDomain  []string `json:"unless-domain,omitempty"`
	IfTopURL      []string `json:"if-top-url,omitempty"`
	ResourceType  []string `json:"resource-type,omitempty"`
	LoadType      []string `json:"load-type,omitempty"`
	LoadContext   []string `json:"load-context,omitempty"`
}

// Action is what a rule does.
type Action struct {
	Type     string `json:"type"`
	Selector string `json:"selector,omitempty"`
}

// Rules are filters converted: what blocks, what hides, what undoes the
// two, and what blocks despite exceptions ($important), in the order
// WebKit must see them, as an exception undoes only the rules before it.
type Rules struct {
	Block, Hide, Except, Important []Rule
}

// Len returns how many rules there are.
func (r *Rules) Len() int { return len(r.Block) + len(r.Hide) + len(r.Except) + len(r.Important) }

// Lists splits the rules into lists of at most MaxRules rules, in
// WebKit's JSON: each list has some of the rules that block and hide,
// and all of the exceptions and important ones, since a list's exceptions
// undo only its own rules. A rule that two filter lists make is there
// once. No rules make no list, which WebKit would not take.
func (r *Rules) Lists() []string {
	var lists []string
	seen := map[string]bool{}
	unique := func(rules []Rule) []Rule {
		var out []Rule
		for _, rule := range rules {
			key, _ := json.Marshal(rule)
			if !seen[string(key)] {
				seen[string(key)] = true
				out = append(out, rule)
			}
		}
		return out
	}
	tail := unique(slices.Concat(r.Except, r.Important))
	room := max(MaxRules-len(tail), 1)
	head := unique(slices.Concat(r.Block, r.Hide))
	for len(head) > 0 || lists == nil && len(tail) > 0 {
		n := min(room, len(head))
		data, _ := json.Marshal(slices.Concat(head[:n], tail))
		lists = append(lists, string(data))
		head = head[n:]
	}
	return lists
}

// typeNames maps Adblock Plus's types to WebKit's resource types. Others
// than these make a filter that WebKit cannot apply as Adblock Plus does.
var typeNames = map[string]string{
	"script": "script", "image": "image", "stylesheet": "style-sheet", "css": "style-sheet",
	"font": "font", "media": "media", "xmlhttprequest": "raw", "xhr": "raw",
	"subdocument": "document", "frame": "document", "websocket": "raw", "ping": "ping",
	"popup": "popup", "object": "raw", "object-subrequest": "raw", "other": "raw",
	"document": "document", "doc": "document",
}

// subresources are the types of a filter that names none: Adblock Plus
// applies it to neither pages nor popups.
var subresources = []string{"image", "style-sheet", "script", "font", "raw", "svg-document", "media", "ping"}

// Convert converts filters in Adblock Plus syntax, one a line, adding the
// rules to r. It skips what WebKit cannot do: other filters' redirects,
// rewrites, CSP and parameter removal, scriptlets, procedural cosmetic
// filters and regular expressions. It returns how many filters it
// skipped.
func (r *Rules) Convert(filters string) (skipped int) {
	c := converter{Rules: r}
	for line := range strings.Lines(filters) {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '!' || line[0] == '[' {
			continue
		}
		cosmetic, took := c.cosmetic(line)
		if !cosmetic {
			took = c.network(line)
		}
		if !took {
			skipped++
		}
	}
	c.finish()
	return skipped
}

type converter struct {
	*Rules
	// hides are the element hiding filters by selector, in order, which
	// their exceptions (#@#) change before they become rules.
	hides     map[string]*hiding
	selectors []string
}

// hiding is what hides a selector: on every page (generic) but on the
// domains of unless, and on the domains of on; on those excepted (#@#)
// it does not, and exceptAll undoes it everywhere.
type hiding struct {
	generic   bool
	unless    []string
	on        [][]string // a filter's domains each
	excepted  []string
	exceptAll bool
}

// cosmeticMarkers start what follows a cosmetic filter's domains: those
// WebKit can do (## and #@#) and those it cannot.
var cosmeticMarkers = []string{"##", "#@#", "#?#", "#@?#", "#$#", "#@$#", "#%#", "#@%#", "$$", "$@$"}

// cosmetic converts a cosmetic filter, and reports whether the line is
// one, and whether it took it: an element hiding filter WebKit can apply.
func (c *converter) cosmetic(line string) (cosmetic, took bool) {
	at, marker := -1, ""
	for _, m := range cosmeticMarkers {
		if i := strings.Index(line, m); i >= 0 && (at < 0 || i < at) {
			at, marker = i, m
		}
	}
	if at < 0 || !cosmeticDomains(line[:at]) {
		return false, false
	}
	selector := strings.TrimSpace(line[at+len(marker):])
	if marker != "##" && marker != "#@#" || !hidingSelector(selector) {
		return true, false
	}
	ifDomains, unless, ok := domainList(line[:at], ",")
	if !ok {
		return true, false
	}
	if c.hides == nil {
		c.hides = map[string]*hiding{}
	}
	h := c.hides[selector]
	if h == nil {
		h = &hiding{}
		c.hides[selector] = h
		c.selectors = append(c.selectors, selector)
	}
	switch {
	case marker == "#@#" && line[:at] == "":
		h.exceptAll = true
	case marker == "#@#":
		h.excepted = append(h.excepted, ifDomains...)
	case len(ifDomains) > 0:
		// A domain of the page's that a filter excludes is mostly one of
		// the subdomains it names: WebKit takes either the domains or the
		// exclusions, so the domains win.
		h.on = append(h.on, ifDomains)
	case len(unless) > 0:
		h.unless = append(h.unless, unless...)
		h.generic = true
	case line[:at] == "":
		h.generic = true
	default:
		return true, false // only domains WebKit cannot name
	}
	return true, true
}

// cosmeticDomains reports whether s may be a cosmetic filter's domains.
func cosmeticDomains(s string) bool {
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == ',' || c == '~' || c == '*' || c == '_' || c >= 0x80) {
			return false
		}
	}
	return true
}

// procedural are pseudo-classes of extended CSS, which only the
// blockers' own scripts take, and which make WebKit skip a rule.
var procedural = []string{
	":-abp-", ":has-text(", ":contains(", ":xpath(", ":matches-css", ":upward(", ":remove(",
	":style(", ":min-text-length(", ":watch-attr(", ":matches-path(", ":matches-attr(",
	":matches-prop(", ":others(", ":if(", ":if-not(", ":nth-ancestor(", ":matches-media(",
	":-ext-", ":remove-attr(", ":remove-class(",
}

// hidingSelector reports whether WebKit can hide what selector selects:
// CSS, not extended CSS, a style to apply or a scriptlet.
func hidingSelector(selector string) bool {
	if selector == "" || strings.HasPrefix(selector, "+js(") || strings.HasPrefix(selector, "^") ||
		strings.ContainsAny(selector, "{}") {
		return false
	}
	for _, c := range []byte(selector) {
		if c >= 0x80 || c < 0x20 {
			return false
		}
	}
	for _, p := range procedural {
		if strings.Contains(selector, p) {
			return false
		}
	}
	return true
}

// domainList parses the domains of a filter, separated by sep, into those
// it applies on and those it does not (~), for WebKit's if-domain and
// unless-domain: with their subdomains ("*"), lowercase and ASCII. ok is
// false when it names domains WebKit cannot, as entities ("example.*"),
// and none it can.
func domainList(s, sep string) (ifDomains, unless []string, ok bool) {
	if s == "" {
		return nil, nil, true
	}
	dropped := false
	for d := range strings.SplitSeq(s, sep) {
		not := strings.HasPrefix(d, "~")
		d = strings.ToLower(strings.TrimPrefix(d, "~"))
		if !validDomain(d) {
			dropped = true
			continue
		}
		if not {
			unless = append(unless, "*"+d)
		} else {
			ifDomains = append(ifDomains, "*"+d)
		}
	}
	return ifDomains, unless, len(ifDomains)+len(unless) > 0 || !dropped
}

func validDomain(d string) bool {
	if d == "" || d[0] == '.' || d[len(d)-1] == '.' {
		return false
	}
	for _, c := range []byte(d) {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// selectorsPerRule is how many selectors a hiding rule holds, as a list
// of selectors. WebKit skips a rule one of whose selectors it cannot
// parse, so a selector with a pseudo-class, the likeliest to be one, has
// a rule of its own.
const selectorsPerRule = 100

// pseudo reports whether selector has a pseudo-class or element: a ":"
// outside of quotes and brackets.
func pseudo(selector string) bool {
	var quote byte
	depth := 0
	for i := 0; i < len(selector); i++ {
		switch c := selector[i]; {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '[':
			depth++
		case c == ']':
			depth--
		case c == ':' && depth == 0:
			return true
		}
	}
	return false
}

// finish turns the element hiding filters into rules, grouping the
// selectors of the same domains.
func (c *converter) finish() {
	type group struct {
		trigger   Trigger
		selectors []string
	}
	var order []string
	groups := map[string]*group{}
	add := func(t Trigger, selector string) {
		key, _ := json.Marshal(t)
		k := string(key)
		if pseudo(selector) {
			k += "\x00" + selector // alone
		}
		g := groups[k]
		if g == nil || len(g.selectors) == selectorsPerRule {
			if g != nil {
				k += "\x00" + selector
			}
			g = &group{trigger: t}
			groups[k] = g
			order = append(order, k)
		}
		g.selectors = append(g.selectors, selector)
	}
	for _, sel := range c.selectors {
		h := c.hides[sel]
		if h.exceptAll {
			continue
		}
		if h.generic {
			unless := slices.Concat(h.unless, h.excepted)
			sort.Strings(unless)
			add(Trigger{URLFilter: ".*", UnlessDomain: slices.Compact(unless)}, sel)
		}
		for _, domains := range h.on {
			domains = slices.DeleteFunc(slices.Clone(domains), func(d string) bool { return slices.Contains(h.excepted, d) })
			if len(domains) == 0 {
				continue
			}
			sort.Strings(domains)
			add(Trigger{URLFilter: ".*", IfDomain: slices.Compact(domains)}, sel)
		}
	}
	for _, k := range order {
		g := groups[k]
		c.Hide = append(c.Hide, Rule{Trigger: g.trigger, Action: Action{Type: "css-display-none", Selector: strings.Join(g.selectors, ", ")}})
	}
	c.hides, c.selectors = nil, nil
}

// network converts a filter of what loads, and reports whether it took
// it.
func (c *converter) network(line string) bool {
	except := strings.HasPrefix(line, "@@")
	pattern := strings.TrimPrefix(line, "@@")
	var options string
	if i := strings.LastIndexByte(pattern, '$'); i >= 0 && filterOptions(pattern[i+1:]) {
		pattern, options = pattern[:i], pattern[i+1:]
	}
	if len(pattern) > 1 && pattern[0] == '/' && pattern[len(pattern)-1] == '/' {
		return false // a regular expression, which WebKit's may not take
	}
	filter, ok := urlFilter(pattern)
	if !ok {
		return false
	}
	t := Trigger{URLFilter: filter}
	var types, notTypes []string
	important, page, hiding := false, false, false
	for opt := range strings.SplitSeq(options, ",") {
		if opt == "" {
			continue
		}
		name, value, _ := strings.Cut(opt, "=")
		not := strings.HasPrefix(name, "~")
		name = strings.TrimPrefix(name, "~")
		switch {
		case name == "third-party" || name == "3p" || name == "first-party" || name == "1p":
			third := name == "third-party" || name == "3p"
			if third == not {
				t.LoadType = []string{"first-party"}
			} else {
				t.LoadType = []string{"third-party"}
			}
		case name == "domain" || name == "from":
			var ok bool
			if t.IfDomain, t.UnlessDomain, ok = domainList(value, "|"); !ok {
				return false
			}
			if len(t.IfDomain) > 0 {
				t.UnlessDomain = nil
			}
		case name == "match-case":
			t.CaseSensitive = true
		case name == "important":
			important = true
		case except && (name == "elemhide" || name == "ehide" || name == "generichide" || name == "ghide" || name == "specifichide" || name == "shide"):
			hiding = true
		case (name == "document" || name == "doc") && !not:
			// ~document leaves pages out, a type to subtract below.
			if except {
				page = true
			} else {
				types = append(types, "document")
			}
		case name == "all":
			types = append(types, "document", "popup")
			types = append(types, subresources...)
		case typeNames[name] != "":
			if not {
				notTypes = append(notTypes, typeNames[name])
			} else {
				types = append(types, typeNames[name])
			}
			if name == "subdocument" || name == "frame" {
				t.LoadContext = []string{"child-frame"}
			}
		default:
			return false // redirect, csp, removeparam and the like
		}
	}
	if len(notTypes) > 0 {
		if len(types) == 0 {
			types = slices.Clone(subresources)
		}
		types = slices.DeleteFunc(types, func(s string) bool { return slices.Contains(notTypes, s) })
		if len(types) == 0 {
			return false
		}
	}
	if len(types) > 0 {
		sort.Strings(types)
		t.ResourceType = slices.Compact(types)
		if !slices.Equal(t.ResourceType, []string{"document"}) {
			t.LoadContext = nil
		}
	} else if !strings.HasPrefix(pattern, "||") && !page && !hiding {
		// Adblock Plus applies a filter of no type to what pages load,
		// not to pages. One of a domain applies to every load, its frames
		// and its pages too, as uBlock Origin's do: a rule a filter, as
		// WebKit tells frames from pages only by a rule of their own.
		t.ResourceType = subresources
	}
	switch {
	case except && (page || hiding):
		// Pages of the filter's: what blocks, or hides, in them is undone.
		if len(t.IfDomain)+len(t.UnlessDomain) > 0 {
			return false // WebKit takes one condition
		}
		if d, ok := filterDomain(pattern); ok {
			t.IfDomain = []string{"*" + d}
		} else {
			t.IfTopURL = []string{filter}
		}
		t.URLFilter, t.ResourceType, t.LoadContext, t.LoadType = ".*", nil, nil, nil
		if hiding && !page {
			t.ResourceType = []string{"document"}
		}
		c.Except = append(c.Except, Rule{Trigger: t, Action: Action{Type: "ignore-previous-rules"}})
	case except:
		c.Except = append(c.Except, Rule{Trigger: t, Action: Action{Type: "ignore-previous-rules"}})
	case important:
		c.Important = append(c.Important, Rule{Trigger: t, Action: Action{Type: "block"}})
	default:
		c.Block = append(c.Block, Rule{Trigger: t, Action: Action{Type: "block"}})
	}
	return true
}

// optionNames are the names of filters' options, those WebKit can do
// and those it cannot, which tell a filter's options after its last "$"
// from a "$" of its pattern.
var optionNames = []string{
	"script", "image", "stylesheet", "css", "font", "media", "xmlhttprequest", "xhr", "subdocument",
	"frame", "websocket", "ping", "popup", "object", "object-subrequest", "other", "document", "doc",
	"all", "third-party", "3p", "first-party", "1p", "domain", "from", "match-case", "important",
	"elemhide", "ehide", "generichide", "ghide", "specifichide", "shide", "genericblock",
	"redirect", "redirect-rule", "csp", "removeparam", "removeheader", "replace", "rewrite", "header",
	"permissions", "cookie", "empty", "mp4", "badfilter", "denyallow", "to", "method", "sitekey",
	"webrtc", "popunder", "inline-script", "inline-font", "strict1p", "strict3p", "urltransform",
	"uritransform", "jsonprune", "hls", "network", "app", "extension", "content", "stealth",
	"urlblock", "jsinject", "referrerpolicy", "ipaddress", "reason",
}

// filterOptions reports whether s may be the options of a filter, after
// its "$", rather than part of its pattern: it starts with an option.
func filterOptions(s string) bool {
	name, _, _ := strings.Cut(s, ",")
	name, _, _ = strings.Cut(name, "=")
	return slices.Contains(optionNames, strings.TrimPrefix(name, "~"))
}

// filterDomain returns the domain of a pattern that is one, "||domain^".
func filterDomain(pattern string) (string, bool) {
	d, ok := strings.CutPrefix(pattern, "||")
	if !ok {
		return "", false
	}
	d = strings.TrimSuffix(strings.TrimSuffix(d, "|"), "^")
	d = strings.ToLower(d)
	return d, validDomain(d)
}

// hostPrefix is what "||" becomes: the URL's scheme, and any subdomains.
const hostPrefix = `^[^:]+://+([^:/]+\.)?`

// separator is Adblock Plus's "^": a character other than a letter, a
// digit, or one of "_-.%".
const separator = `[^a-zA-Z0-9_.%-]`

// urlFilter converts a filter's pattern into a regular expression WebKit
// takes, false for one it cannot: with characters other than ASCII.
func urlFilter(pattern string) (string, bool) {
	var b strings.Builder
	host := false // in the host, after "||"
	if p, ok := strings.CutPrefix(pattern, "||"); ok {
		b.WriteString(hostPrefix)
		pattern, host = p, true
	} else if p, ok := strings.CutPrefix(pattern, "|"); ok {
		b.WriteByte('^')
		pattern = p
	}
	end := false
	if p, ok := strings.CutSuffix(pattern, "|"); ok {
		pattern, end = p, true
	}
	if host && (pattern == "" || pattern[0] == '*' || pattern[0] == '^') {
		return "", false // matches every URL
	}
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case c >= 0x80 || c < 0x20:
			return "", false
		case c == '*':
			if !strings.HasSuffix(b.String(), ".*") {
				b.WriteString(".*")
			}
		case c == '^' && host:
			// After a host comes a port or the path, which a URL always
			// has.
			b.WriteString("[/:]")
			host = false
		case c == '^' && i == len(pattern)-1 && !end:
			b.WriteString("(" + separator + ".*)?$")
		case c == '^':
			b.WriteString(separator)
		case strings.IndexByte(`.+?()[]{}\$|`, c) >= 0:
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			if c == '/' || c == ':' || c == '?' || c == '#' {
				host = false
			}
			b.WriteByte(c)
		}
	}
	if end {
		b.WriteByte('$')
	}
	if b.Len() == 0 {
		return ".*", true
	}
	return b.String(), true
}
