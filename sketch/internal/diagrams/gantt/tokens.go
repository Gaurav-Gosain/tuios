// Ported from pele src/diagrams/gantt/tokens.ts at 8aff183be57021b533f319a29341a529be934b08.

// Package gantt parses and draws Mermaid gantt charts.
package gantt

// Token types. pele names them T.X; the port names them T_X.
const (
	T_END = iota
	T_ERROR
	T_open_directive
	T_acc_title
	T_acc_title_value
	T_acc_descr
	T_acc_descr_value
	T_acc_descr_multiline_value
	T_NL
	T_href
	T_callbackname
	T_callbackargs
	T_click
	T_gantt
	T_dateFormat
	T_inclusiveEndDates
	T_topAxis
	T_axisFormat
	T_tickInterval
	T_includes
	T_excludes
	T_todayMarker
	T_weekday_monday
	T_weekday_tuesday
	T_weekday_wednesday
	T_weekday_thursday
	T_weekday_friday
	T_weekday_saturday
	T_weekday_sunday
	T_weekend_friday
	T_weekend_saturday
	T_date
	T_title
	T_accDescription
	T_section
	T_taskTxt
	T_taskData
	T_COLON
	T_EOF
)

var TOKEN_NAMES = [...]string{
	"$end",
	"INVALID",
	"open_directive",
	"acc_title",
	"acc_title_value",
	"acc_descr",
	"acc_descr_value",
	"acc_descr_multiline_value",
	"NL",
	"href",
	"callbackname",
	"callbackargs",
	"click",
	"gantt",
	"dateFormat",
	"inclusiveEndDates",
	"topAxis",
	"axisFormat",
	"tickInterval",
	"includes",
	"excludes",
	"todayMarker",
	"weekday_monday",
	"weekday_tuesday",
	"weekday_wednesday",
	"weekday_thursday",
	"weekday_friday",
	"weekday_saturday",
	"weekday_sunday",
	"weekend_friday",
	"weekend_saturday",
	"date",
	"title",
	"accDescription",
	"section",
	"taskTxt",
	"taskData",
	":",
	"EOF",
}
