package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func sheetStep(t *testing.T, term *tuitest.Terminal, name string, markers ...string) {
	t.Helper()
	for _, want := range markers {
		if err := term.WaitForText(want, 60*time.Second); err != nil {
			t.Fatalf("ASSERTION: the sheet never showed %q at %s: %v\n%s", want, name, err, term.Snapshot())
		}
	}
	railShot(t, term, name)
}

func waitAny(t *testing.T, term *tuitest.Terminal, markers ...string) string {
	t.Helper()
	var text string
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text = s.Text()
		for _, m := range markers {
			if strings.Contains(text, m) {
				return true
			}
		}
		return false
	}, 60*time.Second); err != nil {
		t.Fatalf("ASSERTION: the sheet never showed any of %q: %v\n%s", markers, err, term.Snapshot())
	}
	return text
}

func openAddSheet(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('p'))
	waitPaletteOpen(t, term, "for Add a machine")
	if err := term.SendKeys("Add a machine"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Add a machine", uiTimeout); err != nil {
		t.Fatalf("the palette has no Add a machine row: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Enter)
}

// TestAddMachineSheet adds buildbox from the sheet: type the address, test,
// copy this tuios, no start at boot, both ways with this machine's address,
// and Done with the link up.
func TestAddMachineSheet(t *testing.T) {
	here := remoteMachine(t)
	far := remoteMachine(t)
	farBin := filepath.Join(xdgDir(far, "HOME"), ".local", "bin", "tuios")
	env := []string{"TUIOS_SSH=" + writeSetupSSH(t, here, far)}
	if out, err := tuiosCLIEnv(t, here, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the daemon here: %v\n%s", err, out)
	}
	term := attachIn(t, here, "home", startOpts{cols: 120, rows: 40, env: env, shippedLooks: true})
	cfg := filepath.Join(xdgDir(here, "XDG_CONFIG_HOME"), "tuios", "config.toml")

	openAddSheet(t, term)
	sheetStep(t, term, "sheet-1-pick", "Address", "Tailnet", "ssh config", "user@host")
	if err := term.SendKeys("someone@buildbox"); err != nil {
		t.Fatal(err)
	}
	sendKeys(t, term, tuitest.Tab, tuitest.Tab)
	if err := term.SendKeys("someone@laptopbox"); err != nil {
		t.Fatal(err)
	}
	sheetStep(t, term, "sheet-1-pick-typed", "Test buildbox", "someone@laptopbox")
	sendKeys(t, term, tuitest.Enter)

	sheetStep(t, term, "sheet-2-test", "tuios is not installed", "Pick how to install tuios on buildbox.", "Copy this tuios")
	if _, err := os.Stat(farBin); err == nil {
		t.Fatalf("ASSERTION: tuios was installed on buildbox before the person picked how")
	}
	sendKeys(t, term, tuitest.Enter)

	if strings.Contains(waitAny(t, term, "when it boots?", "Add this machine on buildbox too?"), "when it boots?") {
		sheetStep(t, term, "sheet-3-boot", "when it boots?")
		sendKeys(t, term, "n")
	}

	sheetStep(t, term, "sheet-4-both-ways", "Add this machine on buildbox too?")
	if _, err := os.Stat(farBin); err != nil {
		t.Fatalf("ASSERTION: tuios is not at ~/.local/bin/tuios on buildbox: %v", err)
	}
	if data, _ := os.ReadFile(cfg); !strings.Contains(string(data), "[hosts.buildbox]") {
		t.Fatalf("ASSERTION: the add step did not write [hosts.buildbox]:\n%s", data)
	}
	sendKeys(t, term, "y")

	sheetStep(t, term, "sheet-5-done", "buildbox is ready.", "buildbox is added here.", "is added on buildbox. Its link is up.", "Open a session on buildbox")
	farCfg, _ := os.ReadFile(filepath.Join(xdgDir(far, "XDG_CONFIG_HOME"), "tuios", "config.toml"))
	if !strings.Contains(string(farCfg), "someone@laptopbox") {
		t.Fatalf("ASSERTION: buildbox has no entry for this machine:\n%s", farCfg)
	}
	waitForHostListing(t, here, func(s string) bool {
		return containsAll(s, "buildbox", "up")
	}, "the daemon here never reported buildbox up after the sheet")
	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "buildbox is ready.") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc did not close the sheet\n%s", term.Snapshot())
	}
}

// TestAddMachineSheetTailnet sets up buildbox from the Tailnet tab. The list
// shows buildbox without tuios and sleepy offline. The offline one is never
// dialed.
func TestAddMachineSheetTailnet(t *testing.T) {
	here := remoteMachine(t)
	far := remoteMachine(t)
	farBin := filepath.Join(xdgDir(far, "HOME"), ".local", "bin", "tuios")
	tsDir := filepath.Join(here, "tsbin")
	writeFakeTailscale(t, tsDir)
	env := []string{"TUIOS_SSH=" + writeTailnetSetupSSH(t, here, far), "PATH=" + tsDir + ":" + os.Getenv("PATH")}
	if out, err := tuiosCLIEnv(t, here, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the daemon here: %v\n%s", err, out)
	}
	term := attachIn(t, here, "home", startOpts{cols: 120, rows: 40, env: env, shippedLooks: true})

	openAddSheet(t, term)
	if err := term.WaitForText("user@host", uiTimeout); err != nil {
		t.Fatalf("the sheet did not open: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Tab, tuitest.Tab, tuitest.Tab)
	sheetStep(t, term, "sheet-tailnet-list", "Each ticked machine gets this version", "buildbox", "no tuios", "sleepy", "offline")
	sendKeys(t, term, "a", "b", "w")
	sheetStep(t, term, "sheet-tailnet-ticked", "[x]", "Set up 1 machine")
	sendKeys(t, term, tuitest.Enter)

	sheetStep(t, term, "sheet-tailnet-summary", "buildbox is ready.", "The link to buildbox is up.")
	if _, err := os.Stat(farBin); err != nil {
		t.Fatalf("ASSERTION: tuios is not installed on buildbox: %v", err)
	}
	if _, err := os.Stat(filepath.Join(here, "sleepy-dialed")); err == nil {
		t.Fatalf("ASSERTION: the offline machine was dialed")
	}
	waitForHostListing(t, here, func(s string) bool {
		return containsAll(s, "buildbox", "up")
	}, "the daemon here never reported buildbox up after the tailnet run")
}
