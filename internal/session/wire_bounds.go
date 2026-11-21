package session

import (
	"fmt"
	"reflect"
	"strconv"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

func payloadCap(intSize int) int {
	if intSize < 64 {
		return 768 << 10
	}
	return 1 << 20
}

var (
	maxStateUpdateBytes = payloadCap(strconv.IntSize)

	maxCommandResultBytes = payloadCap(strconv.IntSize)
)

const (
	maxBSPDepth = 256

	maxBSPNodes = 16383

	maxResultDataDepth = 16

	maxDisplayTextBytes = 256
)

const maxFrameBytes = 16 * 1024 * 1024

const maxMasterLayoutFrame = 4 * 1024

const maxClientActivityFrame = 16

const maxRequestFrame = 64 * 1024

func daemonFrameLimit(t MessageType) uint32 {
	switch t {
	case MsgInput, MsgExecuteCommand:
		return maxFrameBytes
	case MsgUpdateState, MsgLayoutTree:
		return uint32(maxStateUpdateBytes) + 2
	case MsgCommandResult:
		return uint32(maxCommandResultBytes) + 2
	case MsgMasterLayout:
		return maxMasterLayoutFrame
	case MsgSidebarVisibility:
		return maxSidebarVisibilityFrame
	case MsgClientActivity, MsgSessionUsed:
		return maxClientActivityFrame
	case MsgClientTheme:
		return maxClientThemeFrame
	}
	return maxRequestFrame
}

// ClampDisplayText cuts s to maxDisplayTextBytes on a rune boundary. Session
// state holds display text through it on both sides, so a client and its
// daemon agree on what a pane is called.
func ClampDisplayText(s string) string {
	if len(s) <= maxDisplayTextBytes {
		return s
	}
	cut := maxDisplayTextBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func clampPushedText(s *SessionState) {
	for i := range s.Windows {
		s.Windows[i].Title = ClampDisplayText(s.Windows[i].Title)
		s.Windows[i].CustomName = ClampDisplayText(s.Windows[i].CustomName)
	}
}

// FrameTooLargeError is a frame over the limit for its message type. The
// reader has skipped its body, so the stream is still in step and the
// connection can go on.
type FrameTooLargeError struct {
	Type  MessageType
	Size  uint32
	Limit uint32
	// ReqID is the request id the frame carried, so the refusal can be sent
	// as the answer to it. Zero for an untagged frame.
	ReqID uint64
}

func (e *FrameTooLargeError) Error() string {
	return fmt.Sprintf("%s message of %d bytes is over the %d byte limit for that type and was not read",
		MessageTypeName(e.Type), e.Size, e.Limit)
}

var (
	errLayoutTooDeep  = errfmt.Errorf("layout tree is deeper than %d levels", maxBSPDepth)
	errLayoutTooLarge = errfmt.Errorf("layout trees hold more than %d nodes", maxBSPNodes)
	errResultTooDeep  = errfmt.Errorf("result data nests deeper than %d levels", maxResultDataDepth)
)

func validateSessionState(s *SessionState) error {
	if s == nil {
		return nil
	}
	nodes := 0
	for ws, tree := range s.WorkspaceTrees {
		if tree == nil {
			continue
		}
		if err := checkBSPTree(tree.Root, &nodes); err != nil {
			return errfmt.Errorf("workspace %d: %w", ws, err)
		}
	}
	return nil
}

func checkBSPTree(root *SerializedBSPNode, nodes *int) error {
	type item struct {
		n     *SerializedBSPNode
		depth int
	}
	if root == nil {
		return nil
	}
	stack := []item{{root, 0}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if it.depth > maxBSPDepth {
			return errLayoutTooDeep
		}
		*nodes++
		if *nodes > maxBSPNodes {
			return errLayoutTooLarge
		}
		if it.n.Left != nil {
			stack = append(stack, item{it.n.Left, it.depth + 1})
		}
		if it.n.Right != nil {
			stack = append(stack, item{it.n.Right, it.depth + 1})
		}
	}
	return nil
}

func checkResultData(data map[string]any) error {
	type item struct {
		v     reflect.Value
		depth int
	}
	if data == nil {
		return nil
	}
	stack := []item{{reflect.ValueOf(data), 1}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		v := it.v
		for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
			if v.IsNil() {
				break
			}
			v = v.Elem()
		}
		switch v.Kind() {
		case reflect.Map, reflect.Slice, reflect.Array, reflect.Struct:
		default:
			continue
		}
		if it.depth > maxResultDataDepth {
			return errResultTooDeep
		}
		switch v.Kind() {
		case reflect.Map:
			iter := v.MapRange()
			for iter.Next() {
				stack = append(stack, item{iter.Value(), it.depth + 1})
			}
		case reflect.Slice, reflect.Array:
			if v.Type().Elem().Kind() == reflect.Uint8 {
				continue
			}
			for i := 0; i < v.Len(); i++ {
				stack = append(stack, item{v.Index(i), it.depth + 1})
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					stack = append(stack, item{v.Field(i), it.depth + 1})
				}
			}
		}
	}
	return nil
}

const maxPushOriginLen = 64
