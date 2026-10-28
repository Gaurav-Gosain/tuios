// Ported from pele src/diagrams/class/members.ts at 8aff183be57021b533f319a29341a529be934b08.

package class

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
)

// Visibility is '#', '+', '~', '-' or "".
type Visibility = string

func isVisibility(ch string) bool {
	return ch == "#" || ch == "+" || ch == "~" || ch == "-"
}

type ClassMember struct {
	ID string
	// "method" or "attribute".
	MemberType string
	Visibility Visibility
	Classifier string
	Parameters string
	ReturnType string
}

func NewClassMember(input string, memberType string) *ClassMember {
	m := &ClassMember{MemberType: memberType}
	// Mermaid throws a TypeError for a method with no opening parenthesis, such as `run)`.
	// It is kept here as an attribute, so it is shown as written.
	if memberType != "method" || !m.parseMethod(input) {
		m.parseAttribute(input)
	}
	if strings.HasPrefix(m.ID, " ") {
		m.ID = " " + js.Trim(m.ID)
	} else {
		m.ID = js.Trim(m.ID)
	}
	return m
}

// lineEndAt is the length in bytes of the line end at q, or 0 where there is none: isLineEnd over UTF-8.
func lineEndAt(input string, q int) int {
	c := input[q]
	if c == '\n' || c == '\r' {
		return 1
	}
	if c == 0xe2 && q+2 < len(input) && input[q+1] == 0x80 && (input[q+2] == 0xa8 || input[q+2] == 0xa9) {
		return 3
	}
	return 0
}

// The same split as Mermaid's /([#+~-])?(.+)\((.*)\)([\s$*])?(.*)([$*])?/, found in one pass:
// the first line with an opening parenthesis after its first character and a closing one later.
func (m *ClassMember) parseMethod(input string) bool {
	n := len(input)
	for start := 0; start < n; {
		end := start
		for end < n && lineEndAt(input, end) == 0 {
			end++
		}
		close := end - 1
		for close > start && input[close] != 41 {
			close--
		}
		open := close - 1
		for open > start && input[open] != 40 {
			open--
		}
		if open > start {
			first := input[start : start+1]
			visible := isVisibility(first) && open > start+1
			if visible {
				m.Visibility = first
			}
			from := start
			if visible {
				from = start + 1
			}
			m.ID = input[from:open]
			m.Parameters = js.Trim(input[open+1 : close])
			rest := close + 1
			classifier := ""
			if rest < n {
				next, size := utf8.DecodeRuneInString(input[rest:])
				if next == '$' || next == '*' {
					classifier = string(next)
					rest++
				} else if next != utf8.RuneError && js.IsSpace(next) {
					// next.trim() === '': a whitespace character, which trims to nothing.
					rest += size
				}
			}
			stop := rest
			for stop < n && lineEndAt(input, stop) == 0 {
				stop++
			}
			m.ReturnType = js.Trim(input[rest:stop])
			if classifier == "" && m.ReturnType != "" {
				last := m.ReturnType[len(m.ReturnType)-1:]
				if last == "$" || last == "*" {
					classifier = last
					m.ReturnType = m.ReturnType[:len(m.ReturnType)-1]
				}
			}
			m.Classifier = classifier
			return true
		}
		if end < n {
			start = end + lineEndAt(input, end)
		} else {
			start = end + 1
		}
	}
	m.MemberType = "attribute"
	return false
}

func (m *ClassMember) parseAttribute(input string) {
	first := ""
	last := ""
	if input != "" {
		first = input[:1]
		last = input[len(input)-1:]
	}
	if isVisibility(first) {
		m.Visibility = first
	}
	if last == "$" || last == "*" {
		m.Classifier = last
	}
	from := 0
	if m.Visibility != "" {
		from = 1
	}
	to := len(input)
	if m.Classifier != "" {
		to = len(input) - 1
	}
	if from > to {
		from, to = to, from
	}
	m.ID = input[from:to]
}

func (m *ClassMember) GetDisplayDetails() (displayText string, cssStyle string) {
	displayText = m.Visibility + common.ParseGenericTypes(m.ID)
	if m.MemberType == "method" {
		displayText += "(" + common.ParseGenericTypes(js.Trim(m.Parameters)) + ")"
		if m.ReturnType != "" {
			displayText += " : " + common.ParseGenericTypes(m.ReturnType)
		}
	}
	return js.Trim(displayText), m.parseClassifier()
}

func (m *ClassMember) parseClassifier() string {
	switch m.Classifier {
	case "*":
		return "font-style:italic;"
	case "$":
		return "text-decoration:underline;"
	}
	return ""
}
