package gantt

import "tuios.dev/sketch/internal/js"

type rawTask struct {
	data  string
	start *string
	end   string
}

type GanttTask struct {
	id            string
	task          string
	section       string
	startTime     *js.Date
	endTime       *js.Date
	renderEndTime *js.Date
	manualEndTime bool
	processed     bool
	active        bool
	done          bool
	crit          bool
	milestone     bool
	vert          bool
	order         int
	classes       []string
	prevTaskId    *string
	raw           rawTask
}
