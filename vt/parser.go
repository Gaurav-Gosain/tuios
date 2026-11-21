package vt

import (
	"unicode/utf8"
	"unsafe"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

const seqParserInitialData = 4 * 1024

type seqParser struct {
	handler ansi.Handler

	params []int

	data []byte

	dataLen int

	dataCap int

	paramsLen int

	cmd int

	state byte

	oscBEL bool
}

func newSeqParser(dataCap int) *seqParser {
	p := new(seqParser)
	p.SetParamsSize(parser.MaxParamsSize)
	p.SetDataCap(dataCap)
	return p
}

func (p *seqParser) SetParamsSize(size int) {
	p.params = make([]int, size)
}

func (p *seqParser) SetDataCap(size int) {
	size = max(size, 0)
	p.dataCap = size
	p.data = make([]byte, min(size, seqParserInitialData))
	p.dataLen = 0
}

func (p *seqParser) SetHandler(h ansi.Handler) {
	p.handler = h
}

func (p *seqParser) Params() ansi.Params {
	return unsafe.Slice((*ansi.Param)(unsafe.Pointer(&p.params[0])), p.paramsLen)
}

func (p *seqParser) Rune() rune {
	rw := utf8ByteLen(byte(p.cmd & 0xff))
	if rw == -1 {
		return utf8.RuneError
	}
	r, _ := utf8.DecodeRune((*[utf8.UTFMax]byte)(unsafe.Pointer(&p.cmd))[:rw])
	return r
}

func (p *seqParser) Data() []byte {
	return p.data[:p.dataLen]
}

func (p *seqParser) clear() {
	if len(p.params) > 0 {
		p.params[0] = parser.MissingParam
	}
	p.paramsLen = 0
	p.cmd = 0
}

func (p *seqParser) State() parser.State {
	return p.state
}

func (p *seqParser) Advance(b byte) parser.Action {
	switch p.state {
	case parser.Utf8State:
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
	if b&0xc0 != 0x80 {
		p.abortRune()
		return p.advance(b)
	}
	p.collectRune(b)
	rw := utf8ByteLen(byte(p.cmd & 0xff))
	if rw == -1 {
		panic("invalid rune")
	}

	if p.paramsLen < rw {
		return parser.CollectAction
	}

	if p.handler.Print != nil {
		p.handler.Print(p.Rune())
	}

	p.state = parser.GroundState
	p.paramsLen = 0

	return parser.PrintAction
}

func (p *seqParser) abortRune() {
	if p.handler.Print != nil {
		p.handler.Print(utf8.RuneError)
	}
	p.state = parser.GroundState
	p.paramsLen = 0
	p.cmd = 0
}

func (p *seqParser) inStringState() bool {
	return isStringState(p.state)
}

func (p *seqParser) runeNeed() int {
	return utf8ByteLen(byte(p.cmd&0xff)) - p.paramsLen
}

func isStringState(st parser.State) bool {
	switch st {
	case parser.OscStringState, parser.DcsStringState,
		parser.SosStringState, parser.PmStringState, parser.ApcStringState:
		return true
	default:
		return false
	}
}

func (p *seqParser) advance(b byte) parser.Action {
	if b >= 0x80 && p.inStringState() {
		p.performAction(parser.PutAction, p.state, b)
		return parser.PutAction
	}

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

	if p.state != state {
		if p.state == parser.EscapeState {
			p.performAction(parser.ClearAction, state, b)
		}
		if action == parser.PutAction &&
			p.state == parser.DcsEntryState && state == parser.DcsStringState {
			p.performAction(parser.StartAction, state, 0)
		}
	}

	switch {
	case b == ansi.ESC && p.state == parser.EscapeState:
		p.performAction(parser.ExecuteAction, state, b)
	default:
		p.performAction(action, state, b)
	}

	p.state = state

	return action
}

func (p *seqParser) parseStringCmd() {
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

func (p *seqParser) releaseData() {
	if p.state != parser.GroundState || len(p.data) <= seqParserInitialData {
		return
	}
	p.data = make([]byte, min(p.dataCap, seqParserInitialData))
	p.dataLen = 0
}

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

func (p *seqParser) putRun(b []byte) {
	if need := p.dataLen + len(b); need > len(p.data) && len(p.data) < p.dataCap {
		size := max(len(p.data), seqParserInitialData)
		for size < need && size < p.dataCap {
			size *= 2
		}
		grown := make([]byte, min(size, p.dataCap))
		copy(grown, p.data[:p.dataLen])
		p.data = grown
	}
	n := copy(p.data[p.dataLen:], b)
	p.dataLen += n
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
		if p.state == parser.GroundState {
			p.cmd = int(b)
		}
		if p.handler.Execute != nil {
			p.handler.Execute(b)
		}

	case parser.PrefixAction:
		p.cmd &^= 0xff << parser.PrefixShift
		p.cmd |= int(b) << parser.PrefixShift

	case parser.CollectAction:
		if state == parser.Utf8State {
			p.paramsLen = 0
			p.collectRune(b)
		} else {
			p.cmd &^= 0xff << parser.IntermedShift
			p.cmd |= int(b) << parser.IntermedShift
		}

	case parser.ParamAction:
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
		if p.paramsLen > 0 && p.paramsLen < len(p.params)-1 ||
			p.paramsLen == 0 && len(p.params) > 0 && p.params[0] != parser.MissingParam {
			p.paramsLen++
		}

		if p.state == parser.OscStringState && p.cmd == parser.MissingCommand {
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
			p.oscBEL = b == ansi.BEL
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
	if b <= 0b0111_1111 {
		return 1
	} else if b >= 0b1100_0000 && b <= 0b1101_1111 {
		return 2
	} else if b >= 0b1110_0000 && b <= 0b1110_1111 {
		return 3
	} else if b >= 0b1111_0000 && b <= 0b1111_0111 {
		return 4
	}
	return -1
}
