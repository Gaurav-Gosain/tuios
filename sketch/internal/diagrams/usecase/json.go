// Ported from pele src/diagrams/usecase/json.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// The text is read as UTF-16 units, as pele reads it, so that offsets in messages agree.
// JSON.parse is js.ParseJSON, a port of V8's parser with its messages.

package usecase

import (
	"math"
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/js"
	sktext "tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

type OrderedJson struct {
	value         *jsonObject
	propertyOrder *keyOrder
}

// keyOrder is pele's Map from JSON Pointer to keys. A pointer is a node of a tree of its
// segments, which hold no '/', so two pointers are equal when their nodes are. pele builds
// each pointer by adding to its parent's, which JavaScript's ropes make cheap; Go would copy
// the whole pointer at every level of a deep text. The root, "", is node 0. Not in pele.
type keyOrder struct {
	nodes map[segment]int
	order map[int][]string
}

type segment struct {
	parent int
	text   string
}

func newKeyOrder() *keyOrder {
	return &keyOrder{nodes: map[segment]int{}, order: map[int][]string{}}
}

// child is the pointer parent + "/" + text, made if it is new.
func (o *keyOrder) child(parent int, text string) int {
	key := segment{parent, text}
	if id, ok := o.nodes[key]; ok {
		return id
	}
	id := len(o.nodes) + 1
	o.nodes[key] = id
	return id
}

// lookup is the pointer parent + "/" + text, or -1 when no key order was kept for it or below it.
func (o *keyOrder) lookup(parent int, text string) int {
	if parent < 0 {
		return -1
	}
	if id, ok := o.nodes[segment{parent, text}]; ok {
		return id
	}
	return -1
}

// get is order.get(pointer).
func (o *keyOrder) get(pointer int) ([]string, bool) {
	keys, ok := o.order[pointer]
	return keys, ok
}

type jsonObject = js.JSONObject

// walkError is thrown with the offset of the first character that is not valid JSON.
type walkError struct{ offset int }

type frame struct {
	object  bool
	pointer int
	keys    *[]string
	// For each key read so far, the stretch of `created` its value added.
	ranges map[string][2]int
	key    string
	from   int
	index  int
}

const (
	stValue = iota
	stKey
	stAfter
)

func pointerKey(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

// propertyOrder walks JSON text that JSON.parse has accepted, to recover the order its keys were written in,
// which JSON.parse loses for integer-like keys. On text JSON.parse rejected it finds where.
// Nesting is kept on a stack of its own, so depth is limited by memory and not by the call stack.
func propertyOrder(text []uint16) *keyOrder {
	tree := newKeyOrder()
	order := map[int]*[]string{}
	var created []int
	var stack []*frame
	n := len(text)
	at := 0
	pointer := 0
	state := stValue

	char := func(k int) int {
		if k >= n {
			return -1
		}
		return int(text[k])
	}
	skip := func() {
		for at < n {
			c := text[at]
			if c != 32 && c != 9 && c != 10 && c != 13 {
				return
			}
			at++
		}
	}

	readString := func(decode bool) string {
		at++
		var value []uint16
		for at < n {
			start := at
			ch := text[at]
			at++
			if ch == '"' {
				return js.DecodeUTF16(value)
			}
			if ch < 0x20 {
				panic(walkError{start})
			}
			if ch != '\\' {
				if decode {
					value = append(value, ch)
				}
				continue
			}
			escapeAt := at
			escape := char(at)
			at++
			var decoded uint16
			switch escape {
			case '"', '\\', '/':
				decoded = uint16(escape)
			case 'b':
				decoded = '\b'
			case 'f':
				decoded = '\f'
			case 'n':
				decoded = '\n'
			case 'r':
				decoded = '\r'
			case 't':
				decoded = '\t'
			case 'u':
				if at+4 > n {
					panic(walkError{at})
				}
				v := 0
				for k := at; k < at+4; k++ {
					d := hexDigit(int(text[k]))
					if d < 0 {
						panic(walkError{at})
					}
					v = v*16 + d
				}
				decoded = uint16(v)
				at += 4
			default:
				panic(walkError{escapeAt})
			}
			if decode {
				value = append(value, decoded)
			}
		}
		panic(walkError{at})
	}

	literal := func(word string) {
		for k := 0; k < len(word); k++ {
			if char(at+k) != int(word[k]) {
				panic(walkError{at + k})
			}
		}
		at += len(word)
	}

	number := func() {
		if char(at) == 45 {
			at++
		}
		if char(at) == 48 {
			at++
		} else if isDigit(char(at)) {
			for isDigit(char(at)) {
				at++
			}
		} else {
			panic(walkError{at})
		}
		if char(at) == 46 {
			at++
			if !isDigit(char(at)) {
				panic(walkError{at})
			}
			for isDigit(char(at)) {
				at++
			}
		}
		if char(at)|32 == 101 {
			at++
			if char(at) == 43 || char(at) == 45 {
				at++
			}
			if !isDigit(char(at)) {
				panic(walkError{at})
			}
			for isDigit(char(at)) {
				at++
			}
		}
	}

	skip()
	for {
		if state == stValue {
			skip()
			c := char(at)
			state = stAfter
			switch {
			case c == 123:
				at++
				keys := &[]string{}
				order[pointer] = keys
				created = append(created, pointer)
				skip()
				if char(at) == 125 {
					at++
				} else {
					stack = append(stack, &frame{object: true, pointer: pointer, keys: keys, ranges: map[string][2]int{}})
					state = stKey
				}
			case c == 91:
				at++
				skip()
				if char(at) == 93 {
					at++
				} else {
					stack = append(stack, &frame{object: false, pointer: pointer, keys: &[]string{}, ranges: map[string][2]int{}})
					pointer = tree.child(pointer, "0")
					state = stValue
				}
			case c == 34:
				readString(false)
			case c == 116:
				literal("true")
			case c == 102:
				literal("false")
			case c == 110:
				literal("null")
			case c == 45 || isDigit(c):
				number()
			default:
				panic(walkError{at})
			}
		} else if state == stKey {
			f := stack[len(stack)-1]
			if char(at) != 34 {
				panic(walkError{at})
			}
			key := readString(true)
			// A repeated key keeps its first place in the order and takes the nested order of its last value.
			if r, ok := f.ranges[key]; !ok {
				*f.keys = append(*f.keys, key)
			} else {
				for k := r[0]; k < r[1]; k++ {
					delete(order, created[k])
				}
			}
			f.key = key
			f.from = len(created)
			skip()
			if char(at) != 58 {
				panic(walkError{at})
			}
			at++
			pointer = tree.child(f.pointer, pointerKey(key))
			state = stValue
		} else {
			if len(stack) == 0 {
				break
			}
			f := stack[len(stack)-1]
			if f.object {
				f.ranges[f.key] = [2]int{f.from, len(created)}
			} else {
				f.index++
			}
			skip()
			c := char(at)
			closer := 93
			if f.object {
				closer = 125
			}
			if c == closer {
				at++
				stack = stack[:len(stack)-1]
			} else if c != 44 {
				panic(walkError{at})
			} else {
				at++
				skip()
				pointer = tree.child(f.pointer, strconv.Itoa(f.index))
				if f.object {
					state = stKey
				} else {
					state = stValue
				}
			}
		}
	}
	skip()
	if at != n {
		panic(walkError{at})
	}
	for k, v := range order {
		tree.order[k] = *v
	}
	return tree
}

func hexDigit(c int) int {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return -1
}

func locationAt(text []uint16, offset float64, line, column float64) (float64, float64) {
	end := int(math.Min(math.Max(offset, 0), float64(len(text))))
	for i := 0; i < end; i++ {
		c := text[i]
		if c == 13 {
			line++
			column = 1
		} else if c == 10 {
			if i == 0 || text[i-1] != 13 {
				line++
				column = 1
			}
		} else {
			column++
		}
	}
	return line, column
}

// wordByte is a character of `\w`.
func wordByte(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

// findNumbers finds /\b<words> (\d+)<more> (\d+)\b/ as the regexp does: the first place where
// each pattern piece matches, with the word boundaries. more is "" for one number.
func findNumbers(message, words, more string) (a, b string, ok bool) {
	digits := func(s string, k int) int {
		d := k
		for d < len(s) && s[d] >= '0' && s[d] <= '9' {
			d++
		}
		return d
	}
	for i := 0; i+len(words) <= len(message); i++ {
		if !strings.HasPrefix(message[i:], words) || (i > 0 && wordByte(message[i-1])) {
			continue
		}
		k := i + len(words)
		d := digits(message, k)
		if d == k {
			continue
		}
		if more == "" {
			// \d+ then \b: backing off a digit leaves a digit after it, so only the longest run can match.
			if d < len(message) && wordByte(message[d]) {
				continue
			}
			return message[k:d], "", true
		}
		if !strings.HasPrefix(message[d:], more) {
			continue
		}
		k2 := d + len(more)
		d2 := digits(message, k2)
		if d2 == k2 || (d2 < len(message) && wordByte(message[d2])) {
			continue
		}
		return message[k:d], message[k2:d2], true
	}
	return "", "", false
}

// locationFromMessage: engines word JSON.parse errors differently: some give an offset, some a line and column.
func locationFromMessage(message string, text []uint16, line, column float64) (float64, float64, bool) {
	if position, _, ok := findNumbers(message, "position ", ""); ok {
		n, _ := strconv.ParseFloat(position, 64)
		l, c := locationAt(text, n, line, column)
		return l, c, true
	}
	if l, c, ok := findNumbers(message, "line ", " column "); ok {
		localLine, _ := strconv.ParseFloat(l, 64)
		localColumn, _ := strconv.ParseFloat(c, 64)
		if localLine == 1 {
			return line + localLine - 1, column + localColumn - 1, true
		}
		return line + localLine - 1, localColumn, true
	}
	lower := asciiLower(message)
	if strings.Contains(lower, "unexpected end") || strings.Contains(lower, "end of json input") {
		l, c := locationAt(text, float64(len(text)), line, column)
		return l, c, true
	}
	return 0, 0, false
}

func asciiLower(s string) string {
	b := []byte(s)
	for k, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[k] = c + 32
		}
	}
	return string(b)
}

func jsonError(message string, line, column float64) *types.PeleError {
	return types.NewPeleError(message+" (line "+js.Num(line)+", column "+js.Num(column)+")", types.CodeSyntax, types.ErrorInfo{Type: "usecase", Line: int(line), Column: int(column)})
}

// walkOffset is where propertyOrder stops on text it rejects, or 0.
func walkOffset(text []uint16) (offset int) {
	defer func() {
		if r := recover(); r != nil {
			w, ok := r.(walkError)
			if !ok {
				panic(r)
			}
			offset = w.offset
		}
	}()
	propertyOrder(text)
	return 0
}

// parseOrderedJson parses the body of a `json` node. `origin` gives the line and column the text starts at in the diagram.
func parseOrderedJson(source string, origin func() (int, int)) OrderedJson {
	text := js.UTF16(source)
	parsed, err := js.ParseJSON(text)
	if err != nil {
		message := err.Error()
		// Reparse restored entity codes so error messages and positions match the source.
		shown := text
		if written := sktext.WrittenCodes(source); written != source {
			shown = js.UTF16(written)
			if _, again := js.ParseJSON(shown); again != nil {
				message = again.Error()
			}
		}
		line, column := origin()
		l, c, ok := locationFromMessage(message, shown, float64(line), float64(column))
		if !ok {
			l, c = locationAt(shown, float64(walkOffset(shown)), float64(line), float64(column))
		}
		panic(jsonError("Invalid JSON: "+message, l, c))
	}
	obj, ok := parsed.(*jsonObject)
	if !ok {
		line, column := origin()
		panic(jsonError("JSON value must have an object root", float64(line), float64(column)))
	}
	return OrderedJson{value: obj, propertyOrder: propertyOrder(text)}
}
