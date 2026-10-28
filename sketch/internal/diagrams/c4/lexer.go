// Ported from pele src/diagrams/c4/lexer.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// The port reads UTF-8 bytes, and offsets are byte offsets. Every character the lexer
// matches is ASCII, except whitespace and line ends, which are read as whole characters.
// RE_DIRECTION and RE_LINE_END are hand matchers.

package c4

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/util"
)

// Tokens: token text is src[starts[i]:ends[i]]; nothing is copied until the parser asks for it.
type Tokens struct {
	types  []int
	starts []int
	ends   []int
}

const (
	S_INITIAL = iota
	S_struct
	S_attribute
	S_string
	S_string_kv
	S_string_kv_key
	S_string_kv_value
	S_acc
	S_acc_descr_multiline
)

// keyword is KEYWORDS.get(word), or -1.
func keyword(word string) int {
	switch word {
	case "C4Context":
		return T_C4_CONTEXT
	case "C4Container":
		return T_C4_CONTAINER
	case "C4Component":
		return T_C4_COMPONENT
	case "C4Dynamic":
		return T_C4_DYNAMIC
	case "C4Deployment":
		return T_C4_DEPLOYMENT
	case "Person":
		return T_PERSON
	case "Person_Ext":
		return T_PERSON_EXT
	case "System":
		return T_SYSTEM
	case "SystemDb":
		return T_SYSTEM_DB
	case "SystemQueue":
		return T_SYSTEM_QUEUE
	case "System_Ext":
		return T_SYSTEM_EXT
	case "SystemDb_Ext":
		return T_SYSTEM_EXT_DB
	case "SystemQueue_Ext":
		return T_SYSTEM_EXT_QUEUE
	case "Container":
		return T_CONTAINER
	case "ContainerDb":
		return T_CONTAINER_DB
	case "ContainerQueue":
		return T_CONTAINER_QUEUE
	case "Container_Ext":
		return T_CONTAINER_EXT
	case "ContainerDb_Ext":
		return T_CONTAINER_EXT_DB
	case "ContainerQueue_Ext":
		return T_CONTAINER_EXT_QUEUE
	case "Component":
		return T_COMPONENT
	case "ComponentDb":
		return T_COMPONENT_DB
	case "ComponentQueue":
		return T_COMPONENT_QUEUE
	case "Component_Ext":
		return T_COMPONENT_EXT
	case "ComponentDb_Ext":
		return T_COMPONENT_EXT_DB
	case "ComponentQueue_Ext":
		return T_COMPONENT_EXT_QUEUE
	case "Enterprise_Boundary":
		return T_ENTERPRISE_BOUNDARY
	case "System_Boundary":
		return T_SYSTEM_BOUNDARY
	case "Boundary":
		return T_BOUNDARY
	case "Container_Boundary":
		return T_CONTAINER_BOUNDARY
	case "Deployment_Node", "Node":
		return T_NODE
	case "Node_L":
		return T_NODE_L
	case "Node_R":
		return T_NODE_R
	case "Rel":
		return T_REL
	case "BiRel":
		return T_BIREL
	case "Rel_Up", "Rel_U":
		return T_REL_U
	case "Rel_Down", "Rel_D":
		return T_REL_D
	case "Rel_Left", "Rel_L":
		return T_REL_L
	case "Rel_Right", "Rel_R":
		return T_REL_R
	case "Rel_Back":
		return T_REL_B
	case "RelIndex":
		return T_REL_INDEX
	case "UpdateElementStyle":
		return T_UPDATE_EL_STYLE
	case "UpdateRelStyle":
		return T_UPDATE_REL_STYLE
	case "UpdateLayoutConfig":
		return T_UPDATE_LAYOUT_CONFIG
	}
	return -1
}

// DIRECTIONS are the words of RE_DIRECTION, in the order they are tried.
var DIRECTIONS = [4]string{"TB", "BT", "RL", "LR"}

// spaceAt is the length in bytes of the whitespace character at q, or 0 if there is none.
// bareTitle matches /title[^\S\r\n]*(?=[\r\n]|$)\s*/y at p and returns its end, or -1. It
// takes the line breaks too, because the grammar rejects blank statements.
func bareTitle(src string, p int) int {
	if !strings.HasPrefix(src[p:], "title") {
		return -1
	}
	q := p + 5
	for q < len(src) && src[q] != '\r' && src[q] != '\n' {
		s := spaceAt(src, q)
		if s == 0 {
			break
		}
		q += s
	}
	if q < len(src) && src[q] != '\r' && src[q] != '\n' {
		return -1
	}
	for s := spaceAt(src, q); s > 0; s = spaceAt(src, q) {
		q += s
	}
	return q
}

func spaceAt(src string, q int) int {
	if q < 0 || q >= len(src) {
		return 0
	}
	if c := src[q]; c < utf8.RuneSelf {
		if util.IsSpace(rune(c)) {
			return 1
		}
		return 0
	}
	r, size := utf8.DecodeRuneInString(src[q:])
	if util.IsSpace(r) {
		return size
	}
	return 0
}

// lineEndAt is the length in bytes of the line terminator at q (\n, \r, U+2028, U+2029), or 0.
func lineEndAt(src string, q int) int {
	switch {
	case q >= len(src):
		return 0
	case src[q] == '\n' || src[q] == '\r':
		return 1
	case strings.HasPrefix(src[q:], " ") || strings.HasPrefix(src[q:], " "):
		return 3
	}
	return 0
}

// direction is the end of /.*direction\s+WORD[^\n]*/y at p, or -1. lineEnd is where the line
// of p ends, which `.*` cannot cross. `.*` is greedy, so the last start in the line wins.
func direction(src string, p, lineEnd int, word string) int {
	for q := lineEnd - len("direction"); q >= p; q-- {
		if !strings.HasPrefix(src[q:], "direction") {
			continue
		}
		r := q + len("direction")
		s := r
		for {
			size := spaceAt(src, s)
			if size == 0 {
				break
			}
			s += size
		}
		if s == r || !strings.HasPrefix(src[s:], word) {
			continue
		}
		e := strings.IndexByte(src[s+len(word):], '\n')
		if e == -1 {
			return len(src)
		}
		return s + len(word) + e
	}
	return -1
}

// tokenize follows the lexer Jison compiles from c4Diagram.jison: its rule order, its start conditions,
// and what it returns once the input runs out in each of them.
func tokenize(src string) Tokens {
	n := len(src)
	var toks Tokens
	state := S_INITIAL
	accValue := T_acc_title_value
	p := 0
	done := false
	// Where the next `direction` is; -2 until the first search.
	dirNext := -2
	lineEnd := -1
	dirFailed := 0

	emit := func(typ, s, e int) {
		toks.types = append(toks.types, typ)
		toks.starts = append(toks.starts, s)
		toks.ends = append(toks.ends, e)
		p = e
	}
	at := func(q int) int {
		if q < 0 || q >= n {
			return -1
		}
		return int(src[q])
	}
	skipWs := func(q int) int {
		for {
			size := spaceAt(src, q)
			if size == 0 {
				return q
			}
			q += size
		}
	}
	upTo := func(ch byte, from int) int {
		if from >= n {
			return n
		}
		q := strings.IndexByte(src[from:], ch)
		if q == -1 {
			return n
		}
		return from + q
	}
	// `word\s[^#\n;]+`
	statement := func(word string, typ int) bool {
		q := p + len(word)
		if !strings.HasPrefix(src[p:], word) {
			return false
		}
		size := spaceAt(src, q)
		if size == 0 {
			return false
		}
		q += size
		from := q
		for q < n {
			c := src[q]
			if c == 35 || c == 10 || c == 59 {
				break
			}
			q++
		}
		if q == from {
			return false
		}
		emit(typ, p, q)
		return true
	}

	for {
		// Jison notes the end of input one call before it reports it, so a rule that matches
		// nothing gets one last turn.
		if done {
			emit(T_END, n, n)
			break
		}
		if p >= n {
			done = true
		}
		c := at(p)

		if state == S_INITIAL {
			if p >= n {
				emit(T_EOF, n, n)
				continue
			}
			if dirNext != -1 && p >= dirFailed {
				if dirNext < p {
					dirNext = strings.Index(src[p:], "direction")
					if dirNext != -1 {
						dirNext += p
					}
				}
				if dirNext != -1 {
					if lineEnd < p {
						lineEnd = n
						for q := p; q < n; q++ {
							if lineEndAt(src, q) > 0 {
								lineEnd = q
								break
							}
						}
					}
					if dirNext < lineEnd {
						matched := false
						for i := 0; i < 4 && !matched; i++ {
							if e := direction(src, p, lineEnd, DIRECTIONS[i]); e != -1 {
								emit(T_direction_tb+i, p, e)
								matched = true
							}
						}
						if matched {
							continue
						}
						// No direction statement starts anywhere in the rest of this line either.
						dirFailed = lineEnd
					}
				}
			}
			if c == 116 {
				if e := bareTitle(src, p); e >= 0 {
					p = e
					continue
				}
				if statement("title", T_title) {
					continue
				}
			}
			if c == 97 {
				if statement("accDescription", T_accDescription) {
					continue
				}
				title := strings.HasPrefix(src[p:], "accTitle")
				if title || strings.HasPrefix(src[p:], "accDescr") {
					q := skipWs(p + 8)
					d := at(q)
					if d == 58 {
						if title {
							emit(T_acc_title, p, skipWs(q+1))
							accValue = T_acc_title_value
						} else {
							emit(T_acc_descr, p, skipWs(q+1))
							accValue = T_acc_descr_value
						}
						state = S_acc
						continue
					}
					if d == 123 && !title {
						p = skipWs(q + 1)
						state = S_acc_descr_multiline
						continue
					}
				}
			}
			if c == 37 && at(p+1) == 37 {
				// A comment takes the line breaks after it, so no NEWLINE follows.
				q := upTo('\n', p+2)
				for q < n && (src[q] == 10 || src[q] == 13) {
					q++
				}
				p = q
				continue
			}
			if spaceAt(src, p) > 0 {
				q := p
				last := -1
				for q < n {
					size := spaceAt(src, q)
					if size == 0 {
						break
					}
					if src[q] == 10 {
						last = q
					}
					q += size
				}
				if last != -1 {
					emit(T_NEWLINE, p, last+1)
				} else {
					p = q
				}
				continue
			}
			if c == 123 {
				emit(T_LBRACE, p, p+1)
				continue
			}
			if c == 125 {
				emit(T_RBRACE, p, p+1)
				continue
			}
			if util.IsWord(rune(c)) {
				q := p + 1
				for q < n && util.IsWord(rune(src[q])) {
					q++
				}
				typ := -1
				if q-p <= 19 {
					typ = keyword(src[p:q])
				}
				if typ != -1 {
					if typ > T_C4_DEPLOYMENT {
						state = S_struct
					}
					emit(typ, p, q)
					continue
				}
			}
			emit(T_ERROR, p, p)
			break
		}

		if state == S_struct {
			if p >= n {
				emit(T_EOF_IN_STRUCT, n, n)
			} else if c == 40 {
				q := p + 1
				for at(q) == 32 {
					q++
				}
				if at(q) == 44 {
					emit(T_ATTRIBUTE_EMPTY, p, q+1)
				} else {
					p++
				}
				state = S_attribute
			} else if c == 41 {
				p++
				state = S_INITIAL
			} else {
				emit(T_ERROR, p, p)
				break
			}
			continue
		}

		if state == S_attribute {
			if p >= n {
				continue
			}
			if c == 41 {
				p++
				state = S_INITIAL
			} else if c == 44 {
				if at(p+1) == 44 {
					emit(T_ATTRIBUTE_EMPTY, p, p+2)
				} else {
					p++
				}
			} else {
				q := p
				for at(q) == 32 {
					q++
				}
				d := at(q)
				if d == 34 {
					if at(q+1) == 34 {
						emit(T_ATTRIBUTE_EMPTY, p, q+2)
					} else {
						p = q + 1
						state = S_string
					}
				} else if d == 36 {
					p = q + 1
					state = S_string_kv
				} else {
					// An unquoted value runs to the next comma, through closing parentheses and line breaks.
					emit(T_STR, p, upTo(',', p))
				}
			}
			continue
		}

		if state == S_string {
			if c == 34 {
				p++
				state = S_attribute
			} else {
				emit(T_STR, p, upTo('"', p))
			}
			continue
		}

		if state == S_string_kv {
			emit(T_STR_KEY, p, upTo('=', p))
			state = S_string_kv_key
			continue
		}

		if state == S_string_kv_key {
			if p >= n {
				continue
			}
			q := p + 1
			if c == 61 {
				for at(q) == 32 {
					q++
				}
			}
			if c != 61 || at(q) != 34 {
				emit(T_ERROR, p, p)
				break
			}
			p = q + 1
			state = S_string_kv_value
			continue
		}

		if state == S_string_kv_value {
			if p >= n {
				continue
			}
			if c == 34 {
				p++
				state = S_attribute
			} else {
				emit(T_STR_VALUE, p, upTo('"', p))
			}
			continue
		}

		if state == S_acc {
			emit(accValue, p, upTo('\n', p))
			state = S_INITIAL
			continue
		}

		if c == 125 {
			p++
			state = S_INITIAL
		} else {
			emit(T_acc_descr_multiline_value, p, upTo('}', p))
		}
	}

	return toks
}
