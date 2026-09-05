package app

import (
	"fmt"
	"go/ast"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// daemonClientHandlers lists the TUIClient's session-wide callbacks by
// reflection: every On* method that takes one handler. The per-PTY ones take a
// PTY id first and are registered per window elsewhere.
func daemonClientHandlers(t *testing.T) []string {
	t.Helper()
	typ := reflect.TypeOf(&session.TUIClient{})
	var names []string
	for i := range typ.NumMethod() {
		m := typ.Method(i)
		if !strings.HasPrefix(m.Name, "On") || m.Type.NumIn() != 2 {
			continue
		}
		names = append(names, m.Name)
	}
	if len(names) < 5 {
		t.Fatalf("found %d handlers on TUIClient: %v", len(names), names)
	}
	return names
}

// TestEveryDaemonHandlerIsWiredHere derives the handler set from the client's
// type and the registrations from the tree. Every handler is registered in
// WireDaemonClient, and nowhere else outside the session package. A handler
// added to the client and not wired here is a message no client hears; one
// wired by an entry point itself is the three-copies bug back.
func TestEveryDaemonHandlerIsWiredHere(t *testing.T) {
	handlers := daemonClientHandlers(t)
	fset, files := moduleSource(t)
	wired := map[string]bool{}
	for path, file := range files {
		if strings.HasPrefix(path, "internal/session/") {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !strings.HasPrefix(sel.Sel.Name, "On") {
				return true
			}
			for _, h := range handlers {
				if sel.Sel.Name != h {
					continue
				}
				if path == "internal/app/daemon_wiring.go" {
					wired[h] = true
				} else {
					t.Errorf("%s:%d registers %s itself; the other clients do not get what it does",
						path, fset.Position(call.Pos()).Line, h)
				}
			}
			return true
		})
	}
	for _, h := range handlers {
		if !wired[h] {
			t.Errorf("TUIClient.%s is registered by no client; whatever it carries is never heard", h)
		}
	}
}

// TestQueueClientEventKeepsTheNewest: a full queue displaces its oldest event.
// The newest count and the newest size are the ones that matter, and a
// resize storm that dropped its last event left the client at the wrong size.
func TestQueueClientEventKeepsTheNewest(t *testing.T) {
	o := &OS{ClientEventChan: make(chan ClientEvent, 2)}
	o.QueueClientEvent(ClientEvent{Type: "resize", Width: 1})
	o.QueueClientEvent(ClientEvent{Type: "resize", Width: 2})
	if !o.QueueClientEvent(ClientEvent{Type: "resize", Width: 3}) {
		t.Fatal("a third event fit a queue of two")
	}
	got := []int{(<-o.ClientEventChan).Width, (<-o.ClientEventChan).Width}
	if fmt.Sprint(got) != "[2 3]" {
		t.Errorf("queue holds widths %v, want [2 3]: the newest event was the one dropped", got)
	}
}

// TestRestoreAttachedSessionFiresTheAttachHook: the attach hook fires from the
// one attach sequence, so it fires for every client. The SSH and web copies
// of the sequence never fired it.
func TestRestoreAttachedSessionFiresTheAttachHook(t *testing.T) {
	cfg := config.DefaultConfig()
	o := NewOS(OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg), IsDaemonSession: true})
	fired := make(chan string, 1)
	o.HookManager.SetRunner(func(command string, _ hooks.Context) { fired <- command })
	o.HookManager.Register(hooks.AfterAttach, "note-attach")

	o.RestoreAttachedSession(nil)

	select {
	case cmd := <-fired:
		if cmd != "note-attach" {
			t.Errorf("fired %q, want the after-attach command", cmd)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the attach sequence fired no after-attach hook")
	}
}
