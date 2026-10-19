// Package js holds the few JavaScript semantics the port depends on for byte
// identical output: Math.round, Number to string, the \s whitespace set, trim,
// parseFloat and Number(), and UTF-16 lengths. It is not a file of pele.
package js

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Round is Math.round: the nearest integer, ties toward +Infinity.
func Round(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	f := math.Floor(x)
	if x-f >= 0.5 {
		f++
	}
	if f == 0 && math.Signbit(x) {
		return math.Copysign(0, -1)
	}
	return f
}

// Num is String(x) for a JavaScript number.
func Num(x float64) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case math.IsInf(x, 1):
		return "Infinity"
	case math.IsInf(x, -1):
		return "-Infinity"
	case x == 0:
		return "0"
	}
	if x < 0 {
		return "-" + Num(-x)
	}
	if x == math.Trunc(x) && x < 1e21 {
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	// Shortest digits and exponent, then ECMAScript's Number::toString layout.
	e := strconv.FormatFloat(x, 'e', -1, 64)
	mant, expStr, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	k := len(digits)
	exp, _ := strconv.Atoi(expStr)
	n := exp + 1
	switch {
	case k <= n && n <= 21:
		return digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return "0." + strings.Repeat("0", -n) + digits
	}
	sign := "+"
	if n-1 < 0 {
		sign = "-"
	}
	ev := n - 1
	if ev < 0 {
		ev = -ev
	}
	if k == 1 {
		return digits + "e" + sign + strconv.Itoa(ev)
	}
	return digits[:1] + "." + digits[1:] + "e" + sign + strconv.Itoa(ev)
}

// Int is String(n) for an integer.
func Int(n int) string { return strconv.Itoa(n) }

// IsSpace reports whether r is in the set `\s` matches: WhiteSpace and LineTerminator.
func IsSpace(c rune) bool {
	if c < 128 {
		return c == 32 || (c >= 9 && c <= 13)
	}
	return c == 0xa0 || c == 0x1680 || (c >= 0x2000 && c <= 0x200a) || c == 0x2028 || c == 0x2029 ||
		c == 0x202f || c == 0x205f || c == 0x3000 || c == 0xfeff
}

// S is the body of a regexp character class equal to JavaScript's `\s`, for use as
// `[` + S + `]` or `[^` + S + `]`. Go's `\s` leaves out \v and the Unicode spaces.
const S = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// Dot is a class equal to JavaScript's `.` without the s flag.
const Dot = `[^\n\r\x{2028}\x{2029}]`

// Trim is String.prototype.trim.
func Trim(s string) string { return strings.TrimFunc(s, IsSpace) }

// TrimStart is String.prototype.trimStart.
func TrimStart(s string) string { return strings.TrimLeftFunc(s, IsSpace) }

// TrimEnd is String.prototype.trimEnd.
func TrimEnd(s string) string { return strings.TrimRightFunc(s, IsSpace) }

// Len is the length of s in UTF-16 code units, as `.length` counts.
func Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			n++
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r > 0xffff {
			n += 2
		} else {
			n++
		}
		i += size
	}
	return n
}

// ParseFloat is the global parseFloat: the longest decimal prefix after leading
// whitespace, or NaN.
func ParseFloat(s string) float64 {
	s = TrimStart(s)
	if strings.HasPrefix(s, "Infinity") || strings.HasPrefix(s, "+Infinity") {
		return math.Inf(1)
	}
	if strings.HasPrefix(s, "-Infinity") {
		return math.Inf(-1)
	}
	end := decimalPrefix(s)
	if end == 0 {
		return math.NaN()
	}
	v, err := strconv.ParseFloat(s[:end], 64)
	if err != nil {
		// Out of range still parses to ±Inf, as JavaScript does.
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return v
		}
		return math.NaN()
	}
	return v
}

// decimalPrefix returns the length of the StrDecimalLiteral at the start of s, or 0.
func decimalPrefix(s string) int {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		j := i + 1
		frac := 0
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
			frac++
		}
		if digits > 0 || frac > 0 {
			i = j
			digits += frac
		}
	}
	if digits == 0 {
		return 0
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k > j {
			i = k
		}
	}
	return i
}

// Number is Number(s) for a string: the whole trimmed text as a numeric literal,
// 0 for an empty one, NaN otherwise.
func Number(s string) float64 {
	s = Trim(s)
	if s == "" {
		return 0
	}
	switch s {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if len(s) > 2 && s[0] == '0' {
		base := 0
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			var v float64
			for _, c := range s[2:] {
				d := digitVal(c)
				if d < 0 || d >= base {
					return math.NaN()
				}
				v = v*float64(base) + float64(d)
			}
			return v
		}
	}
	if decimalPrefix(s) != len(s) {
		return math.NaN()
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return v
		}
		return math.NaN()
	}
	return v
}

func digitVal(c rune) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return -1
}

// Sign is Math.sign.
func Sign(x float64) float64 {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	}
	return x
}

// Or is `a || b` for numbers: b when a is 0 or NaN.
func Or(a, b float64) float64 {
	if a == 0 || math.IsNaN(a) {
		return b
	}
	return a
}

// Hypot is Math.hypot as V8 computes it: scaled by the largest value, with
// Kahan summation. Go's math.Hypot can differ in the last bit.
func Hypot(values ...float64) float64 {
	max := 0.0
	nan := false
	for _, v := range values {
		a := math.Abs(v)
		if math.IsNaN(a) {
			nan = true
		} else if a > max {
			max = a
		}
	}
	if math.IsInf(max, 1) {
		return max
	}
	if nan {
		return math.NaN()
	}
	if max == 0 {
		return 0
	}
	sum, compensation := 0.0, 0.0
	for _, v := range values {
		n := math.Abs(v) / max
		summand := n*n - compensation
		preliminary := sum + summand
		compensation = (preliminary - sum) - summand
		sum = preliminary
	}
	return math.Sqrt(sum) * max
}

// String is String(v) for a value parsed from YAML or JSON: nil, bool, float64,
// string, []any or map[string]any.
func String(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return Num(t)
	case int:
		return strconv.Itoa(t)
	case string:
		return t
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			if e != nil {
				parts[i] = String(e)
			}
		}
		return strings.Join(parts, ",")
	case map[string]any:
		return "[object Object]"
	}
	return ""
}

// Truthy is JavaScript's truthiness of such a value.
func Truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0 && !math.IsNaN(t)
	case int:
		return t != 0
	case string:
		return t != ""
	}
	return true
}

// ParseIntPrefix is parseInt(s, radix): the leading digits after whitespace and a sign, or NaN.
func ParseIntPrefix(s string, radix int) float64 {
	s = TrimStart(s)
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	if radix == 16 && len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		s = s[2:]
	}
	v := 0.0
	n := 0
	for _, c := range s {
		d := digitVal(c)
		if d < 0 || d >= radix {
			break
		}
		v = v*float64(radix) + float64(d)
		n++
	}
	if n == 0 {
		return math.NaN()
	}
	if neg {
		return -v
	}
	return v
}
