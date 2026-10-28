// Ported from pele src/diagrams/sequence/types.ts at 8aff183be57021b533f319a29341a529be934b08.

package sequence

// The message kinds, numbered as in Mermaid.
const (
	LINETYPE_SOLID                             = 0
	LINETYPE_DOTTED                            = 1
	LINETYPE_NOTE                              = 2
	LINETYPE_SOLID_CROSS                       = 3
	LINETYPE_DOTTED_CROSS                      = 4
	LINETYPE_SOLID_OPEN                        = 5
	LINETYPE_DOTTED_OPEN                       = 6
	LINETYPE_LOOP_START                        = 10
	LINETYPE_LOOP_END                          = 11
	LINETYPE_ALT_START                         = 12
	LINETYPE_ALT_ELSE                          = 13
	LINETYPE_ALT_END                           = 14
	LINETYPE_OPT_START                         = 15
	LINETYPE_OPT_END                           = 16
	LINETYPE_ACTIVE_START                      = 17
	LINETYPE_ACTIVE_END                        = 18
	LINETYPE_PAR_START                         = 19
	LINETYPE_PAR_AND                           = 20
	LINETYPE_PAR_END                           = 21
	LINETYPE_RECT_START                        = 22
	LINETYPE_RECT_END                          = 23
	LINETYPE_SOLID_POINT                       = 24
	LINETYPE_DOTTED_POINT                      = 25
	LINETYPE_AUTONUMBER                        = 26
	LINETYPE_CRITICAL_START                    = 27
	LINETYPE_CRITICAL_OPTION                   = 28
	LINETYPE_CRITICAL_END                      = 29
	LINETYPE_BREAK_START                       = 30
	LINETYPE_BREAK_END                         = 31
	LINETYPE_PAR_OVER_START                    = 32
	LINETYPE_BIDIRECTIONAL_SOLID               = 33
	LINETYPE_BIDIRECTIONAL_DOTTED              = 34
	LINETYPE_SOLID_TOP                         = 41
	LINETYPE_SOLID_BOTTOM                      = 42
	LINETYPE_STICK_TOP                         = 43
	LINETYPE_STICK_BOTTOM                      = 44
	LINETYPE_SOLID_ARROW_TOP_REVERSE           = 45
	LINETYPE_SOLID_ARROW_BOTTOM_REVERSE        = 46
	LINETYPE_STICK_ARROW_TOP_REVERSE           = 47
	LINETYPE_STICK_ARROW_BOTTOM_REVERSE        = 48
	LINETYPE_SOLID_TOP_DOTTED                  = 51
	LINETYPE_SOLID_BOTTOM_DOTTED               = 52
	LINETYPE_STICK_TOP_DOTTED                  = 53
	LINETYPE_STICK_BOTTOM_DOTTED               = 54
	LINETYPE_SOLID_ARROW_TOP_REVERSE_DOTTED    = 55
	LINETYPE_SOLID_ARROW_BOTTOM_REVERSE_DOTTED = 56
	LINETYPE_STICK_ARROW_TOP_REVERSE_DOTTED    = 57
	LINETYPE_STICK_ARROW_BOTTOM_REVERSE_DOTTED = 58
	LINETYPE_CENTRAL_CONNECTION                = 59
	LINETYPE_CENTRAL_CONNECTION_REVERSE        = 60
	LINETYPE_CENTRAL_CONNECTION_DUAL           = 61
)

const (
	PLACEMENT_LEFTOF  = 0
	PLACEMENT_RIGHTOF = 1
	PLACEMENT_OVER    = 2
)

// SeqText is a text and its wrap marker; Wrap is nil where the text has none.
type SeqText struct {
	Text string
	Wrap *bool
}

type SeqBoxData struct {
	// Text is nil for none.
	Text  *string
	Color string
	Wrap  *bool
}

type SeqBox struct {
	// Name is nil for none.
	Name      *string
	Wrap      bool
	Fill      string
	ActorKeys []string
}

type SeqActor struct {
	Box         *SeqBox
	Name        string
	Description string
	Wrap        bool
	PrevActor   string
	NextActor   string
	Links       map[string]any
	Properties  map[string]any
	// Details is the id of the page element named by a `details` statement. Pele has no page to read it from.
	Details string
	Type    string
}

type SeqNumbering struct {
	// Start and Step are 0 for none, as pele's `||` reads them.
	Start   float64
	Step    float64
	Visible bool
}

type SeqMessage struct {
	ID string
	// From and To are nil for none.
	From *string
	To   *string
	// Message is the text; Numbering holds an autonumber statement instead.
	Message   string
	Numbering *SeqNumbering
	Wrap      bool
	// Type is -1 for none.
	Type     int
	Activate bool
	// Placement is -1 for none.
	Placement         int
	CentralConnection int
}

type SeqNote struct {
	Actor     []string
	Placement int
	Message   string
	Wrap      bool
}

// SeqStatement is one entry of the statement list the parser hands to `apply`, as Mermaid's grammar builds it.
type SeqStatement struct {
	Type string
	// Actor is one name, or two for a note over two participants (ActorPair).
	Actor             string
	ActorPair         []string
	Draw              string
	Description       *SeqText
	Config            *string
	SignalType        int
	From              string
	To                string
	Msg               *SeqText
	Activate          bool
	CentralConnection int
	Placement         int
	Text              *SeqText
	BoxData           *SeqBoxData
	BoxText           string
	LoopText          *SeqText
	OptText           *SeqText
	AltText           *SeqText
	ParText           *SeqText
	CriticalText      *SeqText
	OptionText        *SeqText
	BreakText         *SeqText
	Color             *SeqText
	SequenceIndex     float64
	SequenceIndexStep float64
	SequenceVisible   bool
}

// SeqItem is a *SeqStatement, a string, or a []SeqItem.
type SeqItem = any
