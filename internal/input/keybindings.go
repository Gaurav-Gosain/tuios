package input

import (
	"runtime"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"tuios.dev/vt"
)

func runtimeIsDarwin() bool {
	return darwinHost
}

var darwinHost = runtime.GOOS == "darwin" || config.E2EPlatformDarwin()

var ctrlKeyMap = map[rune]byte{
	'@':  0x00,
	'[':  0x1B,
	'\\': 0x1C,
	']':  0x1D,
	'^':  0x1E,
	'_':  0x1F,
	'/':  0x1F,
	'?':  0x7F,
}

var specialKeyMap = map[rune][]byte{
	tea.KeyEnter:     {'\r'},
	tea.KeyTab:       {'\t'},
	tea.KeyBackspace: {0x7f},
	tea.KeyEscape:    {0x1b},
	tea.KeySpace:     {' '},
	tea.KeyDelete:    {0x1b, '[', '3', '~'},
	tea.KeyInsert:    {0x1b, '[', '2', '~'},
	tea.KeyPgUp:      {0x1b, '[', '5', '~'},
	tea.KeyPgDown:    {0x1b, '[', '6', '~'},
}

var functionKeyMap = map[rune][]byte{
	tea.KeyF1:  {0x1b, 'O', 'P'},
	tea.KeyF2:  {0x1b, 'O', 'Q'},
	tea.KeyF3:  {0x1b, 'O', 'R'},
	tea.KeyF4:  {0x1b, 'O', 'S'},
	tea.KeyF5:  {0x1b, '[', '1', '5', '~'},
	tea.KeyF6:  {0x1b, '[', '1', '7', '~'},
	tea.KeyF7:  {0x1b, '[', '1', '8', '~'},
	tea.KeyF8:  {0x1b, '[', '1', '9', '~'},
	tea.KeyF9:  {0x1b, '[', '2', '0', '~'},
	tea.KeyF10: {0x1b, '[', '2', '1', '~'},
	tea.KeyF11: {0x1b, '[', '2', '3', '~'},
	tea.KeyF12: {0x1b, '[', '2', '4', '~'},
}

func getRawKeyBytesWithMode(msg tea.KeyPressMsg, applicationCursorKeys bool) []byte {
	key := legacyKey(msg.Key())

	modMask := tea.ModShift | tea.ModAlt | tea.ModCtrl
	actualMod := key.Mod & modMask

	if actualMod != 0 {
		if actualMod&tea.ModShift != 0 && key.Code == tea.KeyTab {
			return []byte{0x1b, '[', 'Z'}
		}

		if actualMod&tea.ModCtrl != 0 && key.Code >= 0x80 && key.BaseCode > 0x20 && key.BaseCode < 0x7f {
			key.Code = key.BaseCode
		}

		if actualMod&tea.ModCtrl != 0 {
			switch key.Code {
			case tea.KeySpace:
				return []byte{0x00}
			case tea.KeyBackspace:
				return []byte{0x08}
			case tea.KeyTab:
				return []byte{0x09}
			case tea.KeyEnter:
				return []byte{0x0A}
			case tea.KeyEscape:
				return []byte{0x1B}
			}

			if key.Code >= 'a' && key.Code <= 'z' {
				return []byte{byte(key.Code - 'a' + 1)}
			}
			if key.Code >= 'A' && key.Code <= 'Z' {
				return []byte{byte(key.Code - 'A' + 1)}
			}

			if ctrlCode, ok := ctrlKeyMap[key.Code]; ok {
				return []byte{ctrlCode}
			}
		}

		if actualMod&tea.ModAlt != 0 {
			switch key.Code {
			case tea.KeyBackspace:
				return []byte{0x1b, 0x7f}
			default:
				if key.Text != "" && len(key.Text) == 1 {
					return []byte{0x1b, key.Text[0]}
				}
				if key.Code >= 32 && key.Code <= 126 {
					return []byte{0x1b, byte(key.Code)}
				}
			}
		}

		if modSeq := handleModifierKeysWithMod(key, actualMod); len(modSeq) > 0 {
			return modSeq
		}
	}

	switch key.Code {
	case tea.KeyUp:
		if applicationCursorKeys {
			return []byte{0x1b, 'O', 'A'}
		}
		return []byte{0x1b, '[', 'A'}
	case tea.KeyDown:
		if applicationCursorKeys {
			return []byte{0x1b, 'O', 'B'}
		}
		return []byte{0x1b, '[', 'B'}
	case tea.KeyRight:
		if applicationCursorKeys {
			return []byte{0x1b, 'O', 'C'}
		}
		return []byte{0x1b, '[', 'C'}
	case tea.KeyLeft:
		if applicationCursorKeys {
			return []byte{0x1b, 'O', 'D'}
		}
		return []byte{0x1b, '[', 'D'}
	case tea.KeyHome:
		if applicationCursorKeys {
			return []byte{0x1b, 'O', 'H'}
		}
		return []byte{0x1b, '[', 'H'}
	case tea.KeyEnd:
		if applicationCursorKeys {
			return []byte{0x1b, 'O', 'F'}
		}
		return []byte{0x1b, '[', 'F'}
	case tea.KeyBegin:
		if applicationCursorKeys {
			return []byte{0x1b, 'O', 'E'}
		}
		return []byte{0x1b, '[', 'E'}
	}

	if seq, ok := specialKeyMap[key.Code]; ok {
		return seq
	}

	if seq, ok := functionKeyMap[key.Code]; ok {
		return seq
	}

	if key.Text != "" {
		return []byte(key.Text)
	}

	if key.Code >= 32 && key.Code <= 126 {
		return []byte{byte(key.Code)}
	}

	return []byte{}
}

func handleModifierKeysWithMod(key tea.Key, mod tea.KeyMod) []byte {
	if fnSeq := getFunctionKeySequence(key.Code, getModParam(mod)); fnSeq != nil {
		return fnSeq
	}

	if cursorSeq := getCursorSequence(key.Code); cursorSeq != nil {
		modParam := getModParam(mod)
		if modParam > 1 {
			result := make([]byte, 0, 8)
			result = append(result, 0x1b, '[', '1', ';', byte('0'+modParam))
			result = append(result, cursorSeq[len(cursorSeq)-1])
			return result
		}
	}

	if num, ok := tildeKeyNumbers[key.Code]; ok {
		if modParam := getModParam(mod); modParam > 1 {
			return buildCSISequence(num, modParam)
		}
	}

	return []byte{}
}

var tildeKeyNumbers = map[rune]int{
	tea.KeyInsert: 2,
	tea.KeyDelete: 3,
	tea.KeyPgUp:   5,
	tea.KeyPgDown: 6,
}

func legacyKey(key tea.Key) tea.Key {
	if code, ok := keypadLegacyCodes[key.Code]; ok {
		key.Code = code
		return key
	}
	if key.Code >= tea.KeyF13 && key.Code <= tea.KeyF63 {
		n := key.Code - tea.KeyF1
		key.Code = tea.KeyF1 + n%12
		key.Mod |= [...]tea.KeyMod{
			0,
			tea.ModShift,
			tea.ModCtrl,
			tea.ModCtrl | tea.ModShift,
			tea.ModAlt,
			tea.ModAlt | tea.ModShift,
		}[n/12]
	}
	return key
}

var keypadLegacyCodes = map[rune]rune{
	tea.KeyKp0: '0', tea.KeyKp1: '1', tea.KeyKp2: '2', tea.KeyKp3: '3', tea.KeyKp4: '4',
	tea.KeyKp5: '5', tea.KeyKp6: '6', tea.KeyKp7: '7', tea.KeyKp8: '8', tea.KeyKp9: '9',
	tea.KeyKpEqual: '=', tea.KeyKpMultiply: '*', tea.KeyKpPlus: '+', tea.KeyKpComma: ',',
	tea.KeyKpMinus: '-', tea.KeyKpDecimal: '.', tea.KeyKpDivide: '/', tea.KeyKpSep: ',',
	tea.KeyKpEnter:  tea.KeyEnter,
	tea.KeyKpUp:     tea.KeyUp,
	tea.KeyKpDown:   tea.KeyDown,
	tea.KeyKpLeft:   tea.KeyLeft,
	tea.KeyKpRight:  tea.KeyRight,
	tea.KeyKpHome:   tea.KeyHome,
	tea.KeyKpEnd:    tea.KeyEnd,
	tea.KeyKpPgUp:   tea.KeyPgUp,
	tea.KeyKpPgDown: tea.KeyPgDown,
	tea.KeyKpInsert: tea.KeyInsert,
	tea.KeyKpDelete: tea.KeyDelete,
	tea.KeyKpBegin:  tea.KeyBegin,
}

func getModParam(mod tea.KeyMod) int {
	modMask := tea.ModShift | tea.ModAlt | tea.ModCtrl
	mod &= modMask

	modParam := 1
	if mod&tea.ModShift != 0 {
		modParam++
	}
	if mod&tea.ModAlt != 0 {
		modParam += 2
	}
	if mod&tea.ModCtrl != 0 {
		modParam += 4
	}
	return modParam
}

func getCursorSequence(code rune) []byte {
	switch code {
	case tea.KeyUp:
		return []byte{0x1b, '[', 'A'}
	case tea.KeyDown:
		return []byte{0x1b, '[', 'B'}
	case tea.KeyRight:
		return []byte{0x1b, '[', 'C'}
	case tea.KeyLeft:
		return []byte{0x1b, '[', 'D'}
	case tea.KeyHome:
		return []byte{0x1b, '[', 'H'}
	case tea.KeyEnd:
		return []byte{0x1b, '[', 'F'}
	case tea.KeyBegin:
		return []byte{0x1b, '[', 'E'}
	}
	return nil
}

func getFunctionKeySequence(code rune, modParam int) []byte {
	var baseSeq []byte

	switch code {
	case tea.KeyF1:
		baseSeq = []byte{0x1b, 'O', 'P'}
	case tea.KeyF2:
		baseSeq = []byte{0x1b, 'O', 'Q'}
	case tea.KeyF3:
		baseSeq = []byte{0x1b, 'O', 'R'}
	case tea.KeyF4:
		baseSeq = []byte{0x1b, 'O', 'S'}
	case tea.KeyF5:
		return buildCSISequence(15, modParam)
	case tea.KeyF6:
		return buildCSISequence(17, modParam)
	case tea.KeyF7:
		return buildCSISequence(18, modParam)
	case tea.KeyF8:
		return buildCSISequence(19, modParam)
	case tea.KeyF9:
		return buildCSISequence(20, modParam)
	case tea.KeyF10:
		return buildCSISequence(21, modParam)
	case tea.KeyF11:
		return buildCSISequence(23, modParam)
	case tea.KeyF12:
		return buildCSISequence(24, modParam)
	default:
		return nil
	}

	if modParam > 1 && baseSeq != nil {
		result := []byte{0x1b, '[', '1', ';', byte('0' + modParam)}
		result = append(result, baseSeq[len(baseSeq)-1])
		return result
	}

	return baseSeq
}

func buildCSISequence(num, modParam int) []byte {
	seq := []byte{0x1b, '['}

	if num >= 10 {
		seq = append(seq, byte('0'+num/10), byte('0'+num%10))
	} else {
		seq = append(seq, byte('0'+num))
	}

	if modParam > 1 {
		seq = append(seq, ';', byte('0'+modParam))
	}

	seq = append(seq, '~')
	return seq
}

func vtKeyFromBubbletea(msg tea.KeyPressMsg) vt.KeyPressEvent {
	key := msg.Key()
	return vt.KeyPressEvent{
		Code:        key.Code,
		Text:        key.Text,
		Mod:         vt.KeyMod(key.Mod),
		ShiftedCode: shiftedCode(key),
		BaseCode:    key.BaseCode,
		IsRepeat:    key.IsRepeat,
	}
}

func paneMsg(msg tea.KeyPressMsg, o *app.OS) tea.KeyPressMsg {
	if msg.BaseCode == 0 {
		msg.BaseCode = o.HostBaseCode(msg)
	}
	return msg
}

func shiftedCode(key tea.Key) rune {
	if key.BaseCode == 0 || key.ShiftedCode != key.BaseCode {
		return key.ShiftedCode
	}
	if key.Mod&tea.ModShift == 0 {
		return 0
	}
	if up := unicode.ToUpper(key.Code); up != key.Code {
		return up
	}
	if r := []rune(key.Text); len(r) == 1 && r[0] != key.Code {
		return r[0]
	}
	return 0
}
