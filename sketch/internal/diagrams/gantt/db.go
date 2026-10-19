// Ported from pele src/diagrams/gantt/db.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
//
// A Date that pele leaves undefined is a nil *js.Date. getTasks takes the location dates
// are read in besides the time, as pele reads them in the process's time zone.

package gantt

import (
	"math"
	"strings"
	"time"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
	"tuios.dev/sketch/internal/util"
)

type GanttDbOptions struct {
	warn func(message string)
}

// RE_NUMERIC_FORMAT: /^[YMDHhmsSZXx\-/.:, T]*$/.
func numericFormat(s string) bool {
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune("YMDHhmsSZXx-/.:, T", rune(s[i])) {
			return false
		}
	}
	return true
}

// RE_TAG: /^\s*(active|done|crit|milestone|vert)\s*$/. Returns the tag, or "".
func matchTag(s string) string {
	switch t := js.Trim(s); t {
	case "active", "done", "crit", "milestone", "vert":
		return t
	}
	return ""
}

func isRefChar(c byte) bool {
	return util.IsWord(rune(c)) || c == '-' || c == ' '
}

// RE_AFTER and RE_UNTIL: /^after\s+([\d\w- ]+)/. Returns the group, or false.
func matchRef(str, keyword string) (string, bool) {
	if !strings.HasPrefix(str, keyword) {
		return "", false
	}
	q0 := len(keyword)
	q1 := q0
	for q1 < len(str) && util.IsSpace(codeAt(str, q1)) {
		q1 += sizeAt(str, q1)
	}
	if q1 == q0 {
		return "", false
	}
	e := q1
	for e < len(str) && isRefChar(str[e]) {
		e++
	}
	if e > q1 {
		return str[q1:e], true
	}
	// The group needs a character: `\s+` gives back the last space it took that leaves it one.
	for k := q1 - 1; k > q0; k-- {
		if str[k] == ' ' {
			return " ", true
		}
	}
	return "", false
}

// RE_DURATION: /^(\d+(?:\.\d+)?)([Mdhmswy]|ms)$/. Returns the number and the unit, or false.
func matchDuration(s string) (string, Unit, bool) {
	q := 0
	for q < len(s) && util.IsDigit(rune(s[q])) {
		q++
	}
	if q == 0 {
		return "", "", false
	}
	if q+1 < len(s) && s[q] == '.' && util.IsDigit(rune(s[q+1])) {
		q++
		for q < len(s) && util.IsDigit(rune(s[q])) {
			q++
		}
	}
	unit := s[q:]
	switch unit {
	case "M", "d", "h", "m", "s", "w", "y", "ms":
		return s[:q], Unit(unit), true
	}
	return "", "", false
}

// RE_DIGITS: /^\d+$/.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !util.IsDigit(rune(s[i])) {
			return false
		}
	}
	return true
}

// DAY_NAMES.has(name).
func isDayName(name string) bool {
	for _, day := range WEEKDAYS {
		if strings.ToLower(day) == name {
			return true
		}
	}
	return false
}

// Scheduling walks tasks day by day and parses dates with a format the diagram supplies. This bounds the
// total, in rough character operations, so a hostile diagram fails instead of hanging.
const (
	WORK_LIMIT  = 4e8
	DAY_STEP    = 400
	DATED_STEP  = 200
	FORMAT_STEP = 400
)

// The one place the current time is read.
func clock(now time.Time) float64 {
	if now.IsZero() {
		return float64(time.Now().UnixMilli())
	}
	return float64(now.UnixMilli())
}

func parseDuration(str string) (float64, Unit) {
	number, unit, ok := matchDuration(js.Trim(str))
	if !ok {
		return math.NaN(), "ms"
	}
	return js.ParseFloat(number), unit
}

// RE_TOKEN_SPLIT: /[\s,]+/.
func isTokenSplit(r rune) bool { return r == ',' || js.IsSpace(r) }

func mergeTokens(existing []string, txt string) []string {
	merged := append([]string(nil), existing...)
	for _, token := range strings.FieldsFunc(strings.ToLower(txt), isTokenSplit) {
		if !contains(merged, token) {
			merged = append(merged, token)
		}
	}
	return merged
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func ptr(d js.Date) *js.Date { return &d }

// val is +date, NaN for undefined.
func val(d *js.Date) float64 {
	if d == nil {
		return math.NaN()
	}
	return d.T
}

type GanttDb struct {
	title             string
	accTitle          string
	accDescr          string
	dateFormat        string
	axisFormat        string
	tickInterval      string
	todayMarker       string
	includes          []string
	excludes          []string
	inclusiveEndDates bool
	topAxis           bool
	displayMode       string
	// "" when the chart names no weekday.
	weekday  string
	weekend  string
	sections []string
	links    map[string]string
	tasks    []*GanttTask

	byId           map[string]*GanttTask
	currentSection string
	taskCount      int
	lastOrder      int
	lastTaskId     *string
	options        GanttDbOptions
	now            float64
	loc            *time.Location
	work           float64
	includeSet     map[string]bool
	excludeSet     map[string]bool
	excludedDays   [7]bool
	datedTokens    bool
	cache          formatCache
}

func NewGanttDb(options GanttDbOptions) *GanttDb {
	return &GanttDb{weekend: "saturday", links: map[string]string{}, byId: map[string]*GanttTask{}, options: options}
}

func (db *GanttDb) setDateFormat(txt string) {
	db.dateFormat = txt
}

func (db *GanttDb) enableInclusiveEndDates() {
	db.inclusiveEndDates = true
}

// Mermaid's grammar calls a method its database does not define, so `topAxis` crashes there.
func (db *GanttDb) TopAxis() {
	db.topAxis = true
}

func (db *GanttDb) setAxisFormat(txt string) {
	db.axisFormat = txt
}

func (db *GanttDb) setTickInterval(txt string) {
	db.tickInterval = txt
}

func (db *GanttDb) setTodayMarker(txt string) {
	db.todayMarker = txt
}

func (db *GanttDb) setIncludes(txt string) {
	db.includes = mergeTokens(db.includes, txt)
}

func (db *GanttDb) setExcludes(txt string) {
	db.excludes = mergeTokens(db.excludes, txt)
}

func (db *GanttDb) setWeekday(txt string) {
	db.weekday = txt
}

func (db *GanttDb) setWeekend(startDay string) {
	db.weekend = startDay
}

func (db *GanttDb) setDiagramTitle(txt string) {
	db.title = txt
}

func (db *GanttDb) setAccTitle(txt string) {
	db.accTitle = js.TrimStart(txt)
}

func (db *GanttDb) setAccDescription(txt string) {
	db.accDescr = reNewlineSpace.ReplaceAllString(txt, "\n")
}

func (db *GanttDb) addSection(txt string) {
	db.currentSection = txt
	db.sections = append(db.sections, txt)
}

func (db *GanttDb) addTask(descr, dataStr string) {
	data := strings.Split(strings.TrimPrefix(dataStr, ":"), ",")
	task := &GanttTask{
		task:       descr,
		section:    db.currentSection,
		order:      -1,
		classes:    []string{},
		prevTaskId: db.lastTaskId,
		raw:        rawTask{data: dataStr},
	}

	for len(data) > 0 {
		tag := matchTag(data[0])
		if tag == "" {
			break
		}
		switch tag {
		case "active":
			task.active = true
		case "done":
			task.done = true
		case "crit":
			task.crit = true
		case "milestone":
			task.milestone = true
		case "vert":
			task.vert = true
		}
		data = data[1:]
	}
	// Mermaid fails with a TypeError on both of these.
	if len(data) == 0 {
		types.Throw(`Task "`+js.Trim(descr)+`" needs a duration or an end date.`, types.CodeSemantic, types.ErrorInfo{Type: "gantt"})
	}
	if len(data) > 3 {
		types.Throw(`Task "`+js.Trim(descr)+`" has too many fields. A task takes an id, a start, and an end or duration.`,
			types.CodeSemantic, types.ErrorInfo{Type: "gantt"})
	}
	for i := range data {
		data[i] = js.Trim(data[i])
	}

	if len(data) == 3 {
		task.id = data[0]
	} else {
		db.taskCount++
		task.id = "task" + js.Int(db.taskCount)
	}
	if len(data) > 1 {
		task.raw.start = &data[len(data)-2]
	}
	task.raw.end = data[len(data)-1]

	if !task.vert {
		task.order = db.lastOrder
		db.lastOrder++
	}
	db.tasks = append(db.tasks, task)
	id := task.id
	db.lastTaskId = &id
	db.byId[task.id] = task
}

func (db *GanttDb) setLink(ids, link string) {
	href := util.SafeUrl(link)
	for _, id := range strings.Split(ids, ",") {
		if _, ok := db.byId[id]; ok {
			db.links[id] = href
		}
	}
	db.setClass(ids, "clickable")
}

// Callbacks are recognized and never run; the task is only marked clickable, as in Mermaid.
func (db *GanttDb) setClickEvent(ids, _functionName string, _functionArgs *string) {
	db.setClass(ids, "clickable")
}

func (db *GanttDb) setClass(ids, className string) {
	for _, id := range strings.Split(ids, ",") {
		if task := db.byId[id]; task != nil {
			task.classes = append(task.classes, className)
		}
	}
}

// Resolves every task's start and end, from scratch on each call. A task may refer to tasks defined
// after it, so, like Mermaid, this makes up to eleven passes, each of which schedules every task again.
func (db *GanttDb) getTasks(now float64, loc *time.Location) []*GanttTask {
	db.now = now
	db.loc = loc
	db.work = 0
	db.includeSet = map[string]bool{}
	for _, token := range db.includes {
		db.includeSet[token] = true
	}
	db.excludeSet = map[string]bool{}
	for _, token := range db.excludes {
		db.excludeSet[token] = true
	}
	weekendStart := math.NaN()
	if db.weekend == "friday" {
		weekendStart = 5
	} else if db.weekend == "saturday" {
		weekendStart = 6
	}
	weekends := db.excludeSet["weekends"]
	for day, name := range WEEKDAYS {
		iso := float64(day)
		if day == 0 {
			iso = 7
		}
		db.excludedDays[day] = (weekends && (iso == weekendStart || iso == weekendStart+1)) || db.excludeSet[strings.ToLower(name)]
	}
	// A date written with only these tokens and separators can never read as a weekday name, so when
	// nothing else is excluded or included there is no need to format each day.
	db.datedTokens = len(db.includes) > 0 || !numericFormat(js.Trim(db.dateFormat))
	for _, token := range db.excludes {
		if token != "weekends" && !isDayName(token) {
			db.datedTokens = true
		}
	}

	for _, task := range db.tasks {
		task.startTime, task.endTime = nil, nil
		task.renderEndTime = nil
		task.processed, task.manualEndTime = false, false
	}

	allProcessed := db.compileTasks()
	for i := 0; !allProcessed && i < 10; i++ {
		allProcessed = db.compileTasks()
	}
	return db.tasks
}

// Whether a day is excluded. A day matches a token by its weekday name, by its date as YYYY-MM-DD,
// or by its date written in the chart's own format.
func (db *GanttDb) isInvalidDate(date js.Date) bool {
	weekday := false
	if d := date.Day(); d >= 0 && d < 7 {
		weekday = db.excludedDays[int(d)]
	}
	if !db.datedTokens {
		return weekday
	}
	template := js.Trim(db.dateFormat)
	dateOnly := isoDate(date)
	formatted, hasFormatted := dateOnly, true
	db.spend(DATED_STEP)
	if template != "YYYY-MM-DD" {
		db.spend(FORMAT_STEP + float64(js.Len(template))*8)
		formatted, hasFormatted = format(date, template)
	}
	listed := func(set map[string]bool) bool { return set[dateOnly] || (hasFormatted && set[formatted]) }
	if listed(db.includeSet) {
		return false
	}
	return weekday || listed(db.excludeSet)
}

func (db *GanttDb) spend(units float64) {
	db.work += units
	if db.work > WORK_LIMIT {
		types.Throw("The gantt chart takes too much work to schedule.", types.CodeLimit, types.ErrorInfo{Type: "gantt"})
	}
}

func (db *GanttDb) parse(str, template string) (js.Date, bool) {
	db.spend(float64(js.Len(template)+1) * float64(js.Len(str)+1))
	return parseStrict(str, template, js.NewDate(db.now, db.loc), &db.cache)
}

func (db *GanttDb) today() js.Date {
	today := js.NewDate(db.now, db.loc)
	today.SetHours(0, 0, 0, 0)
	return today
}

func (db *GanttDb) warn(message func() string) {
	if db.options.warn != nil {
		db.options.warn(message())
	}
}

func (db *GanttDb) compileTasks() bool {
	allProcessed := true
	for _, task := range db.tasks {
		db.compileTask(task)
		allProcessed = allProcessed && task.processed
	}
	return allProcessed
}

func (db *GanttDb) compileTask(task *GanttTask) {
	start := task.raw.start
	if start == nil {
		// Mermaid fails with a TypeError when the first task has no start. It starts today here.
		var previous *GanttTask
		if task.prevTaskId != nil {
			previous = db.byId[*task.prevTaskId]
		}
		if previous != nil {
			task.startTime = previous.endTime
		} else {
			task.startTime = ptr(db.today())
		}
	} else {
		if startTime := db.getStartDate(*start); startTime != nil {
			task.startTime = startTime
		}
	}
	if task.startTime == nil {
		return
	}
	task.endTime = db.getEndDate(*task.startTime, task.raw.end)
	if task.endTime == nil {
		return
	}
	task.processed = true
	_, manual := db.parse(task.raw.end, "YYYY-MM-DD")
	task.manualEndTime = manual
	db.checkTaskDates(task)
}

func (db *GanttDb) references(keyword, list string) []*GanttTask {
	var found []*GanttTask
	var unknown []string
	for _, id := range strings.Split(list, " ") {
		if id == "" {
			continue
		}
		if task := db.byId[id]; task != nil {
			found = append(found, task)
		} else {
			unknown = append(unknown, id)
		}
	}
	if unknown != nil {
		ids := unknown
		db.warn(func() string {
			return `Gantt: "` + keyword + `" refers to task ids that do not exist: ` + strings.Join(ids, ", ") + `.`
		})
	}
	return found
}

func (db *GanttDb) getStartDate(raw string) *js.Date {
	str := js.Trim(raw)
	template := js.Trim(db.dateFormat)

	if (template == "x" || template == "X") && allDigits(str) {
		return ptr(db.checked(js.NewDate(js.Number(str), db.loc), str))
	}

	if after, ok := matchRef(str, "after"); ok {
		tasks := db.references("after", after)
		var latest *GanttTask
		for _, task := range tasks {
			if latest == nil || val(task.endTime) > val(latest.endTime) {
				latest = task
			}
		}
		if latest != nil {
			return latest.endTime
		}
		return ptr(db.today())
	}

	if parsed, ok := db.parse(str, template); ok {
		return &parsed
	}

	// What does not fit the format gets a second chance with the JavaScript date parser, as in Mermaid.
	date := js.ParseDate(str, db.loc)
	if !isValid(date) || date.FullYear() < -10000 || date.FullYear() > 10000 {
		types.Throw("Invalid date:"+str, types.CodeSemantic, types.ErrorInfo{Type: "gantt"})
	}
	return &date
}

func (db *GanttDb) checked(date js.Date, str string) js.Date {
	if !isValid(date) {
		types.Throw("Invalid date:"+str, types.CodeSemantic, types.ErrorInfo{Type: "gantt"})
	}
	return date
}

func (db *GanttDb) getEndDate(startTime js.Date, raw string) *js.Date {
	str := js.Trim(raw)

	if until, ok := matchRef(str, "until"); ok {
		tasks := db.references("until", until)
		var earliest *GanttTask
		for _, task := range tasks {
			if earliest == nil || val(task.startTime) < val(earliest.startTime) {
				earliest = task
			}
		}
		if earliest != nil {
			return earliest.startTime
		}
		return ptr(db.today())
	}

	template := js.Trim(db.dateFormat)
	if parsed, ok := db.parse(str, template); ok {
		if db.inclusiveEndDates {
			return ptr(addDays(parsed, 1))
		}
		return &parsed
	}

	value, unit := parseDuration(str)
	if math.IsNaN(value) {
		db.warn(func() string {
			return `Gantt: "` + str + `" is neither a date in the "` + template + `" format nor a duration such as "3d", so the task gets no duration.`
		})
		return ptr(startTime)
	}
	end := add(startTime, value, unit)
	if isValid(end) {
		return &end
	}
	return ptr(startTime)
}

// Pushes the end of a task out by one day for every excluded day it covers.
func (db *GanttDb) checkTaskDates(task *GanttTask) {
	if len(db.excludes) == 0 || task.manualEndTime {
		return
	}
	cursor := addDays(*task.startTime, 1)
	endTime := *task.endTime
	maxEndTime := addDays(endTime, 10000).T
	invalid := false
	renderEndTime := math.NaN()
	hasRenderEnd := false
	for cursor.T <= endTime.T {
		db.spend(DAY_STEP)
		if !invalid {
			renderEndTime = endTime.T
			hasRenderEnd = true
		}
		invalid = db.isInvalidDate(cursor)
		if invalid {
			endTime.SetDate(endTime.Date() + 1)
			if endTime.T > maxEndTime {
				types.Throw("Failed to find a valid date that was not excluded by `excludes` after 10,000 iterations.",
					types.CodeSemantic, types.ErrorInfo{Type: "gantt"})
			}
		}
		cursor.SetDate(cursor.Date() + 1)
	}
	task.endTime = &endTime
	if hasRenderEnd {
		task.renderEndTime = ptr(js.NewDate(renderEndTime, db.loc))
	} else {
		task.renderEndTime = nil
	}
}
