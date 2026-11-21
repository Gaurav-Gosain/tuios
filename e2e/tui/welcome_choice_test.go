package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

const welcomeQuestion = "How do you want to use tuios?"

// tmuxGlossary is the line the first start and the help panel show a tmux
// user.
const tmuxGlossary = "A window here is a tmux pane. A workspace is a tmux window."

// configText is the config.toml under base.
func configText(t *testing.T, base string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(base, "XDG_CONFIG_HOME", "tuios", "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	return string(data)
}

// welcomeShot saves the frame as text and as a PNG drawn by tuios.dev/shot.
func welcomeShot(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	saveFrame(t, term, name)
	savePNG(t, term.Screen(), shot.XTermPalette(), artifactDir(t), name)
}

// waitConfig waits until config.toml under base holds want.
func waitConfig(t *testing.T, term *tuitest.Terminal, base, want string) {
	t.Helper()
	if err := term.WaitFor(func(tuitest.Screen) bool {
		data, _ := os.ReadFile(filepath.Join(base, "XDG_CONFIG_HOME", "tuios", "config.toml"))
		return strings.Contains(string(data), want)
	}, uiTimeout); err != nil {
		t.Fatalf("config.toml never got %q:\n%s", want, configText(t, base))
	}
}

// TestWelcomeAsksOnTheFirstStart: with no config.toml, the welcome screen asks
// the question. 1 picks Simple, which writes profile = "simple", and the
// Simple dock comes up without a restart.
//
// Negative control: set welcomeAsk to false in NewOS. The question never
// shows.
func TestWelcomeAsksOnTheFirstStart(t *testing.T) {
	base := t.TempDir()
	// A true first start: no config.toml, not even the harness's own.
	useShippedLooks(base)
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	if err := term.WaitForText(welcomeQuestion, uiTimeout); err != nil {
		t.Fatalf("the first start did not ask: %v\n%s", err, term.Snapshot())
	}
	if !screenHas(term.Screen(), "Simple", "Keyboard", "tmux keys") {
		t.Fatalf("the question is missing a choice\n%s", term.Snapshot())
	}
	// A tmux user reads "window" as a tab, so the first start says what
	// the two words mean here.
	if !strings.Contains(term.Screen().Text(), tmuxGlossary) {
		t.Fatalf("the first start has no glossary line\n%s", term.Snapshot())
	}
	welcomeShot(t, term, "welcome-question")
	if err := term.SendKeys("1"); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	waitConfig(t, term, base, "profile = \"simple\"")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, rows := s.Size()
		return strings.Contains(s.Line(rows-1), "Settings") && strings.Contains(s.Line(rows-1), "Menu")
	}, uiTimeout); err != nil {
		t.Fatalf("the Simple dock never came up: %v\n%s", err, term.Snapshot())
	}
	welcomeShot(t, term, "welcome-simple")
}

// TestWelcomeEscKeepsTheKeyboard: esc closes the question, writes nothing,
// and the welcome screen shows today's hints.
func TestWelcomeEscKeepsTheKeyboard(t *testing.T) {
	base := t.TempDir()
	// A true first start: no config.toml, not even the harness's own.
	useShippedLooks(base)
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	if err := term.WaitForText(welcomeQuestion, uiTimeout); err != nil {
		t.Fatalf("the first start did not ask: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("\x1b"); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	if err := term.WaitForText(welcomeHint, uiTimeout); err != nil {
		t.Fatalf("esc did not bring back the hints: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), welcomeQuestion) {
		t.Fatalf("the question is still up after esc\n%s", term.Snapshot())
	}
	if strings.Contains(configText(t, base), "profile") {
		t.Fatalf("esc wrote a profile:\n%s", configText(t, base))
	}
}

// TestWelcomeClickPicksTmuxKeys: a click on the tmux keys choice writes the
// tmux preset with window mode off.
func TestWelcomeClickPicksTmuxKeys(t *testing.T) {
	base := t.TempDir()
	// A true first start: no config.toml, not even the harness's own.
	useShippedLooks(base)
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	if err := term.WaitForText(welcomeQuestion, uiTimeout); err != nil {
		t.Fatalf("the first start did not ask: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	_, rows := s.Size()
	for r := range rows {
		if i := strings.Index(s.Line(r), "tmux keys"); i >= 0 {
			leftClick(t, term, len([]rune(s.Line(r)[:i]))+2, r)
			break
		}
	}
	waitConfig(t, term, base, "preset = \"tmux\"")
	if !strings.Contains(configText(t, base), "window_mode = false") {
		t.Fatalf("tmux keys did not turn window mode off:\n%s", configText(t, base))
	}
}

// TestWelcomeDoesNotAskAnExistingInstall: a config.toml that is already there
// means the person chose before. The welcome screen shows its hints.
func TestWelcomeDoesNotAskAnExistingInstall(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\n")
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	if err := term.WaitForText(welcomeHint, uiTimeout); err != nil {
		t.Fatalf("no hints on the welcome screen: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), welcomeQuestion) {
		t.Fatalf("an existing install was asked\n%s", term.Snapshot())
	}
}
