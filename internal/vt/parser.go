package vt

// The sequence parser. This is charmbracelet/x/ansi's Parser (v0.11.8, MIT,
// Copyright (c) 2023 Charmbracelet, Inc.) with three changes:
//
//   - the string data buffer starts small and grows on demand up to its cap,
//     instead of being allocated at the cap up front;
//   - a byte with the top bit set inside a string state is payload, never an
//     8-bit C1 control (see advance);
//   - a parameter byte inside a CSI takes a direct path in advance that makes
//     the same change to the params as the transition table and performAction
//     would, without the table lookup and the action switch.
//
// Upstream allocates the whole buffer in SetDataSize, and the emulator needs
// a 4 MiB cap so a sixel image or a large OSC 52 write is not cut short. That
// was 4 MiB resident per pane on both sides of the socket before the pane had
// printed anything: with eight empty panes it was 32 MiB of a 55 MiB daemon
// heap, the largest per-pane constant left on the pure backend. Upstream's
// unlimited mode grows by append with no cap at all, so a guest could make
// the process allocate without bound. Neither shape is what a terminal wants,
// which is a buffer that costs nothing until a payload arrives and never
// exceeds the cap. A payload longer than the cap is cut at the cap, exactly
// as it was before.
//
// Apart from those, the state machine, the parameter handling and the
// dispatch are verbatim upstream, so every sequence parses as it did.

import (
	"unicode/utf8"
	"unsafe"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

// seqParserInitialData is the data buffer a fresh parser starts with. Every
// OSC a shell prompt emits (title, cwd, semantic marks) fits in it, and a
// pane that never receives a larger payload never grows past it.
const seqParserInitialData = 4 * 1024

// seqParser is a DEC ANSI compatible sequence parser. See [ansi.Parser].
type seqParser struct {
	handler ansi.Handler

	// params contains the raw parameters of the sequence.
	// These parameters used when constructing CSI and DCS sequences.
	params []int

	// data contains the raw data of the sequence.
	// These data used when constructing OSC, DCS, SOS, PM, and APC sequences.
	data []byte

	// dataLen keeps track of the length of the data buffer.
	dataLen int

	// dataCap is the most bytes of one string sequence the parser keeps.
	// data grows towards it on demand and never past it.
	dataCap int

	// paramsLen keeps track of the number of parameters.
	// This is limited by the size of the params buffer.
	//
	// This is also used when collecting UTF-8 runes to keep track of the
	// number of rune bytes collected.
	paramsLen int

	// cmd contains the raw command along with the private prefix and
	// intermediate bytes of the sequence.
	// The first lower byte contains the command byte, the next byte contains
	// the private prefix, and the next byte contains the intermediate byte.
	//
	// This is also used when collecting UTF-8 runes treating it as a slice of
	// 4 bytes.
	cmd int

	// state is the current state of the parser.
	state byte
}

// newSeqParser returns a parser with [parser.MaxParamsSize] parameters and a
// data buffer that grows to dataCap.
func newSeqParser(dataCap int) *seqParser {
	p := new(seqParser)
	p.SetParamsSize(parser.MaxParamsSize)
	p.SetDataCap(dataCap)
	return p
}

// SetParamsSize sets the size of the parameters buffer.
// This is used when constructing CSI and DCS sequences.
func (p *seqParser) SetParamsSize(size int) {
	p.params = make([]int, size)
}

// SetDataCap sets the most bytes of one OSC, DCS, SOS, PM or APC payload
// the parser keeps. The buffer starts at the smaller of the cap and
// seqParserInitialData and grows on demand.
func (p *seqParser) SetDataCap(size int) {
	size = max(size, 0)
	p.dataCap = size
	p.data = make([]byte, min(size, seqParserInitialData))
	p.dataLen = 0
}

// SetHandler sets the handler for the parser.
func (p *seqParser) SetHandler(h ansi.Handler) {
	p.handler = h
}

// Params returns the list of parsed packed parameters.
func (p *seqParser) Params() ansi.Params {
	return unsafe.Slice((*ansi.Param)(unsafe.Pointer(&p.params[0])), p.paramsLen)
}

// Rune returns the last dispatched sequence as a rune.
func (p *seqParser) Rune() rune {
	rw := utf8ByteLen(byte(p.cmd & 0xff))
	if rw == -1 {
		return utf8.RuneError
	}
	r, _ := utf8.DecodeRune((*[utf8.UTFMax]byte)(unsafe.Pointer(&p.cmd))[:rw])
	return r
}

// Data returns the raw data of the last dispatched sequence.
func (p *seqParser) Data() []byte {
	return p.data[:p.dataLen]
}

// clear clears the parser parameters and command.
func (p *seqParser) clear() {
	if len(p.params) > 0 {
		p.params[0] = parser.MissingParam
	}
	p.paramsLen = 0
	p.cmd = 0
}

// State returns the current state of the parser.
func (p *seqParser) State() parser.State {
	return p.state
}

// Advance advances the parser using the given byte. It returns the action
// performed by the parser.
func (p *seqParser) Advance(b byte) parser.Action {
	switch p.state {
	case parser.Utf8State:
		// We handle UTF-8 here.
		return p.advanceUtf8(b)
	default:
		return p.advance(b)
	}
}

func (p *seqParser) collectRune(b byte) {
	if p.paramsLen >= utf8.UTFMax {
		return
	}

	shift := p.paramsLen * 8
	p.cmd &^= 0xff << shift
	p.cmd |= int(b) << shift
	p.paramsLen++
}

func (p *seqParser) advanceUtf8(b byte) parser.Action {
	// Collect UTF-8 rune bytes.
	p.collectRune(b)
	rw := utf8ByteLen(byte(p.cmd & 0xff))
	if rw == -1 {
		// We panic here because the first byte comes from the state machine,
		// if this panics, it means there is a bug in the state machine!
		panic("invalid rune") // unreachable
	}

	if p.paramsLen < rw {
		return parser.CollectAction
	}

	// We have enough bytes to decode the rune using unsafe
	if p.handler.Print != nil {
		p.handler.Print(p.Rune())
	}

	p.state = parser.GroundState
	p.paramsLen = 0

	return parser.PrintAction
}

// inStringState reports whether the parser is collecting the payload of a
// string sequence: OSC, DCS, SOS, PM or APC.
func (p *seqParser) inStringState() bool {
	switch p.state {
	case parser.OscStringState, parser.DcsStringState,
		parser.SosStringState, parser.PmStringState, parser.ApcStringState:
		return true
	default:
		return false
	}
}

func (p *seqParser) advance(b byte) parser.Action {
	// A byte with the top bit set is payload while a string sequence is being
	// collected, never a control.
	//
	// The transition table honours the 8-bit C1 controls, where 0x9C is a
	// String Terminator. Those bytes also occur inside UTF-8: U+2733 is
	// e2 9c b3, so its middle byte ended an OSC that had only just started.
	// What that produced was an empty title and the rest of the payload
	// printed at the cursor, which in a terminal UI is wherever that program
	// last put it. A window title of "✳ Say hello in three words" therefore
	// wrote "Say hello in three words" into the input box.
	//
	// That is the whole of the Dingbats block and a good deal besides:
	// anything whose encoding contains 9c, including ✳ and ✅, and the same
	// argument applies to 90, 9d and 9f. A UTF-8 terminal must not recognise
	// the 8-bit forms at all, which is what xterm does and what the scanner
	// for the other backend already says it does; see ghostty_scan.go. The
	// ESC-prefixed forms and BEL still terminate, and those are what programs
	// actually send.
	if b >= 0x80 && p.inStringState() {
		p.performAction(parser.PutAction, p.state, b)
		return parser.PutAction
	}

	// A parameter byte inside a CSI. In an SGR-heavy stream these are most of
	// the bytes, and the transition table maps every one of them to
	// ParamAction with the state unchanged (x/ansi parser/transition_table.go:
	// AddRange(0x30, 0x3B, CsiParamState, ParamAction, CsiParamState)). With
	// no state change, nothing below but performAction(ParamAction) has an
	// effect, so the update is done here directly. A full params buffer falls
	// through to the generic path, which ignores the byte.
	if p.state == parser.CsiParamState && b >= '0' && b <= ';' && p.paramsLen < len(p.params) {
		if b <= '9' {
			v := p.params[p.paramsLen]
			if v == parser.MissingParam {
				v = 0
			}
			p.params[p.paramsLen] = v*10 + int(b-'0')
			return parser.ParamAction
		}
		if b == ':' {
			p.params[p.paramsLen] |= parser.HasMoreFlag
		}
		p.paramsLen++
		if p.paramsLen < len(p.params) {
			p.params[p.paramsLen] = parser.MissingParam
		}
		return parser.ParamAction
	}

	state, action := parser.Table.Transition(p.state, b)

	// We need to clear the parser state if the state changes from EscapeState.
	// This is because when we enter the EscapeState, we don't get a chance to
	// clear the parser state. For example, when a sequence terminates with a
	// ST (\x1b\\ or \x9c), we dispatch the current sequence and transition to
	// EscapeState. However, the parser state is not cleared in this case and
	// we need to clear it here before dispatching the esc sequence.
	if p.state != state {
		if p.state == parser.EscapeState {
			p.performAction(parser.ClearAction, state, b)
		}
		if action == parser.PutAction &&
			p.state == parser.DcsEntryState && state == parser.DcsStringState {
			// XXX: This is a special case where we need to start collecting
			// non-string parameterized data i.e. doesn't follow the ECMA-48 §
			// 5.4.1 string parameters format.
			p.performAction(parser.StartAction, state, 0)
		}
	}

	// Handle special cases
	switch {
	case b == ansi.ESC && p.state == parser.EscapeState:
		// Two ESCs in a row
		p.performAction(parser.ExecuteAction, state, b)
	default:
		p.performAction(action, state, b)
	}

	p.state = state

	return action
}

func (p *seqParser) parseStringCmd() {
	// Try to parse the command
	for i := range p.dataLen {
		d := p.data[i]
		if d < '0' || d > '9' {
			break
		}
		if p.cmd == parser.MissingCommand {
			p.cmd = 0
		}
		p.cmd *= 10
		p.cmd += int(d - '0')
	}
}

// put appends one byte of string data. The buffer doubles towards the cap
// when it is full, and a byte past the cap is dropped, which is where the
// fixed buffer dropped it too.
func (p *seqParser) put(b byte) {
	if p.dataLen == len(p.data) {
		if p.dataLen >= p.dataCap {
			return
		}
		grown := make([]byte, min(max(2*len(p.data), seqParserInitialData), p.dataCap))
		copy(grown, p.data)
		p.data = grown
	}
	p.data[p.dataLen] = b
	p.dataLen++
}

func (p *seqParser) performAction(action parser.Action, state parser.State, b byte) {
	switch action {
	case parser.IgnoreAction:
		break

	case parser.ClearAction:
		p.clear()

	case parser.PrintAction:
		p.cmd = int(b)
		if p.handler.Print != nil {
			p.handler.Print(rune(b))
		}

	case parser.ExecuteAction:
		p.cmd = int(b)
		if p.handler.Execute != nil {
			p.handler.Execute(b)
		}

	case parser.PrefixAction:
		// Collect private prefix
		// we only store the last prefix
		p.cmd &^= 0xff << parser.PrefixShift
		p.cmd |= int(b) << parser.PrefixShift

	case parser.CollectAction:
		if state == parser.Utf8State {
			// Reset the UTF-8 counter
			p.paramsLen = 0
			p.collectRune(b)
		} else {
			// Collect intermediate bytes
			// we only store the last intermediate byte
			p.cmd &^= 0xff << parser.IntermedShift
			p.cmd |= int(b) << parser.IntermedShift
		}

	case parser.ParamAction:
		// Collect parameters
		if p.paramsLen >= len(p.params) {
			break
		}

		if b >= '0' && b <= '9' {
			if p.params[p.paramsLen] == parser.MissingParam {
				p.params[p.paramsLen] = 0
			}

			p.params[p.paramsLen] *= 10
			p.params[p.paramsLen] += int(b - '0')
		}

		if b == ':' {
			p.params[p.paramsLen] |= parser.HasMoreFlag
		}

		if b == ';' || b == ':' {
			p.paramsLen++
			if p.paramsLen < len(p.params) {
				p.params[p.paramsLen] = parser.MissingParam
			}
		}

	case parser.StartAction:
		p.dataLen = 0
		if p.state >= parser.DcsEntryState && p.state <= parser.DcsStringState {
			// Collect the command byte for DCS
			p.cmd |= int(b)
		} else {
			p.cmd = parser.MissingCommand
		}

	case parser.PutAction:
		switch p.state {
		case parser.OscStringState:
			if b == ';' && p.cmd == parser.MissingCommand {
				p.parseStringCmd()
			}
		}

		p.put(b)

	case parser.DispatchAction:
		// Increment the last parameter
		if p.paramsLen > 0 && p.paramsLen < len(p.params)-1 ||
			p.paramsLen == 0 && len(p.params) > 0 && p.params[0] != parser.MissingParam {
			p.paramsLen++
		}

		if p.state == parser.OscStringState && p.cmd == parser.MissingCommand {
			// Ensure we have a command for OSC
			p.parseStringCmd()
		}

		data := p.data[:p.dataLen]
		switch p.state {
		case parser.CsiEntryState, parser.CsiParamState, parser.CsiIntermediateState:
			p.cmd |= int(b)
			if p.handler.HandleCsi != nil {
				p.handler.HandleCsi(ansi.Cmd(p.cmd), p.Params())
			}
		case parser.EscapeState, parser.EscapeIntermediateState:
			p.cmd |= int(b)
			if p.handler.HandleEsc != nil {
				p.handler.HandleEsc(ansi.Cmd(p.cmd))
			}
		case parser.DcsEntryState, parser.DcsParamState, parser.DcsIntermediateState, parser.DcsStringState:
			if p.handler.HandleDcs != nil {
				p.handler.HandleDcs(ansi.Cmd(p.cmd), p.Params(), data)
			}
		case parser.OscStringState:
			if p.handler.HandleOsc != nil {
				p.handler.HandleOsc(p.cmd, data)
			}
		case parser.SosStringState:
			if p.handler.HandleSos != nil {
				p.handler.HandleSos(data)
			}
		case parser.PmStringState:
			if p.handler.HandlePm != nil {
				p.handler.HandlePm(data)
			}
		case parser.ApcStringState:
			if p.handler.HandleApc != nil {
				p.handler.HandleApc(data)
			}
		}
	}
}

func utf8ByteLen(b byte) int {
	if b <= 0b0111_1111 { // 0x00-0x7F
		return 1
	} else if b >= 0b1100_0000 && b <= 0b1101_1111 { // 0xC0-0xDF
		return 2
	} else if b >= 0b1110_0000 && b <= 0b1110_1111 { // 0xE0-0xEF
		return 3
	} else if b >= 0b1111_0000 && b <= 0b1111_0111 { // 0xF0-0xF7
		return 4
	}
	return -1
}
