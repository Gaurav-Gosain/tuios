package js

import (
	"math"
	"time"
	"unicode/utf8"
)

// ParseDate is `new Date(s)` for a string, as V8 parses it: the ECMAScript date time
// string format first, and V8's legacy parser for whatever follows or does not fit.
// It returns an invalid date for text neither accepts. This follows V8's
// src/date/dateparser-inl.h.
func ParseDate(s string, loc *time.Location) Date {
	return Date{parseDate(s, loc), loc}
}

const (
	dpNone = math.MinInt32

	tokInvalid = iota
	tokEnd
	tokNumber
	tokSymbol
	tokWhiteSpace
	tokKeyword
	tokUnknown

	kwInvalid = iota
	kwMonth
	kwAmPm
	kwZone
	kwTimeSep
)

type dateToken struct {
	tag    int
	value  int // number, symbol or keyword value
	length int
	kwType int
}

func (t dateToken) isNumber() bool           { return t.tag == tokNumber }
func (t dateToken) isFixed(n int) bool       { return t.tag == tokNumber && t.length == n }
func (t dateToken) isSymbol(c int) bool      { return t.tag == tokSymbol && t.value == c }
func (t dateToken) isSign() bool             { return t.tag == tokSymbol && (t.value == '+' || t.value == '-') }
func (t dateToken) isKeywordType(k int) bool { return t.tag == tokKeyword && t.kwType == k }
func (t dateToken) isKeywordZ() bool {
	return t.tag == tokKeyword && t.kwType == kwZone && t.length == 1 && t.value == 0
}

var keywords = [...]struct {
	word   string
	kwType int
	value  int
}{
	{"jan", kwMonth, 1}, {"feb", kwMonth, 2}, {"mar", kwMonth, 3}, {"apr", kwMonth, 4},
	{"may", kwMonth, 5}, {"jun", kwMonth, 6}, {"jul", kwMonth, 7}, {"aug", kwMonth, 8},
	{"sep", kwMonth, 9}, {"oct", kwMonth, 10}, {"nov", kwMonth, 11}, {"dec", kwMonth, 12},
	{"am", kwAmPm, 0}, {"pm", kwAmPm, 12},
	{"ut", kwZone, 0}, {"utc", kwZone, 0}, {"z", kwZone, 0}, {"gmt", kwZone, 0},
	{"cdt", kwZone, -5}, {"cst", kwZone, -6}, {"edt", kwZone, -4}, {"est", kwZone, -5},
	{"mdt", kwZone, -6}, {"mst", kwZone, -7}, {"pdt", kwZone, -7}, {"pst", kwZone, -8},
	{"t", kwTimeSep, 0},
}

type dateScanner struct {
	s    string
	p    int
	next dateToken
}

func (sc *dateScanner) ch() rune {
	if sc.p >= len(sc.s) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(sc.s[sc.p:])
	return r
}

func (sc *dateScanner) advance() {
	if sc.p < len(sc.s) {
		_, n := utf8.DecodeRuneInString(sc.s[sc.p:])
		sc.p += n
	}
}

func (sc *dateScanner) scan() dateToken {
	if sc.p >= len(sc.s) {
		return dateToken{tag: tokEnd}
	}
	c := sc.ch()
	if c >= '0' && c <= '9' {
		start := sc.p
		for sc.ch() == '0' {
			sc.advance()
		}
		n, i := 0, 0
		for c := sc.ch(); c >= '0' && c <= '9'; c = sc.ch() {
			if i < 9 {
				n = n*10 + int(c-'0')
			}
			i++
			sc.advance()
		}
		return dateToken{tag: tokNumber, value: n, length: sc.p - start}
	}
	switch c {
	case ':', '-', '+', '.', ')':
		sc.advance()
		return dateToken{tag: tokSymbol, value: int(c)}
	}
	if c >= 'A' && !IsSpace(c) {
		var pre [3]rune
		length := 0
		for c := sc.ch(); sc.p < len(sc.s) && c >= 'A' && !IsSpace(c); c = sc.ch() {
			if length < 3 {
				pre[length] = c | 0x20
			}
			if c > 0xffff {
				length += 2
			} else {
				length++
			}
			sc.advance()
		}
		for _, k := range keywords {
			j := 0
			for j < 3 {
				var want rune
				if j < len(k.word) {
					want = rune(k.word[j])
				}
				if pre[j] != want {
					break
				}
				j++
			}
			if j == 3 && (length <= 3 || k.kwType == kwMonth) {
				return dateToken{tag: tokKeyword, kwType: k.kwType, value: k.value, length: length}
			}
		}
		return dateToken{tag: tokKeyword, kwType: kwInvalid, length: length}
	}
	if IsSpace(c) {
		start := sc.p
		for sc.p < len(sc.s) && IsSpace(sc.ch()) {
			sc.advance()
		}
		return dateToken{tag: tokWhiteSpace, length: sc.p - start}
	}
	if c == '(' {
		balance := 0
		for {
			switch sc.ch() {
			case ')':
				balance--
			case '(':
				balance++
			}
			sc.advance()
			if balance <= 0 || sc.p >= len(sc.s) {
				return dateToken{tag: tokUnknown}
			}
		}
	}
	sc.advance()
	return dateToken{tag: tokUnknown}
}

func (sc *dateScanner) Next() dateToken {
	t := sc.next
	sc.next = sc.scan()
	return t
}

func (sc *dateScanner) Peek() dateToken { return sc.next }

func (sc *dateScanner) SkipSymbol(c int) bool {
	if sc.next.isSymbol(c) {
		sc.Next()
		return true
	}
	return false
}

type dayComposer struct {
	comp       [3]int
	index      int
	namedMonth int
	iso        bool
}

func (d *dayComposer) Add(n int) bool {
	if d.index < 3 {
		d.comp[d.index] = n
		d.index++
		return true
	}
	return false
}

func isMonth(x int) bool  { return x >= 1 && x <= 12 }
func isDay(x int) bool    { return x >= 1 && x <= 31 }
func isHour(x int) bool   { return x >= 0 && x <= 23 }
func isMinute(x int) bool { return x >= 0 && x <= 59 }
func isHour12(x int) bool { return x >= 0 && x <= 12 }
func isMs(x int) bool     { return x >= 0 && x <= 999 }

func (d *dayComposer) Write() (year, month, day int, ok bool) {
	if d.index < 1 {
		return 0, 0, 0, false
	}
	for d.index < 3 {
		d.comp[d.index] = 1
		d.index++
	}
	if d.namedMonth == dpNone {
		if d.iso || (d.index == 3 && !isDay(d.comp[0])) {
			year, month, day = d.comp[0], d.comp[1], d.comp[2]
		} else {
			month, day = d.comp[0], d.comp[1]
			if d.index == 3 {
				year = d.comp[2]
			}
		}
	} else {
		month = d.namedMonth
		if d.index == 1 {
			day = d.comp[0]
		} else if !isDay(d.comp[0]) {
			year, day = d.comp[0], d.comp[1]
		} else {
			day, year = d.comp[0], d.comp[1]
		}
	}
	if !d.iso {
		if year >= 0 && year <= 49 {
			year += 2000
		} else if year >= 50 && year <= 99 {
			year += 1900
		}
	}
	if year < -(1<<30) || year >= 1<<30 || !isMonth(month) || !isDay(day) {
		return 0, 0, 0, false
	}
	return year, month - 1, day, true
}

type timeComposer struct {
	comp       [4]int
	index      int
	hourOffset int
}

func (t *timeComposer) IsEmpty() bool { return t.index == 0 }
func (t *timeComposer) IsExpecting(n int) bool {
	return (t.index == 1 && isMinute(n)) || (t.index == 2 && isMinute(n)) || (t.index == 3 && isMs(n))
}
func (t *timeComposer) Add(n int) bool {
	if t.index < 4 {
		t.comp[t.index] = n
		t.index++
		return true
	}
	return false
}
func (t *timeComposer) AddFinal(n int) bool {
	if !t.Add(n) {
		return false
	}
	for t.index < 4 {
		t.comp[t.index] = 0
		t.index++
	}
	return true
}

func (t *timeComposer) Write() (h, m, s, ms int, ok bool) {
	for t.index < 4 {
		t.comp[t.index] = 0
		t.index++
	}
	h, m, s, ms = t.comp[0], t.comp[1], t.comp[2], t.comp[3]
	if t.hourOffset != dpNone {
		if !isHour12(h) {
			return 0, 0, 0, 0, false
		}
		h = h%12 + t.hourOffset
	}
	if !isHour(h) || !isMinute(m) || !isMinute(s) || !isMs(ms) {
		if h != 24 || m != 0 || s != 0 || ms != 0 {
			return 0, 0, 0, 0, false
		}
	}
	return h, m, s, ms, true
}

type zoneComposer struct {
	sign, hour, minute int
}

func (z *zoneComposer) Set(hours int) {
	z.sign = 1
	if hours < 0 {
		z.sign = -1
	}
	z.hour = hours * z.sign
	z.minute = 0
}
func (z *zoneComposer) IsEmpty() bool { return z.hour == dpNone }
func (z *zoneComposer) IsUTC() bool   { return z.hour == 0 && z.minute == 0 }
func (z *zoneComposer) IsExpecting(n int) bool {
	return z.hour != dpNone && z.minute == dpNone && isMinute(n)
}

// Write gives the offset in seconds, or ok and none when no zone was given.
func (z *zoneComposer) Write() (offset int, none, ok bool) {
	if z.sign == dpNone {
		return 0, true, true
	}
	h, m := z.hour, z.minute
	if h == dpNone {
		h = 0
	}
	if m == dpNone {
		m = 0
	}
	total := uint64(uint32(h))*3600 + uint64(uint32(m))*60
	if total > 1<<30-1 {
		return 0, false, false
	}
	offset = int(total)
	if z.sign < 0 {
		offset = -offset
	}
	return offset, false, true
}

func readMilliseconds(t dateToken) int {
	n, length := t.value, t.length
	if length == 1 {
		n *= 100
	} else if length == 2 {
		n *= 10
	} else if length > 3 {
		if length > 9 {
			length = 9
		}
		factor := 1
		for length > 3 {
			factor *= 10
			length--
		}
		n /= factor
	}
	return n
}

// parseES5 reads the ECMAScript date time string format. It returns the first token it
// does not handle, an end token when it read the whole text, or an invalid token.
func parseES5(sc *dateScanner, day *dayComposer, tm *timeComposer, tz *zoneComposer) dateToken {
	if sc.Peek().isSign() {
		signTok := sc.Next()
		if !sc.Peek().isFixed(6) {
			return signTok
		}
		sign := 1
		if signTok.value == '-' {
			sign = -1
		}
		year := sc.Next().value
		if sign < 0 && year == 0 {
			return signTok
		}
		day.Add(sign * year)
	} else if sc.Peek().isFixed(4) {
		day.Add(sc.Next().value)
	} else {
		return sc.Next()
	}
	if sc.SkipSymbol('-') {
		if !sc.Peek().isFixed(2) || !isMonth(sc.Peek().value) {
			return sc.Next()
		}
		day.Add(sc.Next().value)
		if sc.SkipSymbol('-') {
			if !sc.Peek().isFixed(2) || !isDay(sc.Peek().value) {
				return sc.Next()
			}
			day.Add(sc.Next().value)
		}
	}
	if !sc.Peek().isKeywordType(kwTimeSep) {
		if sc.Peek().tag != tokEnd {
			return sc.Next()
		}
	} else {
		sc.Next()
		if !sc.Peek().isFixed(2) || sc.Peek().value < 0 || sc.Peek().value > 24 {
			return dateToken{tag: tokInvalid}
		}
		hourIs24 := sc.Peek().value == 24
		tm.Add(sc.Next().value)
		if !sc.SkipSymbol(':') {
			return dateToken{tag: tokInvalid}
		}
		if !sc.Peek().isFixed(2) || !isMinute(sc.Peek().value) || (hourIs24 && sc.Peek().value > 0) {
			return dateToken{tag: tokInvalid}
		}
		tm.Add(sc.Next().value)
		if sc.SkipSymbol(':') {
			if !sc.Peek().isFixed(2) || !isMinute(sc.Peek().value) || (hourIs24 && sc.Peek().value > 0) {
				return dateToken{tag: tokInvalid}
			}
			tm.Add(sc.Next().value)
			if sc.SkipSymbol('.') {
				if !sc.Peek().isNumber() || (hourIs24 && sc.Peek().value > 0) {
					return dateToken{tag: tokInvalid}
				}
				tm.Add(readMilliseconds(sc.Next()))
			}
		}
		if sc.Peek().isKeywordZ() {
			sc.Next()
			tz.Set(0)
		} else if sc.Peek().isSign() {
			tz.sign = 1
			if sc.Next().value == '-' {
				tz.sign = -1
			}
			if sc.Peek().isFixed(4) {
				hourmin := sc.Next().value
				hour, min := hourmin/100, hourmin%100
				if !isHour(hour) || !isMinute(min) {
					return dateToken{tag: tokInvalid}
				}
				tz.hour, tz.minute = hour, min
			} else {
				if !sc.Peek().isFixed(2) || !isHour(sc.Peek().value) {
					return dateToken{tag: tokInvalid}
				}
				tz.hour = sc.Next().value
				if !sc.SkipSymbol(':') {
					return dateToken{tag: tokInvalid}
				}
				if !sc.Peek().isFixed(2) || !isMinute(sc.Peek().value) {
					return dateToken{tag: tokInvalid}
				}
				tz.minute = sc.Next().value
			}
		}
		if sc.Peek().tag != tokEnd {
			return dateToken{tag: tokInvalid}
		}
	}
	// Date-only forms are UTC and date-time forms local, when no offset is given.
	if tz.IsEmpty() && tm.IsEmpty() {
		tz.Set(0)
	}
	day.iso = true
	return dateToken{tag: tokEnd}
}

func parseDate(s string, loc *time.Location) float64 {
	sc := &dateScanner{s: s}
	sc.next = sc.scan()
	day := &dayComposer{namedMonth: dpNone}
	tm := &timeComposer{hourOffset: dpNone}
	tz := &zoneComposer{sign: dpNone, hour: dpNone, minute: dpNone}

	token := parseES5(sc, day, tm, tz)
	if token.tag == tokInvalid {
		return math.NaN()
	}
	hasReadNumber := day.index != 0
	for ; token.tag != tokEnd; token = sc.Next() {
		switch {
		case token.isNumber():
			hasReadNumber = true
			n := token.value
			if sc.SkipSymbol(':') {
				if sc.SkipSymbol(':') {
					if !tm.IsEmpty() {
						return math.NaN()
					}
					tm.Add(n)
					tm.Add(0)
				} else {
					if !tm.Add(n) {
						return math.NaN()
					}
					if sc.Peek().isSymbol('.') {
						sc.Next()
					}
				}
			} else if sc.SkipSymbol('.') && tm.IsExpecting(n) {
				tm.Add(n)
				if !sc.Peek().isNumber() {
					return math.NaN()
				}
				ms := readMilliseconds(sc.Next())
				if ms < 0 {
					return math.NaN()
				}
				tm.AddFinal(ms)
			} else if tz.IsExpecting(n) {
				tz.minute = n
			} else if tm.IsExpecting(n) {
				tm.AddFinal(n)
				peek := sc.Peek()
				if peek.tag != tokEnd && peek.tag != tokWhiteSpace && !peek.isKeywordZ() && !peek.isSign() {
					return math.NaN()
				}
			} else {
				if !day.Add(n) {
					return math.NaN()
				}
				sc.SkipSymbol('-')
			}
		case token.tag == tokKeyword:
			if token.kwType == kwAmPm && !tm.IsEmpty() {
				tm.hourOffset = token.value
			} else if token.kwType == kwMonth {
				day.namedMonth = token.value
				sc.SkipSymbol('-')
			} else if token.kwType == kwZone && hasReadNumber {
				tz.Set(token.value)
			} else {
				// Garbage words are illegal once a number has been read, and the first
				// number has to be kept apart from them.
				if hasReadNumber || sc.Peek().isNumber() {
					return math.NaN()
				}
			}
		case token.isSign() && (tz.IsUTC() || !tm.IsEmpty()):
			tz.sign = 1
			if token.value == '-' {
				tz.sign = -1
			}
			n, length := 0, 0
			if sc.Peek().isNumber() {
				t := sc.Next()
				n, length = t.value, t.length
			}
			hasReadNumber = true
			if sc.Peek().isSymbol(':') {
				tz.hour, tz.minute = n, dpNone
			} else if length == 1 || length == 2 {
				tz.hour, tz.minute = n, 0
			} else if length == 3 || length == 4 {
				tz.hour, tz.minute = n/100, n%100
			} else {
				return math.NaN()
			}
		case (token.isSign() || token.isSymbol(')')) && hasReadNumber:
			return math.NaN()
		}
	}

	year, month, dd, ok := day.Write()
	if !ok {
		return math.NaN()
	}
	h, m, sec, ms, ok := tm.Write()
	if !ok {
		return math.NaN()
	}
	offset, none, ok := tz.Write()
	if !ok {
		return math.NaN()
	}
	date := makeDate(makeDay(float64(year), float64(month), float64(dd)), makeTime(float64(h), float64(m), float64(sec), float64(ms)))
	if none {
		if date < -maxTime-864000000 || date > maxTime+864000000 {
			return math.NaN()
		}
		date = utc(date, loc)
	} else {
		date -= float64(offset) * 1000
		if date < -maxTime || date > maxTime {
			return math.NaN()
		}
	}
	return timeClip(date)
}
