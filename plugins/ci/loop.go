package main

import (
	"os"
	"time"
	"unicode/utf8"
)

// A small event loop in the shape of Bubble Tea's. Bubble Tea keeps a
// ticker that wakes the program 60 times a second for as long as it runs,
// also without its renderer. This loop waits on a channel, so a pane where
// nothing happens costs nothing.

// Msg is an event: a key, a size, or the answer of a command.
type Msg = any

// Cmd runs in a goroutine of its own and gives one message.
type Cmd func() Msg

type batchMsg []Cmd

type quitMsg struct{}

func quit() Msg { return quitMsg{} }

// batch runs the commands at the same time.
func batch(cmds ...Cmd) Cmd {
	var some []Cmd
	for _, c := range cmds {
		if c != nil {
			some = append(some, c)
		}
	}
	switch len(some) {
	case 0:
		return nil
	case 1:
		return some[0]
	}
	return func() Msg { return batchMsg(some) }
}

// tick gives a message after d. Nothing wakes up before then.
func tick(d time.Duration, fn func(time.Time) Msg) Cmd {
	return func() Msg {
		time.Sleep(d)
		return fn(time.Now())
	}
}

type (
	sizeMsg  struct{ Width, Height int }
	focusMsg struct{}
	// keyMsg is a key: its name, such as "j", "G", "pgup" or "shift+tab",
	// and the text it types, if any.
	keyMsg struct{ name, text string }
)

func (k keyMsg) String() string { return k.name }

// run is the loop: it gives each message to the model, draws the frame,
// and runs the command the model returns.
func run(m *model, msgs chan Msg) *model {
	exec := func(c Cmd) {
		if c != nil {
			go func() { msgs <- c() }()
		}
	}
	exec(m.Init())
	for msg := range msgs {
		switch msg := msg.(type) {
		case nil:
			continue
		case quitMsg:
			return m
		case batchMsg:
			for _, c := range msg {
				exec(c)
			}
			continue
		}
		_, c := m.Update(msg)
		exec(c)
	}
	return m
}

// readKeys turns the bytes of the terminal into keys. The plugin asks for
// no keyboard protocol, so the keys come as the legacy sequences.
func readKeys(msgs chan<- Msg) {
	buf := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			msgs <- quitMsg{}
			return
		}
		for _, k := range decode(buf[:n]) {
			msgs <- k
		}
	}
}

var csiKeys = map[string]string{
	"A": "up", "B": "down", "C": "right", "D": "left", "H": "home", "F": "end", "Z": "shift+tab",
	"1~": "home", "4~": "end", "7~": "home", "8~": "end", "5~": "pgup", "6~": "pgdown", "3~": "delete",
}

var ctrlKeys = map[byte]string{
	3: "ctrl+c", 9: "tab", 13: "enter", 10: "enter", 8: "backspace", 127: "backspace",
	14: "ctrl+n", 16: "ctrl+p", 18: "ctrl+r",
}

func decode(b []byte) []Msg {
	var out []Msg
	for len(b) > 0 {
		c := b[0]
		switch {
		case c == 0x1b && len(b) == 1:
			// A lone escape at the end of a read is the escape key.
			return append(out, keyMsg{name: "esc"})
		case c == 0x1b && (b[1] == '[' || b[1] == 'O'):
			// A CSI or SS3 sequence: parameters, then a final byte.
			i := 2
			for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
				i++
			}
			if i == len(b) {
				return out
			}
			seq := string(b[2 : i+1])
			b = b[i+1:]
			switch seq {
			case "I":
				out = append(out, focusMsg{})
			default:
				if name, ok := csiKeys[seq]; ok {
					out = append(out, keyMsg{name: name})
				}
			}
			continue
		case c == 0x1b:
			// Alt and a key, which the plugin does not use.
			b = b[2:]
			continue
		case c == ' ':
			out = append(out, keyMsg{name: "space", text: " "})
		case c < 0x20 || c == 0x7f:
			if name, ok := ctrlKeys[c]; ok {
				out = append(out, keyMsg{name: name})
			}
		default:
			r, size := utf8.DecodeRune(b)
			s := string(r)
			out = append(out, keyMsg{name: s, text: s})
			b = b[size:]
			continue
		}
		b = b[1:]
	}
	return out
}
