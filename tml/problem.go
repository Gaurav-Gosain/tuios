package tml

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Severity tells an error from a warning. A document with warnings alone
// still renders.
type Severity string

// The severities.
const (
	SevError   Severity = "error"
	SevWarning Severity = "warning"
)

// Problem is one thing wrong with a document. At is a JSON Pointer into the
// JSON form: /children/0/children/2 names a node, and
// /children/0/children/2/rows names one of its attributes.
type Problem struct {
	At         string   `json:"at"`
	Line       int      `json:"line,omitempty"`
	Col        int      `json:"col,omitempty"`
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	Severity   Severity `json:"severity,omitempty"`
	DidYouMean string   `json:"did_you_mean,omitempty"`
	Accepted   []string `json:"accepted,omitempty"`
	Available  []string `json:"available,omitempty"`
}

// The problem codes. Each names one rule, so a program can act on it.
const (
	CodeSyntax           = "syntax"            // the text or JSON does not parse
	CodeUnsupportedXML   = "unsupported_xml"   // DTD, entity, namespace, CDATA or processing instruction
	CodeUnknownComponent = "unknown_component" // a type that is not in the catalog
	CodeUnknownAttribute = "unknown_attribute" // an attribute the component does not have
	CodeMissingAttribute = "missing_attribute" // a required attribute is absent
	CodeBadValue         = "bad_value"         // a value of the wrong type or out of range
	CodeHexColor         = "hex_color"         // a colour written as hex instead of a role
	CodeBadChild         = "bad_child"         // a child the parent does not accept
	CodeTextNotAllowed   = "text_not_allowed"  // text where only components may go
	CodeChildCount       = "child_count"       // too few or too many children
	CodeBadExpression    = "bad_expression"    // an expression does not parse
	CodeUnknownFunction  = "unknown_function"  // a function that is not in the list
	CodeUnknownRoot      = "unknown_path_root" // a path that starts with an unknown name
	CodeBindingMissing   = "binding_missing"   // a path the state does not have (warning)
	CodeBadAction        = "bad_action"        // an action call that is not allowed
	CodeDuplicateID      = "duplicate_id"      // two nodes with one id
	CodeMisplacedState   = "misplaced_state"   // state anywhere but the root
	CodePasswordRefused  = "password_refused"  // v1 has no password input
	CodeLimit            = "limit"             // over a size, depth or count limit
	CodeBadRoot          = "bad_root"          // the root is not a block component

	// Patch error codes.
	CodeTestFailed  = "test_failed"  // a test operation did not match: read the state again
	CodePathMissing = "path_missing" // a patch names a path the state does not have
)

// Error is the error a parser or the validator returns. It marshals to the
// protocol's error object: {"error": {"code": "invalid_tml", ...}}.
type Error struct {
	Problems []Problem
}

// Error implements error.
func (e *Error) Error() string {
	n := e.errorCount()
	if n == 1 {
		for _, p := range e.Problems {
			if p.Severity != SevWarning {
				return p.String()
			}
		}
	}
	return fmt.Sprintf("%d problems in the document", n)
}

func (e *Error) errorCount() int {
	n := 0
	for _, p := range e.Problems {
		if p.Severity != SevWarning {
			n++
		}
	}
	return n
}

// MarshalJSON writes the protocol's error object.
func (e *Error) MarshalJSON() ([]byte, error) {
	n := e.errorCount()
	msg := fmt.Sprintf("%d problems in the document", n)
	if n == 1 {
		msg = "1 problem in the document"
	}
	return json.Marshal(map[string]any{"error": map[string]any{
		"code": "invalid_tml", "message": msg, "problems": e.Problems,
	}})
}

// String formats a problem as line:col: message (code), with the suggestion.
func (p Problem) String() string {
	var b strings.Builder
	if p.Line > 0 {
		fmt.Fprintf(&b, "%d:%d: ", p.Line, p.Col)
	}
	if p.Severity == SevWarning {
		b.WriteString("warning: ")
	}
	b.WriteString(p.Message)
	fmt.Fprintf(&b, " (%s at %s)", p.Code, pointerOrRoot(p.At))
	if p.DidYouMean != "" {
		fmt.Fprintf(&b, " Did you mean %q?", p.DidYouMean)
	}
	return b.String()
}

func pointerOrRoot(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

// suggest returns the closest candidate to s, or "". A hyphen segment that
// is itself a candidate wins: chart-bar suggests bar. Otherwise the candidate
// at the smallest edit distance wins, when that distance is small for the
// length of s.
func suggest(s string, candidates []string) string {
	ls := strings.ToLower(s)
	for _, c := range candidates {
		if strings.ToLower(c) == ls {
			return c
		}
	}
	if strings.ContainsAny(ls, "-_.:") {
		parts := strings.FieldsFunc(ls, func(r rune) bool { return strings.ContainsRune("-_.:", r) })
		for i := len(parts) - 1; i >= 0; i-- {
			for _, c := range candidates {
				if c == parts[i] {
					return c
				}
			}
		}
	}
	best, bestD := "", 1<<30
	for _, c := range candidates {
		if d := levenshtein(ls, strings.ToLower(c)); d < bestD || (d == bestD && c < best) {
			best, bestD = c, d
		}
	}
	limit := max(2, len(ls)/3)
	if len(ls) <= 4 {
		limit = 1 // body is not a typo of code
	}
	if bestD <= limit && bestD < len(ls) {
		return best
	}
	// A candidate that is a prefix of s, or s of it, still reads as a typo:
	// sortBy suggests sort.
	for _, c := range sortedCopy(candidates) {
		if len(c) >= 3 && (strings.HasPrefix(ls, strings.ToLower(c)) || strings.HasPrefix(strings.ToLower(c), ls) && len(ls) >= 3) {
			return c
		}
	}
	return ""
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) > 64 || len(rb) > 64 {
		return 1 << 20
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
