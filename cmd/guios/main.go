// Command guios is the tuios desktop app: a native window, drawn with MyGo
// (github.com/egoist/mygo), over the tuios client model running in the same
// process.
//
// It is a binary of its own. The tuios binary never imports it or MyGo
// (internal/lint keeps it that way).
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// version is the build, set with -ldflags "-X main.version=...".
var version = "dev"

var (
	flagServer  = flag.String("server", "", "Use the daemon named NAME, as tuios --server does. TUIOS_SERVER sets it too")
	flagSession = flag.String("session", "", "Session to attach, created when missing. Empty attaches the session last active")
	flagTuios   = flag.String("tuios", "", "The tuios binary that starts the daemon. Empty finds it")
	flagTheme   = flag.String("theme", "", "Theme to use instead of the one the config names")
	flagControl = flag.String("control", "", "Unix socket for scripted checks (stats, dump, type, key, shot)")
	flagW       = flag.Int("width", 1440, "Window width")
	flagH       = flag.Int("height", 900, "Window height")
	flagGPU     = flag.String("gpu", "auto", "The GPU to draw with on Linux: auto, integrated or discrete")
	flagFont    = flag.Float64("font-size", 15, "Terminal font size")
	flagVersion = flag.Bool("version", false, "Print the version and exit")
	flagPprof   = flag.String("pprof", "", "Serve /debug/pprof on this address, for profiling")
	flagRender  = flag.String("render", "", "Draw the window into this PNG with no window, then exit")
	flagScale   = flag.Float64("render-scale", 2, "Scale of -render")
	flagRType   = flag.String("render-type", "", "Text to type into the focused pane before -render")
	flagFrames  = flag.Int("render-frames", 60, "Frames -render times")
)

// guiApp is the window's state. Everything here is read and written on the
// main thread, except what is atomic.
type guiApp struct {
	win    *mygo.Window
	cm     *clientModel
	roles  roles
	shaper *shaper
	views  map[string]*paneView

	// grid is the stage as the model last heard it: cols, rows, cell w, h.
	grid [4]int
	// geo is the stage's grid in device pixels, for the cards.
	geo geo

	services  *ui.Services
	hoverPane string
	preedit   string
	caret     ui.Rect

	frames  atomic.Int64
	started time.Time
	first   atomic.Int64 // first frame with pane text, Unix ns
}

func main() {
	flag.Parse()
	if *flagVersion {
		fmt.Println("guios", version)
		return
	}
	log.SetFlags(log.Lmicroseconds)
	preferGPU(*flagGPU)
	if *flagPprof != "" {
		go func() { log.Println(http.ListenAndServe(*flagPprof, nil)) }()
	}
	a := &guiApp{views: map[string]*paneView{}, started: time.Now()}
	if err := registerFonts(); err != nil {
		log.Fatal(err)
	}
	// The daemon to reach: TUIOS_SERVER, then --server, as every tuios
	// command chooses it. The tuios that starts a daemon inherits it.
	if err := serverscope.ApplyEnv(); err != nil {
		log.Fatal(err)
	}
	if *flagServer != "" {
		if err := serverscope.Apply(*flagServer); err != nil {
			log.Fatal(err)
		}
		_ = os.Setenv("TUIOS_SERVER", *flagServer)
	}
	tuios, err := findTuios(*flagTuios)
	if err != nil {
		log.Fatal(err)
	}
	if err := ensureDaemon(tuios); err != nil {
		log.Fatal(err)
	}
	a.shaper = newShaper(float32(*flagFont), 20/15.0)

	// The model starts at a guess of the window's grid, at scale 1. The
	// first frame tells it the real one.
	m := a.shaper.at(1)
	g := stageGeo(float32(*flagW), float32(*flagH), 1, m)
	cm, err := startModel(version, *flagSession, g.cols, g.rows, m.cellW, m.cellH, g.insets(m))
	if err != nil {
		log.Fatal(err)
	}
	a.cm = cm
	if *flagTheme != "" {
		_ = theme.Initialize(*flagTheme)
	}
	a.roles = currentRoles()
	a.grid = [4]int{g.cols, g.rows, m.cellW, m.cellH}

	if *flagRender != "" {
		if err := a.renderHeadless(*flagRender, *flagW, *flagH, float32(*flagScale), *flagRType, *flagFrames); err != nil {
			log.Fatal(err)
		}
		_ = cm.client.Detach()
		return
	}

	mygo.App.WhenReady(func() {
		opts := mygo.WindowOptions{
			Title:         "guios",
			Width:         *flagW,
			Height:        *flagH,
			TitleBarStyle: mygo.TitleBarHidden,
			Content:       ui.View(a.view),
		}
		if runtime.GOOS == "darwin" {
			opts.TrafficLightPosition = &mygo.Point{X: 16, Y: (int(bandH) - 12) / 2}
		} else {
			opts.HideWindowButtons = true
		}
		a.win = mygo.NewWindow(opts)
		if runtime.GOOS == "darwin" {
			// On macOS the menu bar is the app's. Linux windows get none.
			mygo.App.SetMenu(appMenu(a))
		}
		inv := a.win.Invalidate
		cm.m.notify.Store(&inv)
		clip := func(text string) {
			mygo.RunOnMain(func() {
				if a.services != nil {
					a.services.WriteClipboard(text)
				}
			})
		}
		cm.m.clipboard.Store(&clip)
		a.win.Invalidate()
		a.win.OnClosed(func() {
			cm.do(func(m *model) tea.Cmd { return tea.Quit })
			// Leave the session as a detach does, so the daemon counts this
			// client out at once.
			_ = cm.client.Detach()
			os.Exit(0)
		})
		if *flagControl != "" {
			go serveControl(*flagControl, a)
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// insets is the room around each pane's text in device pixels, as the model
// sizes each pane's PTY by it (terminal.PixelInsets).
func (g geo) insets(m metrics) terminal.PixelInsets {
	d := func(v float32) int { return int(v*m.scale + 0.5) }
	return terminal.PixelInsets{CellWidth: m.cellW, CellHeight: m.cellH, Top: d(paneTop), Left: d(panePad), Right: d(panePad), Bottom: d(panePad)}
}
