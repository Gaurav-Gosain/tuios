// Ported from pele src/diagrams/sequence/db.ts at 8aff183be57021b533f319a29341a529be934b08.

package sequence

import (
	"encoding/json"
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/lazyre"
	"tuios.dev/sketch/internal/types"
	"tuios.dev/sketch/internal/util"
)

type SeqDbOptions struct {
	// Wrap is the top-level `wrap` setting, which the `%%{wrap}%%` directive also sets. nil for none.
	Wrap *bool
	// SequenceWrap is `sequence.wrap`.
	SequenceWrap bool
}

var (
	reWrap           = lazyre.New(`^:?wrap:`)
	reNowrap         = lazyre.New(`^:?nowrap:`)
	reWrapPrefix     = lazyre.New(`^:?(?:no)?wrap:`)
	reBoxFunction    = lazyre.New(`^(?:rgba?|hsla?)[` + js.S + `]*\(`)
	reLineTerminator = lazyre.New(`[\r\x{2028}\x{2029}]`)
	reWord           = lazyre.New(`^\w*`)
	reLeadSpace      = lazyre.New(`^[` + js.S + `]+`)
	reDescrIndent    = lazyre.New(`\n[` + js.S + `]+`)
)

func failSeq(message string) {
	types.Throw(message, types.CodeSemantic, types.ErrorInfo{Type: "sequence"})
}

func boolp(b bool) *bool { return &b }

func extractWrap(raw string) *SeqText {
	t := js.Trim(raw)
	var wrap *bool
	if reWrap.MatchString(t) {
		wrap = boolp(true)
	} else if reNowrap.MatchString(t) {
		wrap = boolp(false)
	}
	if wrap != nil {
		t = reWrapPrefix.ReplaceAllString(t, "")
	}
	return &SeqText{Text: js.Trim(t), Wrap: wrap}
}

// splitBox splits a box line into a leading color and the rest, as Mermaid's
// /^((?:rgba?|hsla?)\s*\(.*\)|\w*)(.*)$/ does. `.` stops at a line terminator, so a line that
// holds one outside the gap before the parenthesis does not match at all.
func splitBox(str string) ([2]string, bool) {
	if fn := reBoxFunction.FindString(str); fn != "" {
		close := strings.LastIndexByte(str, ')')
		if close >= len(fn) {
			if reLineTerminator.MatchString(str[len(fn):]) {
				return [2]string{}, false
			}
			return [2]string{str[:close+1], str[close+1:]}, true
		}
	}
	if reLineTerminator.MatchString(str) {
		return [2]string{}, false
	}
	word := reWord.FindString(str)
	return [2]string{word, str[len(word):]}, true
}

// entries lists a JSON value as key, value pairs, as Object.entries does.
func entries(value any) [][2]any {
	var out [][2]any
	switch t := value.(type) {
	case string:
		i := 0
		for _, ch := range t {
			out = append(out, [2]any{strconv.Itoa(i), string(ch)})
			i++
		}
	case map[string]any:
		for k, v := range t {
			out = append(out, [2]any{k, v})
		}
	case []any:
		for i, v := range t {
			out = append(out, [2]any{strconv.Itoa(i), v})
		}
	}
	return out
}

type actorMap struct {
	keys []string
	m    map[string]*SeqActor
}

func (a *actorMap) Get(k string) (*SeqActor, bool) {
	v, ok := a.m[k]
	return v, ok
}

func (a *actorMap) Set(k string, v *SeqActor) {
	if _, ok := a.m[k]; !ok {
		a.keys = append(a.keys, k)
	}
	a.m[k] = v
}

func (a *actorMap) Len() int { return len(a.keys) }

type SeqDb struct {
	Actors          *actorMap
	CreatedActors   map[string]int
	DestroyedActors map[string]int
	Boxes           []*SeqBox
	Messages        []*SeqMessage
	Notes           []*SeqNote
	Title           string
	AccTitle        string
	AccDescr        string
	prevActor       string
	currentBox      *SeqBox
	lastCreated     string
	lastDestroyed   string
	wrapEnabled     *bool
	sequenceWrap    bool
	active          map[string]int
}

func NewSeqDb(options SeqDbOptions) *SeqDb {
	return &SeqDb{
		Actors:          &actorMap{m: map[string]*SeqActor{}},
		CreatedActors:   map[string]int{},
		DestroyedActors: map[string]int{},
		wrapEnabled:     options.Wrap,
		sequenceWrap:    options.SequenceWrap,
		active:          map[string]int{},
	}
}

func (db *SeqDb) autoWrap() bool {
	if db.wrapEnabled != nil {
		return *db.wrapEnabled
	}
	return db.sequenceWrap
}

func (db *SeqDb) SetAccTitle(text string) { db.AccTitle = reLeadSpace.ReplaceAllString(text, "") }

func (db *SeqDb) SetAccDescription(text string) {
	db.AccDescr = reDescrIndent.ReplaceAllString(text, "\n")
}

func (db *SeqDb) SetDiagramTitle(text string) { db.Title = text }

func (db *SeqDb) ParseMessage(str string) *SeqText { return extractWrap(str) }

func (db *SeqDb) ParseBoxData(str string) *SeqBoxData {
	match, ok := splitBox(str)
	color := "transparent"
	if ok && match[0] != "" {
		color = js.Trim(match[0])
	}
	var title *string
	if ok && match[1] != "" {
		t := js.Trim(match[1])
		title = &t
	}
	if !isCssColor(color) {
		color = "transparent"
		t := js.Trim(str)
		title = &t
	}
	if title == nil {
		return &SeqBoxData{Color: color}
	}
	w := extractWrap(*title)
	out := &SeqBoxData{Color: color, Wrap: w.Wrap}
	if w.Text != "" {
		out.Text = &w.Text
	}
	return out
}

func (db *SeqDb) addBox(data *SeqBoxData) {
	wrap := db.autoWrap()
	if data.Wrap != nil {
		wrap = *data.Wrap
	}
	db.currentBox = &SeqBox{Name: data.Text, Wrap: wrap, Fill: data.Color, ActorKeys: []string{}}
	db.Boxes = append(db.Boxes, db.currentBox)
}

type actorDescription struct {
	text string
	wrap *bool
}

// addActor takes pele's optional arguments as nil pointers.
func (db *SeqDb) addActor(id, name string, description *actorDescription, typ *string, metadata *string) {
	assignedBox := db.currentBox
	var doc map[string]any
	if metadata != nil {
		src := "{\n" + *metadata + "\n}"
		if strings.Contains(*metadata, "\n") {
			src = *metadata + "\n"
		}
		parsed, err := util.ParseYaml(src)
		if err != nil {
			types.Throw(`Config of participant "`+id+`" is not valid YAML. `+err.Error(), types.CodeSyntax, types.ErrorInfo{Type: "sequence"})
		}
		doc, _ = parsed.(map[string]any)
	}
	if docType, ok := doc["type"]; ok && docType != nil {
		t := js.String(docType)
		typ = &t
	}
	if docAlias, ok := doc["alias"]; ok && js.Truthy(docAlias) && (description == nil || description.text == name) {
		var wrap *bool
		if description != nil {
			wrap = description.wrap
		}
		description = &actorDescription{text: js.String(docAlias), wrap: wrap}
	}

	if old, ok := db.Actors.Get(id); ok {
		if db.currentBox != nil && old.Box != nil && db.currentBox != old.Box {
			failSeq("A same participant should only be defined in one Box: " + old.Name + " can't be in '" + boxName(old.Box) +
				"' and in '" + boxName(db.currentBox) + "' at the same time.")
		}
		if old.Box != nil {
			assignedBox = old.Box
		} else {
			assignedBox = db.currentBox
		}
		old.Box = assignedBox
		if name == old.Name && description == nil {
			return
		}
	}
	// A participant that is only mentioned in a message has no kind and takes its name as its label.
	if description == nil || typ == nil {
		description = &actorDescription{text: name}
	}

	wrap := db.autoWrap()
	if description.wrap != nil {
		wrap = *description.wrap
	}
	kind := "participant"
	if typ != nil {
		kind = *typ
	}
	db.Actors.Set(id, &SeqActor{
		Box:         assignedBox,
		Name:        name,
		Description: description.text,
		Wrap:        wrap,
		PrevActor:   db.prevActor,
		Links:       map[string]any{},
		Properties:  map[string]any{},
		Type:        kind,
	})
	if db.prevActor != "" {
		if prev, ok := db.Actors.Get(db.prevActor); ok {
			prev.NextActor = id
		}
	}
	if db.currentBox != nil {
		db.currentBox.ActorKeys = append(db.currentBox.ActorKeys, id)
	}
	db.prevActor = id
}

// boxName is `${box.name}`: "undefined" for a box with no name.
func boxName(b *SeqBox) string {
	if b.Name == nil {
		return "undefined"
	}
	return *b.Name
}

func strp(s string) *string { return &s }

// addSignal takes "" for pele's undefined from and to, and nil for no message.
func (db *SeqDb) addSignal(from, to *string, message *SeqText, typ int, activate bool, centralConnection int) {
	// Mermaid recounts every earlier message here. A running count per participant says the same.
	if typ == LINETYPE_ACTIVE_END {
		count := 0
		if from != nil && *from != "" {
			count = db.active[*from]
		}
		if count < 1 {
			who := "undefined"
			if from != nil {
				who = *from
			}
			failSeq("Trying to inactivate an inactive participant (" + who + ")")
		}
		db.active[*from] = count - 1
	} else if typ == LINETYPE_ACTIVE_START && from != nil && *from != "" {
		db.active[*from]++
	}
	text := ""
	wrap := db.autoWrap()
	if message != nil {
		text = message.Text
		if message.Wrap != nil {
			wrap = *message.Wrap
		}
	}
	db.Messages = append(db.Messages, &SeqMessage{
		ID:                strconv.Itoa(len(db.Messages)),
		From:              from,
		To:                to,
		Message:           text,
		Wrap:              wrap,
		Type:              typ,
		Activate:          activate,
		Placement:         -1,
		CentralConnection: centralConnection,
	})
}

func (db *SeqDb) addNote(actor []string, placement int, message *SeqText) {
	wrap := db.autoWrap()
	if message.Wrap != nil {
		wrap = *message.Wrap
	}
	db.Notes = append(db.Notes, &SeqNote{Actor: actor, Placement: placement, Message: message.Text, Wrap: wrap})
	pair := actor
	if len(actor) == 1 {
		pair = []string{actor[0], actor[0]}
	}
	db.Messages = append(db.Messages, &SeqMessage{
		ID:        strconv.Itoa(len(db.Messages)),
		From:      strp(pair[0]),
		To:        strp(pair[1]),
		Message:   message.Text,
		Wrap:      wrap,
		Type:      LINETYPE_NOTE,
		Placement: placement,
	})
}

// addLinks: Mermaid logs and ignores menu text that is not valid JSON. It also undoes the `&amp;` and
// `&equals;` its sanitizer writes; Pele has no sanitizer, and a `;` cannot occur in the text.
func (db *SeqDb) addLinks(actorID string, text *SeqText) {
	actor, ok := db.Actors.Get(actorID)
	if !ok {
		return
	}
	var parsed any
	if json.Unmarshal([]byte(text.Text), &parsed) != nil {
		return
	}
	for _, e := range entries(parsed) {
		actor.Links[e[0].(string)] = e[1]
	}
}

func (db *SeqDb) addALink(actorID string, text *SeqText) {
	actor, ok := db.Actors.Get(actorID)
	if !ok {
		return
	}
	t := text.Text
	sep := strings.IndexByte(t, '@')
	// slice(0, sep - 1) with sep = -1 is slice(0, -2): all but the last two characters.
	end := sep - 1
	if end < 0 {
		end = len(t) + end
		if end < 0 {
			end = 0
		}
	}
	actor.Links[js.Trim(t[:end])] = js.Trim(t[sep+1:])
}

func (db *SeqDb) addProperties(actorID string, text *SeqText) {
	actor, ok := db.Actors.Get(actorID)
	if !ok {
		return
	}
	var parsed any
	if json.Unmarshal([]byte(text.Text), &parsed) != nil {
		return
	}
	for _, e := range entries(parsed) {
		actor.Properties[e[0].(string)] = e[1]
	}
}

// addDetails: Mermaid reads the properties and links from a page element with this id. Pele keeps the id.
func (db *SeqDb) addDetails(actorID string, text *SeqText) {
	if actor, ok := db.Actors.Get(actorID); ok {
		actor.Details = text.Text
	}
}

// Apply walks the nested statement lists. Mermaid does it by recursion; an explicit stack takes any depth.
func (db *SeqDb) Apply(root SeqItem) {
	var lists [][]SeqItem
	var at []int
	item := root
	has := true
	for {
		if has {
			switch t := item.(type) {
			case []SeqItem:
				lists = append(lists, t)
				at = append(at, 0)
			case *SeqStatement:
				db.applyOne(t)
			}
		}
		depth := len(lists) - 1
		if depth < 0 {
			return
		}
		list := lists[depth]
		if at[depth] < len(list) {
			item = list[at[depth]]
			at[depth]++
			has = true
		} else {
			lists = lists[:depth]
			at = at[:depth]
			has = false
		}
	}
}

func (db *SeqDb) applyOne(param *SeqStatement) {
	actor := param.Actor
	switch param.Type {
	case "sequenceIndex":
		db.Messages = append(db.Messages, &SeqMessage{
			ID:        strconv.Itoa(len(db.Messages)),
			Numbering: &SeqNumbering{Start: param.SequenceIndex, Step: param.SequenceIndexStep, Visible: param.SequenceVisible},
			Type:      param.SignalType,
			Placement: -1,
		})
	case "addParticipant":
		db.addActor(actor, actor, describe(param.Description), drawOf(param), param.Config)
	case "createParticipant":
		if _, ok := db.Actors.Get(actor); ok {
			failSeq("It is not possible to have actors with the same id, even if one is destroyed before the next is created. Use 'AS' aliases to simulate the behavior")
		}
		db.lastCreated = actor
		db.addActor(actor, actor, describe(param.Description), drawOf(param), param.Config)
		db.CreatedActors[actor] = len(db.Messages)
	case "destroyParticipant":
		db.lastDestroyed = actor
		db.DestroyedActors[actor] = len(db.Messages)
	case "activeStart", "activeEnd", "centralConnection", "centralConnectionReverse":
		db.addSignal(strp(actor), nil, nil, param.SignalType, false, 0)
	case "addNote":
		if param.ActorPair != nil {
			db.addNote(param.ActorPair, param.Placement, param.Text)
		} else {
			db.addNote([]string{actor}, param.Placement, param.Text)
		}
	case "addLinks":
		db.addLinks(actor, param.Text)
	case "addALink":
		db.addALink(actor, param.Text)
	case "addProperties":
		db.addProperties(actor, param.Text)
	case "addDetails":
		db.addDetails(actor, param.Text)
	case "addMessage":
		if db.lastCreated != "" {
			if param.To != db.lastCreated {
				failSeq("The created participant " + db.lastCreated + " does not have an associated creating message after its declaration. Please check the sequence diagram.")
			}
			db.lastCreated = ""
		} else if db.lastDestroyed != "" {
			if param.To != db.lastDestroyed && param.From != db.lastDestroyed {
				failSeq("The destroyed participant " + db.lastDestroyed + " does not have an associated destroying message after its declaration. Please check the sequence diagram.")
			}
			db.lastDestroyed = ""
		}
		db.addSignal(strp(param.From), strp(param.To), param.Msg, param.SignalType, param.Activate, param.CentralConnection)
	case "boxStart":
		db.addBox(param.BoxData)
	case "boxEnd":
		db.currentBox = nil
	case "loopStart":
		db.addSignal(nil, nil, param.LoopText, param.SignalType, false, 0)
	case "rectStart":
		db.addSignal(nil, nil, param.Color, param.SignalType, false, 0)
	case "optStart":
		db.addSignal(nil, nil, param.OptText, param.SignalType, false, 0)
	case "altStart", "else":
		db.addSignal(nil, nil, param.AltText, param.SignalType, false, 0)
	case "parStart", "and":
		db.addSignal(nil, nil, param.ParText, param.SignalType, false, 0)
	case "criticalStart":
		db.addSignal(nil, nil, param.CriticalText, param.SignalType, false, 0)
	case "option":
		db.addSignal(nil, nil, param.OptionText, param.SignalType, false, 0)
	case "breakStart":
		db.addSignal(nil, nil, param.BreakText, param.SignalType, false, 0)
	case "loopEnd", "rectEnd", "optEnd", "altEnd", "parEnd", "criticalEnd", "breakEnd":
		db.addSignal(nil, nil, nil, param.SignalType, false, 0)
	}
}

func describe(t *SeqText) *actorDescription {
	if t == nil {
		return nil
	}
	return &actorDescription{text: t.Text, wrap: t.Wrap}
}

func drawOf(param *SeqStatement) *string {
	if param.Draw == "" {
		return nil
	}
	return &param.Draw
}
