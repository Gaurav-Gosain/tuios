package session

import (
	"bytes"
	"encoding/gob"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

const wireCodecGob byte = 0

const wireCodecGobTagged byte = 2

const reqIDLen = 8

func encodePayload(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return nil, errfmt.Errorf("gob encode: %w", err)
	}
	return buf.Bytes(), nil
}

func withoutCardBodies(payload any) any {
	switch p := payload.(type) {
	case *StateSyncPayload:
		if p != nil && p.State != nil && len(p.State.Tasks) > 0 {
			c := *p
			c.State = stateWithoutCardBodies(p.State)
			return &c
		}
	case *AttachedPayload:
		if p != nil && p.State != nil && len(p.State.Tasks) > 0 {
			c := *p
			c.State = stateWithoutCardBodies(p.State)
			return &c
		}
	}
	return payload
}

func stateWithoutCardBodies(st *SessionState) *SessionState {
	c := *st
	c.Tasks = make([]TaskState, len(st.Tasks))
	for i, tk := range st.Tasks {
		c.Tasks[i] = tk.pushCopy()
	}
	return &c
}

func decodePayload(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	if err := checkGobNesting(data); err != nil {
		return errfmt.Errorf("gob decode: %w", err)
	}
	dec := gob.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(v); err != nil {
		return errfmt.Errorf("gob decode: %w", err)
	}
	return nil
}

func init() {
	gob.Register(HelloPayload{})
	gob.Register(WelcomePayload{})
	gob.Register(AttachPayload{})
	gob.Register(AttachedPayload{})
	gob.Register(NewPayload{})
	gob.Register(SessionInfo{})
	gob.Register(SessionListPayload{})
	gob.Register(KillPayload{})
	gob.Register(SessionEndedPayload{})
	gob.Register(ResizePayload{})
	gob.Register(ErrorPayload{})
	gob.Register(CreatePTYPayload{})
	gob.Register(PTYCreatedPayload{})
	gob.Register(ClosePTYPayload{})
	gob.Register(ResizePTYPayload{})
	gob.Register(PTYResizedPayload{})
	gob.Register(SubscribePTYPayload{})
	gob.Register(GetTerminalStatePayload{})
	gob.Register(TerminalStatePayload{})

	gob.Register(SessionState{})
	gob.Register(WindowState{})
	gob.RegisterName("github.com/Gaurav-Gosain/tuios/internal/session.TerminalState", TerminalState{})
	gob.Register(SerializedBSPTree{})
	gob.Register(SerializedBSPNode{})

	gob.Register(map[int]string{})
	gob.Register(map[string]int{})
	gob.Register(map[int]*SerializedBSPTree{})
	gob.Register(map[string]any{})
	gob.Register([]map[string]any{})
	gob.Register([]int{})
	gob.Register([]string{})

	gob.Register(ExecuteCommandPayload{})
	gob.Register(RemoteCommandPayload{})
	gob.Register(CommandResultPayload{})
	gob.Register(GetLogsPayload{})
	gob.Register(LogsDataPayload{})
}
