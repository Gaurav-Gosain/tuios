// Ported from pele src/diagrams/gantt/dates.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// A JavaScript Date is a js.Date, which reads and sets its fields in the local time of
// its location. pele caches the last compiled format in module state; the port keeps
// that cache in a formatCache the caller owns, so that renders can run in parallel.
// DAY and WEEK are axis.go's.

package gantt

import (
	"math"
	"strings"
	"time"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/lazyre"
)

// The parts of dayjs 1.11 that Mermaid's gantt code relies on, with the plugins it loads (custom parse format,
// advanced format, ISO week) and the English locale: strict parsing against a format, formatting, and calendar
// arithmetic, all in local time. Quirks are kept, because a date is accepted only when formatting the parsed
// result gives back the input.

var MONTHS = [12]string{
	"January",
	"February",
	"March",
	"April",
	"May",
	"June",
	"July",
	"August",
	"September",
	"October",
	"November",
	"December",
}
var WEEKDAYS = [7]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

type Unit string

func isValid(date js.Date) bool {
	return date.Valid()
}

func pad(value float64, length int) string {
	s := js.Num(value)
	if len(s) >= length {
		return s
	}
	return strings.Repeat("0", length-len(s)) + s
}

func ordinal(n float64) string {
	s := [4]string{"th", "st", "nd", "rd"}
	v := math.Mod(n, 100)
	suffix := s[0]
	if i := math.Mod(v-20, 10); i >= 0 && i <= 3 {
		suffix = s[int(i)]
	} else if v >= 0 && v <= 3 {
		suffix = s[int(v)]
	}
	return js.Num(n) + suffix
}

func utcOffset(date js.Date) float64 {
	offset := -js.Round(date.TimezoneOffset()/15) * 15
	if offset == 0 {
		return 0
	}
	return offset
}

func zone(date js.Date) string {
	offset := utcOffset(date)
	minutes := math.Abs(offset)
	sign := "-"
	if offset >= 0 {
		sign = "+"
	}
	return sign + pad(math.Floor(minutes/60), 2) + ":" + pad(math.Mod(minutes, 60), 2)
}

func isoWeekday(date js.Date) float64 {
	return js.Or(date.Day(), 7)
}

func addDays(date js.Date, days float64) js.Date {
	next := date
	next.SetDate(next.Date() + days)
	return next
}

func daysInMonth(date js.Date) float64 {
	last := js.LocalDate(date.Loc, date.FullYear(), date.Month()+1, 0, 0, 0, 0, 0)
	last.SetHours(23, 59, 59, 999)
	return last.Date()
}

// Sets the month or the year, keeping the day of the month unless the new month is shorter.
func setCalendar(date js.Date, month bool, value float64) js.Date {
	next := date
	next.SetDate(1)
	if month {
		next.SetMonth(value, math.NaN())
	} else {
		next.SetFullYear(value)
	}
	next.SetDate(math.Min(date.Date(), daysInMonth(next)))
	return next
}

func add(date js.Date, amount float64, unit Unit) js.Date {
	switch unit {
	case "M":
		return setCalendar(date, true, date.Month()+amount)
	case "y":
		return setCalendar(date, false, date.FullYear()+amount)
	case "d":
		return addDays(date, js.Round(amount))
	case "w":
		return addDays(date, js.Round(7*amount))
	case "h":
		return js.NewDate(date.T+float64(amount*3600000), date.Loc)
	case "m":
		return js.NewDate(date.T+float64(amount*60000), date.Loc)
	case "s":
		return js.NewDate(date.T+float64(amount*1000), date.Loc)
	default:
		return js.NewDate(date.T+amount, date.Loc)
	}
}

func weekThursday(date js.Date) js.Date {
	return addDays(date, 4-isoWeekday(date))
}

func isoWeek(date js.Date) float64 {
	thursday := weekThursday(date)
	jan1 := js.LocalDate(date.Loc, thursday.FullYear(), 0, 1, 0, 0, 0, 0)
	weekday := isoWeekday(jan1)
	extra := 0.0
	if weekday > 4 {
		extra = 7
	}
	first := addDays(jan1, 4-weekday+extra)
	zoneDelta := float64((utcOffset(first) - utcOffset(thursday)) * 60000)
	weeks := (thursday.T - first.T - zoneDelta) / WEEK
	if weeks < 0 {
		return js.Or(math.Ceil(weeks), 0) + 1
	}
	return math.Floor(weeks) + 1
}

var (
	RE_ADVANCED = lazyre.New(`\[([^\]]+)\]|Q|wo|ww|w|WW|W|zzz|z|gggg|GGGG|Do|X|x|k{1,2}|S`)
	RE_FORMAT   = lazyre.New(`\[([^\]]+)\]|YYYY|YY|M{1,4}|D{1,2}|d{1,4}|H{1,2}|h{1,2}|a|A|m{1,2}|s{1,2}|Z{1,2}|SSS`)
)

const DEFAULT_FORMAT = "YYYY-MM-DDTHH:mm:ssZ"

// Returns false for the week-of-year and time zone name tokens, which need dayjs plugins Mermaid
// does not load and so fail there.
func format(date js.Date, template string) (string, bool) {
	if !isValid(date) {
		return "Invalid Date", true
	}
	str := template
	if str == "" {
		str = DEFAULT_FORMAT
	}
	if str == "YYYY-MM-DD" {
		return isoDate(date), true
	}
	unsupported := false
	advanced := RE_ADVANCED.ReplaceAllStringFunc(str, func(match string) string {
		switch match {
		case "Q":
			return js.Num(math.Ceil((date.Month() + 1) / 3))
		case "Do":
			return "[" + ordinal(date.Date()) + "]"
		case "GGGG":
			return js.Num(weekThursday(date).FullYear())
		case "W":
			return js.Num(isoWeek(date))
		case "WW":
			return pad(isoWeek(date), 2)
		case "k":
			return js.Num(js.Or(date.Hours(), 24))
		case "kk":
			return pad(js.Or(date.Hours(), 24), 2)
		case "X":
			return js.Num(math.Floor(date.T / 1000))
		case "x":
			return js.Num(date.T)
		case "gggg", "wo", "w", "ww", "z", "zzz":
			unsupported = true
			return match
		default:
			return match
		}
	})
	if unsupported {
		return "", false
	}
	return RE_FORMAT.ReplaceAllStringFunc(advanced, func(match string) string {
		return formatToken(date, match)
	}), true
}

func formatToken(date js.Date, match string) string {
	if match[0] == '[' {
		return match[1 : len(match)-1]
	}
	hours := date.Hours()
	switch match {
	case "YY":
		s := js.Num(date.FullYear())
		return s[max(0, len(s)-2):]
	case "YYYY":
		return pad(date.FullYear(), 4)
	case "M":
		return js.Num(date.Month() + 1)
	case "MM":
		return pad(date.Month()+1, 2)
	case "MMM":
		return MONTHS[int(date.Month())][:3]
	case "MMMM":
		return MONTHS[int(date.Month())]
	case "D":
		return js.Num(date.Date())
	case "DD":
		return pad(date.Date(), 2)
	case "d":
		return js.Num(date.Day())
	case "dd":
		return WEEKDAYS[int(date.Day())][:2]
	case "ddd":
		return WEEKDAYS[int(date.Day())][:3]
	case "dddd":
		return WEEKDAYS[int(date.Day())]
	case "H":
		return js.Num(hours)
	case "HH":
		return pad(hours, 2)
	case "h":
		return js.Num(js.Or(math.Mod(hours, 12), 12))
	case "hh":
		return pad(js.Or(math.Mod(hours, 12), 12), 2)
	case "a":
		if hours < 12 {
			return "am"
		}
		return "pm"
	case "A":
		if hours < 12 {
			return "AM"
		}
		return "PM"
	case "m":
		return js.Num(date.Minutes())
	case "mm":
		return pad(date.Minutes(), 2)
	case "s":
		return js.Num(date.Seconds())
	case "ss":
		return pad(date.Seconds(), 2)
	case "SSS":
		return pad(date.Milliseconds(), 3)
	case "Z":
		return zone(date)
	default:
		return strings.Replace(zone(date), ":", "", 1)
	}
}

func isoDate(date js.Date) string {
	year := date.FullYear()
	month := date.Month() + 1
	day := date.Date()
	y := pad(year, 4)
	if year >= 1000 && year <= 9999 {
		y = js.Num(year)
	}
	m := "-"
	if month < 10 {
		m = "-0"
	}
	d := "-"
	if day < 10 {
		d = "-0"
	}
	return y + m + js.Num(month) + d + js.Num(day)
}

// Fields: NaN is a field the input did not give.
type Fields struct {
	year         float64
	month        float64
	day          float64
	dayGiven     bool
	hours        float64
	minutes      float64
	seconds      float64
	milliseconds float64
	week         float64
	offset       float64
	// -1 when not given.
	afternoon int
}

// Readers, by the token they read.
const (
	rNone = iota
	rAfternoonUpper
	rAfternoonLower
	rQuarter
	rTenths
	rHundredths
	rMilliseconds
	rSeconds
	rMinutes
	rHours
	rDay
	rDo
	rWeek
	rMonth
	rMonthShort
	rMonthLong
	rYear
	rYear2
	rOffset
)

// Step is a literal, or a pattern and the reader of what it matches.
type Step struct {
	literal string
	re      *lazyre.Regexp
	read    int
}

var (
	RE_TOKENS    = lazyre.New(`(\[[^\[]*\])|([-_:/.,()` + js.S + `]+)|(A|a|Q|YYYY|YY?|ww?|MM?M?M?|Do|DD?|hh?|HH?|mm?|ss?|S{1,3}|z|ZZ?)`)
	RE_LITERAL   = lazyre.New(`\[[^\]]+\]`)
	MATCH_1      = lazyre.New(`\d`)
	MATCH_2      = lazyre.New(`\d\d`)
	MATCH_3      = lazyre.New(`\d{3}`)
	MATCH_4      = lazyre.New(`\d{4}`)
	MATCH_1_TO_2 = lazyre.New(`\d\d?`)
	MATCH_SIGNED = lazyre.New(`[+-]?\d+`)
	MATCH_OFFSET = lazyre.New(`[+-]\d\d:?(\d\d)?|Z`)
	MATCH_WORD   = lazyre.New(`\d*[^-_:/,()` + js.S + `\d]+`)
	RE_DIGIT_RUN = lazyre.New(`\d+`)
)

// READERS is READERS.get(token): nil when no reader takes the token.
func READERS(token string) (*lazyre.Regexp, int) {
	switch token {
	case "A":
		return MATCH_WORD, rAfternoonUpper
	case "a":
		return MATCH_WORD, rAfternoonLower
	case "Q":
		return MATCH_1, rQuarter
	case "S":
		return MATCH_1, rTenths
	case "SS":
		return MATCH_2, rHundredths
	case "SSS":
		return MATCH_3, rMilliseconds
	case "s", "ss":
		return MATCH_1_TO_2, rSeconds
	case "m", "mm":
		return MATCH_1_TO_2, rMinutes
	case "H", "h", "HH", "hh":
		return MATCH_1_TO_2, rHours
	case "D":
		return MATCH_1_TO_2, rDay
	case "DD":
		return MATCH_2, rDay
	case "Do":
		return MATCH_WORD, rDo
	case "w":
		return MATCH_1_TO_2, rWeek
	case "ww":
		return MATCH_2, rWeek
	case "M":
		return MATCH_1_TO_2, rMonth
	case "MM":
		return MATCH_2, rMonth
	case "MMM":
		return MATCH_WORD, rMonthShort
	case "MMMM":
		return MATCH_WORD, rMonthLong
	case "Y":
		return MATCH_SIGNED, rYear
	case "YY":
		return MATCH_2, rYear2
	case "YYYY":
		return MATCH_4, rYear
	case "Z", "ZZ":
		return MATCH_OFFSET, rOffset
	}
	return nil, rNone
}

func readOffset(fields *Fields, input string) bool {
	if input == "Z" {
		fields.offset = 0
		return true
	}
	// input is a sign, two digits, maybe a colon, and maybe two digits more.
	minutes := float64(js.Number(input[1:3]) * 60)
	if rest := strings.TrimPrefix(input[3:], ":"); len(rest) == 2 {
		minutes += js.Number(rest)
	}
	if minutes == 0 {
		fields.offset = 0
	} else if input[0] == '+' {
		fields.offset = -minutes
	} else {
		fields.offset = minutes
	}
	return true
}

func monthName(fields *Fields, input string, long bool) bool {
	index := 0
	for i, m := range MONTHS {
		name := m
		if !long {
			name = m[:3]
		}
		if name == input {
			index = i + 1
			break
		}
	}
	if index < 1 {
		return false
	}
	fields.month = float64(index)
	return true
}

func read(f *Fields, kind int, input string) bool {
	switch kind {
	case rAfternoonUpper:
		f.afternoon = boolInt(input == "PM")
	case rAfternoonLower:
		f.afternoon = boolInt(input == "pm")
	case rQuarter:
		f.month = float64((js.Number(input)-1)*3) + 1
	case rTenths:
		f.milliseconds = js.Number(input) * 100
	case rHundredths:
		f.milliseconds = js.Number(input) * 10
	case rMilliseconds:
		f.milliseconds = js.Number(input)
	case rSeconds:
		f.seconds = js.Number(input)
	case rMinutes:
		f.minutes = js.Number(input)
	case rHours:
		f.hours = js.Number(input)
	case rDay:
		f.day = js.Number(input)
	case rDo:
		digits := RE_DIGIT_RUN.FindString(input)
		if digits == "" {
			return false
		}
		// dayjs keeps the digits as a string here, so even "0" counts as a day having been given.
		f.day = js.Number(digits)
		f.dayGiven = true
		for i := 1; i <= 31; i++ {
			if ordinal(float64(i)) == input {
				f.day = float64(i)
			}
		}
	case rWeek:
		f.week = js.Number(input)
	case rMonth:
		f.month = js.Number(input)
	case rMonthShort:
		return monthName(f, input, false)
	case rMonthLong:
		return monthName(f, input, true)
	case rYear:
		f.year = js.Number(input)
	case rYear2:
		n := js.Number(input)
		if n > 68 {
			f.year = n + 1900
		} else {
			f.year = n + 2000
		}
	case rOffset:
		return readOffset(f, input)
	}
	return true
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// formatCache is pele's compiledFormat and compiledSteps.
type formatCache struct {
	compiledFormat string
	compiled       bool
	compiledSteps  []Step
}

var reLocalized = lazyre.New(`[Ll]`)

func (c *formatCache) compile(template string) []Step {
	if c.compiled && template == c.compiledFormat {
		return c.compiledSteps
	}
	c.compiled = true
	c.compiledFormat = template
	c.compiledSteps = nil
	// dayjs expands localized tokens through a locale table the English locale does not have, and fails.
	if reLocalized.MatchString(RE_LITERAL.ReplaceAllString(template, "")) {
		return nil
	}
	tokens := RE_TOKENS.FindAllString(template, -1)
	if tokens == nil {
		return nil
	}
	steps := make([]Step, len(tokens))
	for i, token := range tokens {
		if re, kind := READERS(token); re != nil {
			steps[i] = Step{re: re, read: kind}
		} else {
			steps[i] = Step{literal: strings.TrimSuffix(strings.TrimPrefix(token, "["), "]")}
		}
	}
	c.compiledSteps = steps
	return steps
}

func truthy(x float64) bool { return x != 0 && !math.IsNaN(x) }

func loose(input, template string, now js.Date, cache *formatCache) (js.Date, bool) {
	if template == "x" || template == "X" {
		scale := 1.0
		if template == "X" {
			scale = 1000
		}
		return js.NewDate(scale*js.Number(input), now.Loc), true
	}
	steps := cache.compile(template)
	if steps == nil {
		return js.Date{}, false
	}
	nan := math.NaN()
	f := Fields{nan, nan, nan, false, nan, nan, nan, nan, nan, nan, -1}
	rest := input
	start := 0
	for _, step := range steps {
		if step.re == nil {
			start += js.Len(step.literal)
			continue
		}
		match := step.re.FindString(sliceFrom(rest, start))
		if match == "" || !read(&f, step.read, match) {
			return js.Date{}, false
		}
		// dayjs drops the first occurrence of the matched text, wherever it is, and does not move `start`.
		rest = strings.Replace(rest, match, "", 1)
	}
	if f.afternoon != -1 && !math.IsNaN(f.hours) {
		if f.afternoon == 1 {
			if f.hours < 12 {
				f.hours += 12
			}
		} else if f.hours == 12 {
			f.hours = 0
		}
	}
	var day float64
	if f.dayGiven || truthy(f.day) {
		day = f.day
	} else if !truthy(f.year) && !truthy(f.month) {
		day = now.Date()
	} else {
		day = 1
	}
	year := f.year
	if !truthy(year) {
		year = now.FullYear()
	}
	month := 0.0
	if !(truthy(f.year) && !truthy(f.month)) {
		if f.month > 0 {
			month = f.month - 1
		} else {
			month = now.Month()
		}
	}
	or0 := func(x float64) float64 {
		if truthy(x) {
			return x
		}
		return 0
	}
	hours := or0(f.hours)
	minutes := or0(f.minutes)
	seconds := or0(f.seconds)
	ms := or0(f.milliseconds)
	if !math.IsNaN(f.offset) {
		return js.NewDate(js.DateUTC(year, month, day, hours, minutes, seconds, ms+float64(f.offset*60000)), now.Loc), true
	}
	// Applying a week number needs a dayjs plugin Mermaid does not load.
	if truthy(f.week) {
		return js.Date{}, false
	}
	return js.LocalDate(now.Loc, year, month, day, hours, minutes, seconds, ms), true
}

// Parses the way `dayjs(input, format, true)` does. `now` supplies the fields the format leaves out.
func parseStrict(input, template string, now js.Date, cache *formatCache) (js.Date, bool) {
	date, ok := loose(input, template, now, cache)
	if !ok || !isValid(date) {
		return js.Date{}, false
	}
	if out, ok := format(date, template); !ok || out != input {
		return js.Date{}, false
	}
	return date, true
}

// location is the zone dates are read in: the host's, as JavaScript reads the process's.
func location(loc *time.Location) *time.Location {
	if loc == nil {
		return time.Local
	}
	return loc
}
