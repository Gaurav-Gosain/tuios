package app

import (
	"fmt"
	"go/ast"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestEveryProgramTakesTheSharedOptions holds every tea.NewProgram in the
// module to ProgramOptions. Both sides come from the source: the construction
// sites are every call to tea.NewProgram, and the options a site may add on
// its own are the ones that differ by transport, which is the output writer.
//
// Three things are checked at each site:
//
//   - The function that calls tea.NewProgram calls ProgramOptions. A site that
//     types its own list is the bug this exists for: the motion filter shipped
//     on the local client only, and a served client composed a frame for every
//     pointer move.
//   - No tea.With* option outside ProgramOptions itself except WithOutput.
//   - Where a transport's MakeOptions is used, it comes before ProgramOptions.
//     WithFilter is a single slot and the last one set wins, and both wish and
//     sip set one.
func TestEveryProgramTakesTheSharedOptions(t *testing.T) {
	fset, files := moduleSource(t)

	sites := 0
	for path, file := range files {
		forEachFuncBody(file, func(fn string, body *ast.BlockStmt) {
			var newProgram, shared, makeOpts *ast.CallExpr
			for _, call := range directCalls(body) {
				pkg, name, ok := selectorCall(call)
				if !ok {
					if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "ProgramOptions" {
						shared = call
					}
					continue
				}
				switch {
				case pkg == "tea" && name == "NewProgram":
					newProgram = call
				case name == "ProgramOptions":
					shared = call
				case name == "MakeOptions":
					makeOpts = call
				}
			}
			if newProgram == nil {
				return
			}
			sites++
			where := fmt.Sprintf("%s:%d", path, fset.Position(newProgram.Pos()).Line)
			if shared == nil {
				t.Errorf("%s: tea.NewProgram in %s does not take app.ProgramOptions(); an option added there is missing here", where, fn)
				return
			}
			if makeOpts != nil && makeOpts.Pos() > shared.Pos() {
				t.Errorf("%s: MakeOptions comes after ProgramOptions in %s; its WithFilter replaces the motion filter", where, fn)
			}
		})

		if path == "internal/app/program_options.go" {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			pkg, name, ok := selectorCall(call)
			if !ok || pkg != "tea" || !strings.HasPrefix(name, "With") {
				return true
			}
			if name == "WithOutput" {
				return true
			}
			t.Errorf("%s:%d: tea.%s set outside ProgramOptions; the other clients do not get it",
				path, fset.Position(call.Pos()).Line, name)
			return true
		})
	}
	if sites < 5 {
		t.Fatalf("found %d tea.NewProgram sites, expected the local, attach, tape, SSH and web ones", sites)
	}
}

// TestProgramOptionsReachTheProgram builds a real program from the list and
// reads what it set. The fields are bubbletea's own and unexported, read by
// reflection: if a bubbletea upgrade renames one this fails loudly, which is
// the right answer for a guard that would otherwise pass on nothing.
func TestProgramOptionsReachTheProgram(t *testing.T) {
	p := tea.NewProgram(filterOS(t), ProgramOptions()...)
	v := reflect.ValueOf(p).Elem()

	if f := v.FieldByName("filter"); !f.IsValid() || f.IsNil() {
		t.Error("no event filter on the program; every pointer move would compose a frame")
	}
	// bubbletea clamps to its own ceiling of 120 in NewProgram, so a cap above
	// it reaches the program as 120. The assertion is that the cap was set at
	// all: the default it replaces is 60.
	wantFPS := min(int64(config.MaxFPSCap), 120)
	if f := v.FieldByName("fps"); !f.IsValid() || f.Int() != wantFPS {
		t.Errorf("program fps is %v, want %d", f, wantFPS)
	}
	if f := v.FieldByName("disableSignalHandler"); !f.IsValid() || !f.Bool() {
		t.Error("the program installs its own signal handler; the entry point already owns the process signals")
	}
}

// TestRemoteClientCannotBeSuspended pins what the filter took over from wish
// and sip when it replaced theirs: a served client has nothing to suspend, so
// a SuspendMsg comes back as a ResumeMsg. A local client keeps its suspend.
func TestRemoteClientCannotBeSuspended(t *testing.T) {
	o := filterOS(t)
	o.RemoteClient = true
	if _, ok := FilterMouseMotion(o, tea.SuspendMsg{}).(tea.ResumeMsg); !ok {
		t.Error("a remote client's SuspendMsg was passed through; the transport's filter used to turn it into a resume")
	}
	o.RemoteClient = false
	if _, ok := FilterMouseMotion(o, tea.SuspendMsg{}).(tea.SuspendMsg); !ok {
		t.Error("a local client's SuspendMsg was rewritten; ctrl+z must still suspend it")
	}
}

// TestMotionFilterFollowsTheBeam gives follow = "mouse" its own clause. The
// beam reads the pointer off LastMouseX/Y, which only a motion that reached
// Update sets, and it used to ride the link clause, so it stopped following over
// chrome and with links off.
func TestMotionFilterFollowsTheBeam(t *testing.T) {
	o := filterOS(t)
	o.UserConfig.Appearance.Links = "off"
	o.UserConfig.Spotlight.Follow = config.SpotlightFollowMouse
	// The dock row: chrome, not pane content, so no other clause claims it.
	overChrome := tea.MouseMotionMsg{X: 60, Y: 39}

	if FilterMouseMotion(o, overChrome) != nil {
		t.Fatal("motion over chrome passed with the beam off; the CPU guard is gone")
	}
	o.SetSpotlight(true)
	if FilterMouseMotion(o, overChrome) == nil {
		t.Error("motion over chrome was dropped with follow = mouse; the beam cannot follow")
	}
	o.LastMouseX, o.LastMouseY = overChrome.X, overChrome.Y
	if FilterMouseMotion(o, overChrome) != nil {
		t.Error("a motion inside the beam's current cell passed; it composes the same frame")
	}
}

// sweepOS is a served client with four tiled panes, the shape a pointer sweep
// meets on a remote desktop.
func sweepOS(tb testing.TB) *OS {
	tb.Helper()
	cfg := config.DefaultConfig()
	o := NewOS(OSOptions{Client: ClientSSH, UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	o.Width, o.Height = 120, 40
	o.EffectiveWidth, o.EffectiveHeight = 120, 40
	for i := range 4 {
		o.Windows = append(o.Windows, &terminal.Window{
			ID: fmt.Sprintf("pane-%d", i), X: (i % 2) * 60, Y: (i / 2) * 19, Width: 60, Height: 19, Workspace: 1,
		})
	}
	o.CurrentWorkspace, o.FocusedWindow = 1, 0
	return o
}

// BenchmarkPointerSweep is what the motion filter saves a served client, per
// motion event, measured rather than asserted. "unfiltered" is what the SSH
// and web servers did: every event reached Update and the renderer composed
// the frame after it. "filtered" is the shared list. The two rows are the two
// things a pointer crosses: chrome, which no clause claims, and pane content,
// which the link clause passes once per cell so links can underline.
func BenchmarkPointerSweep(b *testing.B) {
	rows := []struct {
		name string
		y    int
	}{
		{"chrome", 39},
		{"content", 5},
	}
	for _, row := range rows {
		for _, filtered := range []bool{false, true} {
			name := row.name + "/unfiltered"
			if filtered {
				name = row.name + "/filtered"
			}
			b.Run(name, func(b *testing.B) {
				o := sweepOS(b)
				frames := 0
				b.ReportAllocs()
				b.ResetTimer()
				for i := range b.N {
					var msg tea.Msg = tea.MouseMotionMsg{X: i % 120, Y: row.y}
					if filtered {
						msg = FilterMouseMotion(o, msg)
					}
					if msg == nil {
						continue
					}
					o.Update(msg)
					o.View()
					frames++
				}
				b.ReportMetric(float64(frames)/float64(b.N), "frames/event")
			})
		}
	}
}
