// Ported from pele src/diagrams/gantt/types.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
//
// GanttModel is the GanttDb itself here. A Date that pele leaves undefined is nil.

package gantt

import "tuios.dev/sketch/internal/js"

type rawTask struct {
	data string
	// nil when the task gives no start.
	start *string
	end   string
}

type GanttTask struct {
	id string
	// The task's label, under the name Mermaid gives it.
	task    string
	section string
	// Unset until `getTasks` has scheduled the task, and still unset when what it refers to never resolves.
	startTime *js.Date
	endTime   *js.Date
	// Where the bar ends when excluded days pushed the task's end out; null when the end was given as a date.
	renderEndTime *js.Date
	manualEndTime bool
	processed     bool
	active        bool
	done          bool
	crit          bool
	milestone     bool
	vert          bool
	// Row index in source order; -1 for vertical markers, which take no row.
	order      int
	classes    []string
	prevTaskId *string
	raw        rawTask
}
