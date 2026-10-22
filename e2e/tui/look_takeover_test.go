package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLookTakeOverAndRemove drives `tuios look apply` against a config whose
// config.toml, and a config.d file after look.toml, already set keys the look
// sets. A look applied with --yes alone must leave every file as it was. The
// take-over, answered on a real terminal, must remove those keys, keep the
// comments, leave a read-only file alone and name it, and change what a
// running client draws. `tuios look remove` must put every file back byte for
// byte.
//
// How this could pass wrongly, written down first:
//   - The client could draw a rounded border for another reason. It draws the
//     double border of config.toml first, and the test waits for that.
//   - config.toml could come back equal because tuios never changed it. The
//     test checks the take-over removed theme and border_style first.
//   - The prompt could take the first answer for both questions. The test
//     waits for each question on screen before it answers it.
func TestLookTakeOverAndRemove(t *testing.T) {
	base := t.TempDir()
	useShippedLooks(base)
	mainBody := `# my config, by hand

[appearance]
# dracula since 2024
theme = "dracula"
border_style = "double" # I like double
gap = 0

[startup]
tiled = true
`
	writeConfig(t, base, mainBody)
	mineBody := "# mine\n[appearance]\nborder_focused_color = \"#ff0000\"\n"
	mine := writeConfigPart(t, base, filepath.Join("config.d", "zz-mine.toml"), mineBody)
	lockedBody := "# locked\n[appearance]\nborder_unfocused_color = \"#00ff00\"\n"
	locked := writeConfigPart(t, base, filepath.Join("config.d", "zz-locked.toml"), lockedBody)
	if err := os.Chmod(locked, 0o444); err != nil {
		t.Fatalf("make zz-locked.toml read-only: %v", err)
	}
	lookFile := filepath.Join(base, "amber.look.toml")
	if err := os.WriteFile(lookFile, []byte(`look = 1
name = "Amber test"

[appearance]
theme = "nord"
border_style = "rounded"
border_focused_color = "#ffb000"
border_unfocused_color = "#553300"
`), 0o600); err != nil {
		t.Fatalf("write the look: %v", err)
	}
	mainPath := configPathIn(base)
	saved := filepath.Join(configDirIn(base), "config.d", "look.saved.json")

	var transcript strings.Builder
	run := func(args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		transcript.WriteString("$ tuios " + strings.Join(args, " ") + "\n" + out + "\n")
		if err != nil {
			t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	themeFrom := func(file string) {
		t.Helper()
		out := run("config", "origin", "appearance.theme")
		if !originLine(out, "appearance.theme", file) {
			t.Fatalf("ASSERTION: appearance.theme does not come from %s:\n%s", file, out)
		}
	}

	term := startIn(t, base, startOpts{cols: 120, rows: 40, shippedLooks: true})
	waitBoot(t, term)
	newWindow(t, term)
	if err := term.WaitForText("╔", uiTimeout); err != nil {
		t.Fatalf("the double border of config.toml is not on screen: %v\n%s", err, term.Snapshot())
	}

	// --yes alone: the look goes in, and no file of the person's changes.
	out := run("look", "apply", lookFile, "--yes")
	if !strings.Contains(out, "These keys are set in a later file") {
		t.Fatalf("ASSERTION: apply did not name the hidden keys:\n%s", out)
	}
	if got := readFileString(t, mainPath); got != mainBody {
		t.Fatalf("ASSERTION: apply --yes changed config.toml:\n%s", got)
	}
	if got := readFileString(t, mine); got != mineBody {
		t.Fatalf("ASSERTION: apply --yes changed zz-mine.toml:\n%s", got)
	}
	if _, err := os.Stat(saved); err == nil {
		t.Fatal("ASSERTION: apply --yes saved values, so it took keys over")
	}
	themeFrom("config.toml")

	// The take-over, answered on a terminal.
	cli := startIn(t, base, startOpts{cols: 120, rows: 40, shippedLooks: true, animations: true,
		args: []string{"look", "apply", lookFile}})
	answer := func(question string) {
		t.Helper()
		if err := cli.WaitForText(question, uiTimeout); err != nil {
			t.Fatalf("the question %q is not on screen: %v\n%s", question, err, cli.Snapshot())
		}
		if err := cli.Type("y\r"); err != nil {
			t.Fatalf("answer %q: %v", question, err)
		}
	}
	answer("Apply this look? [y/N]")
	answer("Let the look set these keys?")
	if err := cli.WaitForText("The look is applied", uiTimeout); err != nil {
		t.Fatalf("the take-over did not finish: %v\n%s", err, cli.Snapshot())
	}
	if _, err := cli.WaitExit(uiTimeout); err != nil {
		t.Fatalf("look apply did not exit: %v", err)
	}
	prompt := cli.Snapshot()
	transcript.WriteString("--- the take-over on a terminal\n" + prompt + "\n")
	saveArtifact(t, cli, artifactDir(t), "take-over-prompt")
	if !strings.Contains(prompt, "cannot change config.d/zz-locked.toml") {
		t.Fatalf("ASSERTION: the take-over did not name the read-only file that keeps its key:\n%s", prompt)
	}

	taken := readFileString(t, mainPath)
	for _, gone := range []string{"theme =", "border_style ="} {
		if strings.Contains(taken, gone) {
			t.Fatalf("ASSERTION: the take-over kept %q in config.toml:\n%s", gone, taken)
		}
	}
	for _, kept := range []string{"# my config, by hand", "# dracula since 2024", "gap = 0", "tiled = true"} {
		if !strings.Contains(taken, kept) {
			t.Fatalf("ASSERTION: the take-over lost %q from config.toml:\n%s", kept, taken)
		}
	}
	if strings.Contains(readFileString(t, mine), "border_focused_color") {
		t.Fatalf("ASSERTION: the take-over kept border_focused_color in zz-mine.toml")
	}
	if got := readFileString(t, locked); got != lockedBody {
		t.Fatalf("ASSERTION: the take-over wrote the read-only zz-locked.toml:\n%s", got)
	}
	themeFrom("look.toml")
	if err := term.WaitForText("╭", configWatchTimeout); err != nil {
		t.Fatalf("ASSERTION: the running client does not draw the look's rounded border: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "client-after-take-over")

	// The remove puts every file back as it was.
	run("look", "remove", "--yes")
	if got := readFileString(t, mainPath); got != mainBody {
		t.Fatalf("ASSERTION: look remove did not put config.toml back byte for byte:\n--- got\n%s--- want\n%s", got, mainBody)
	}
	if got := readFileString(t, mine); got != mineBody {
		t.Fatalf("ASSERTION: look remove did not put zz-mine.toml back byte for byte:\n%s", got)
	}
	if _, err := os.Stat(saved); err == nil {
		t.Fatal("ASSERTION: look remove left the saved values behind")
	}
	themeFrom("config.toml")
	if err := term.WaitForText("╔", configWatchTimeout); err != nil {
		t.Fatalf("ASSERTION: the running client did not go back to the double border: %v\n%s", err, term.Snapshot())
	}

	// A key the person sets again after a take-over keeps the newer value.
	run("look", "apply", lookFile, "--yes", "--take-over")
	edited := strings.Replace(readFileString(t, mainPath), "gap = 0\n", "gap = 0\nborder_style = \"thick\"\n", 1)
	saveConfigLikeAnEditor(t, base, edited)
	out = run("look", "remove", "--yes")
	if !strings.Contains(out, "tuios keeps your newer values") || !strings.Contains(out, "appearance.border_style") {
		t.Fatalf("ASSERTION: look remove did not say it keeps the newer border_style:\n%s", out)
	}
	after := readFileString(t, mainPath)
	if !strings.Contains(after, `border_style = "thick"`) || strings.Contains(after, `border_style = "double"`) {
		t.Fatalf("ASSERTION: look remove did not keep the newer border_style:\n%s", after)
	}
	if !strings.Contains(after, `theme = "dracula"`) {
		t.Fatalf("ASSERTION: look remove did not put the theme back:\n%s", after)
	}
	themeFrom("config.toml")

	transcript.WriteString("--- config.toml at the end\n" + after)
	saveSyncArtifact(t, "look-take-over.txt", transcript.String())
	alive(t, term, "after the look came and went")
}
