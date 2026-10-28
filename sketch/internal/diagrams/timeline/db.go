// Ported from pele src/diagrams/timeline/db.ts at 8aff183be57021b533f319a29341a529be934b08.

package timeline

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

type TimelinePeriod struct {
	text   string
	events []string
	// Name of the section in force when the period was declared, "" before the first one.
	section string
	// Index into `sections` of that section, -1 before the first one.
	sectionIndex int
}

type TimelineDb struct {
	title     string
	accTitle  string
	accDescr  string
	direction string // "LR" or "TD"
	sections  []string
	periods   []TimelinePeriod

	currentSection string
}

func newTimelineDb() *TimelineDb {
	return &TimelineDb{direction: "LR"}
}

func (db *TimelineDb) setDiagramTitle(text string) { db.title = text }

func (db *TimelineDb) setAccTitle(text string) { db.accTitle = js.TrimStart(text) }

func (db *TimelineDb) setAccDescription(text string) { db.accDescr = joinLines(text) }

func (db *TimelineDb) setDirection(direction string) { db.direction = direction }

func (db *TimelineDb) addSection(text string) {
	db.currentSection = text
	db.sections = append(db.sections, text)
}

func (db *TimelineDb) addTask(period string, _ int, event string) {
	var events []string
	if event != "" {
		events = []string{event}
	}
	db.periods = append(db.periods, TimelinePeriod{
		text:         period,
		events:       events,
		section:      db.currentSection,
		sectionIndex: len(db.sections) - 1,
	})
}

func (db *TimelineDb) addEvent(event string) {
	// Mermaid fails here too, with a TypeError.
	if len(db.periods) == 0 {
		types.Throw("An event needs a time period before it.", types.CodeSemantic, types.ErrorInfo{Type: "timeline"})
	}
	period := &db.periods[len(db.periods)-1]
	period.events = append(period.events, event)
}

// joinLines is text.replace(/\n\s+/g, '\n').
func joinLines(text string) string {
	if !strings.Contains(text, "\n") {
		return text
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '\n' {
			out.WriteByte(text[i])
			i++
			continue
		}
		j := i + 1
		for j < len(text) {
			c, size := utf8.DecodeRuneInString(text[j:])
			if !js.IsSpace(c) {
				break
			}
			j += size
		}
		out.WriteByte('\n')
		i = j
	}
	return out.String()
}
