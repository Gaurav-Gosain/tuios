package config

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// The subagent options of a rail: how the rows under an agent row look, which
// fields they carry, how many there may be, whether a heading line folds them,
// and how long a finished one stays. Each is a key of [appearance.sidebar],
// and [appearance.sidebar.left] and [appearance.sidebar.right] may set any of
// them again for the rail on that edge. Every default draws what the rail drew
// before the options existed.

// SidebarSubagentRowsTree draws the rows on tree lines (├─, └─).
const SidebarSubagentRowsTree = "tree"

// The values of appearance.sidebar.subagent_collapse. Off draws no heading
// line, as before. Expanded and collapsed draw a heading line over each
// agent's subagents that a click, enter or the agent_subagents key opens and
// shuts, and say which way it starts.
const (
	SidebarSubagentCollapseOff       = "off"
	SidebarSubagentCollapseExpanded  = "expanded"
	SidebarSubagentCollapseCollapsed = "collapsed"
)

// SidebarSubagentCollapseValues lists the valid values for
// appearance.sidebar.subagent_collapse.
var SidebarSubagentCollapseValues = []string{
	SidebarSubagentCollapseOff, SidebarSubagentCollapseExpanded, SidebarSubagentCollapseCollapsed,
}

// The fields a subagent row can draw, the names of
// appearance.sidebar.subagent_fields.
const (
	SubagentFieldMark = "mark" // the state mark, also gated by show_glyphs
	SubagentFieldName = "name" // what it was asked to do
	SubagentFieldType = "type" // its agent type, such as Explore
	SubagentFieldTime = "time" // how long it ran, at the right edge
	SubagentFieldNote = "note" // second line: the tool it runs, or how it ended
)

// SubagentFieldNames lists the field names in the order a row draws them.
var SubagentFieldNames = []string{
	SubagentFieldMark, SubagentFieldName, SubagentFieldType, SubagentFieldTime, SubagentFieldNote,
}

// DefaultSubagentFields is the row as it was drawn before the option.
const DefaultSubagentFields = "mark,name,time,note"

// SidebarSubagentMaxRowsMax bounds subagent_max_rows: a client keeps at most
// sixteen subagents per pane.
const SidebarSubagentMaxRowsMax = 16

// SubagentHideOff keeps a finished subagent's row until the daemon drops it.
const SubagentHideOff = "off"

// SubagentFields is the set of fields a subagent row draws.
type SubagentFields struct {
	Mark, Name, Type, Time, Note bool
}

// ParseSubagentFields reads appearance.sidebar.subagent_fields, a comma list
// of SubagentFieldNames. Empty is the default. bad names each word the list
// does not know; they are skipped. A list that names no known field reads as
// the default, and none reports it.
func ParseSubagentFields(v string) (f SubagentFields, bad []string, none bool) {
	if strings.TrimSpace(v) == "" {
		v = DefaultSubagentFields
	}
	defer func() {
		if f == (SubagentFields{}) {
			none = true
			f, _, _ = ParseSubagentFields(DefaultSubagentFields)
		}
	}()
	for word := range strings.SplitSeq(v, ",") {
		switch w := strings.ToLower(strings.TrimSpace(word)); w {
		case "":
		case SubagentFieldMark:
			f.Mark = true
		case SubagentFieldName:
			f.Name = true
		case SubagentFieldType:
			f.Type = true
		case SubagentFieldTime:
			f.Time = true
		case SubagentFieldNote:
			f.Note = true
		default:
			bad = append(bad, w)
		}
	}
	return f, bad, none
}

// ParseSubagentHide reads appearance.sidebar.subagent_hide_finished: off (or
// empty) keeps a finished row as long as the daemon lists it, and a duration
// such as 10s hides it that long after it ended, 0s at once. hide is false
// for off. ok is false for a value that is neither, which reads as off.
func ParseSubagentHide(v string) (after time.Duration, hide, ok bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "", SubagentHideOff:
		return 0, false, true
	case "0":
		return 0, true, true
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, false, false
	}
	return d, true, true
}

// SidebarSubagentView is how one rail draws the subagents of its agent rows,
// resolved from the config.
type SidebarSubagentView struct {
	// Rows draws a row per subagent. False draws the count alone.
	Rows bool
	// Tree draws the rows on tree lines.
	Tree bool
	// Collapse is one of SidebarSubagentCollapseValues.
	Collapse string
	// Fields are what each row draws.
	Fields SubagentFields
	// MaxRows is the most lines one agent's subagents take, the "+N more"
	// line included. Zero is no limit beyond the section's room.
	MaxRows int
	// Hide hides a finished row HideAfter after it ended.
	Hide      bool
	HideAfter time.Duration
}

// Collapsible reports whether the rows have a heading line that folds them.
func (v SidebarSubagentView) Collapsible() bool {
	return v.Collapse == SidebarSubagentCollapseExpanded || v.Collapse == SidebarSubagentCollapseCollapsed
}

// DefaultSidebarSubagentView is the view with every option unset.
func DefaultSidebarSubagentView() SidebarSubagentView {
	return ResolveSidebarSubagentView(SidebarConfig{}, nil)
}

// sidebarSubagentKeys are the subagent keys of one table, [appearance.sidebar]
// or one of its edge tables.
type sidebarSubagentKeys struct {
	rows, collapse, fields, hide string
	maxRows                      *int
}

func (sb SidebarConfig) subagentKeys() sidebarSubagentKeys {
	k := sidebarSubagentKeys{
		rows: sb.SubagentRows, collapse: sb.SubagentCollapse,
		fields: sb.SubagentFields, hide: sb.SubagentHideFinished,
	}
	if sb.SubagentMaxRows != 0 {
		n := sb.SubagentMaxRows
		k.maxRows = &n
	}
	return k
}

func (e *SidebarEdgeConfig) subagentKeys() sidebarSubagentKeys {
	if e == nil {
		return sidebarSubagentKeys{}
	}
	return sidebarSubagentKeys{
		rows: e.SubagentRows, collapse: e.SubagentCollapse,
		fields: e.SubagentFields, hide: e.SubagentHideFinished, maxRows: e.SubagentMaxRows,
	}
}

// ResolveSidebarSubagentView is the view of the rail whose edge table is
// edge (nil for none): each key the edge table sets wins over the one in
// [appearance.sidebar]. A value that does not parse reads as the default, and
// validation names it.
func ResolveSidebarSubagentView(sb SidebarConfig, edge *SidebarEdgeConfig) SidebarSubagentView {
	k := sb.subagentKeys()
	o := edge.subagentKeys()
	if o.rows != "" {
		k.rows = o.rows
	}
	if o.collapse != "" {
		k.collapse = o.collapse
	}
	if o.fields != "" {
		k.fields = o.fields
	}
	if o.hide != "" {
		k.hide = o.hide
	}
	if o.maxRows != nil {
		k.maxRows = o.maxRows
	}
	v := SidebarSubagentView{
		Rows:     k.rows != SidebarSubagentRowsCount,
		Tree:     k.rows == SidebarSubagentRowsTree,
		Collapse: SidebarSubagentCollapseOff,
	}
	if slices.Contains(SidebarSubagentCollapseValues, k.collapse) {
		v.Collapse = k.collapse
	}
	v.Fields, _, _ = ParseSubagentFields(k.fields)
	if k.maxRows != nil {
		v.MaxRows = min(max(*k.maxRows, 0), SidebarSubagentMaxRowsMax)
	}
	v.HideAfter, v.Hide, _ = ParseSubagentHide(k.hide)
	return v
}

// sidebarSubagentProblems are the warnings for one table's subagent keys.
func sidebarSubagentProblems(k sidebarSubagentKeys) map[string]string {
	out := map[string]string{}
	if k.rows != "" && !slices.Contains(SidebarSubagentRowsValues, k.rows) {
		out["subagent_rows"] = fmt.Sprintf("'%s' is not one of %s; read as %s",
			k.rows, strings.Join(SidebarSubagentRowsValues, ", "), SidebarSubagentRowsRows)
	}
	if k.collapse != "" && !slices.Contains(SidebarSubagentCollapseValues, k.collapse) {
		out["subagent_collapse"] = fmt.Sprintf("'%s' is not one of %s; read as %s",
			k.collapse, strings.Join(SidebarSubagentCollapseValues, ", "), SidebarSubagentCollapseOff)
	}
	if err := CheckSubagentFields(k.fields); err != nil {
		out["subagent_fields"] = err.Error() + "; read as " + DefaultSubagentFields
	}
	if k.maxRows != nil && (*k.maxRows < 0 || *k.maxRows > SidebarSubagentMaxRowsMax) {
		out["subagent_max_rows"] = fmt.Sprintf("%d is not from 0 to %d; read as the nearest of the two",
			*k.maxRows, SidebarSubagentMaxRowsMax)
	}
	if err := CheckSubagentHide(k.hide); err != nil {
		out["subagent_hide_finished"] = err.Error() + "; read as off"
	}
	return out
}

// validateSidebarSubagents warns about the subagent keys of
// [appearance.sidebar] and of each edge table.
func validateSidebarSubagents(sb SidebarConfig, result *ValidationResult) {
	tables := []struct {
		field string
		keys  sidebarSubagentKeys
	}{
		{"appearance.sidebar", sb.subagentKeys()},
		{"appearance.sidebar.left", sb.Left.subagentKeys()},
		{"appearance.sidebar.right", sb.Right.subagentKeys()},
	}
	for _, t := range tables {
		problems := sidebarSubagentProblems(t.keys)
		keys := make([]string, 0, len(problems))
		for key := range problems {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			result.Warnings = append(result.Warnings, ValidationError{Field: t.field, Key: key, Message: problems[key]})
		}
	}
}

// CheckSubagentFields is the error for a subagent_fields value that names an
// unknown field or no field at all. Empty is the default and passes.
func CheckSubagentFields(v string) error {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	_, bad, none := ParseSubagentFields(v)
	switch {
	case len(bad) > 0:
		return fmt.Errorf("unknown field %s; the fields are %s", strings.Join(bad, ", "), strings.Join(SubagentFieldNames, ", "))
	case none:
		return fmt.Errorf("names no field; the fields are %s", strings.Join(SubagentFieldNames, ", "))
	}
	return nil
}

// CheckSubagentHide is the error for a subagent_hide_finished value that is
// neither a duration nor off.
func CheckSubagentHide(v string) error {
	if _, _, ok := ParseSubagentHide(v); !ok {
		return fmt.Errorf("%q is not a duration such as 30s, or off", v)
	}
	return nil
}
