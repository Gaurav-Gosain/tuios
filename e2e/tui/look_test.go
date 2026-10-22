package tuie2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"tuios.dev/shot"
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
	savePNG(t, cli.Screen(), shot.XTermPalette(), artifactDir(t), "take-over-prompt")
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

// lookCase is a scratch config for one case of TestLookTakeOverKeepsLaterEdits:
// its root, a look file per name, and a runner for the CLI.
type lookCase struct {
	t     *testing.T
	base  string
	looks map[string]string
}

func newLookCase(t *testing.T, main string, looks map[string]string) *lookCase {
	t.Helper()
	base := t.TempDir()
	useShippedLooks(base)
	writeConfig(t, base, main)
	c := &lookCase{t: t, base: base, looks: map[string]string{}}
	for name, body := range looks {
		path := filepath.Join(base, name+".look.toml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write the look %s: %v", name, err)
		}
		c.looks[name] = path
	}
	return c
}

// run runs tuios and fails the test when it fails.
func (c *lookCase) run(args ...string) string {
	c.t.Helper()
	out, err := tuiosCLI(c.t, c.base, args...)
	if err != nil {
		c.t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (c *lookCase) main() string { return readFileString(c.t, configPathIn(c.base)) }

func (c *lookCase) dropIn(name string) string {
	return filepath.Join(configDirIn(c.base), "config.d", name)
}

// TestLookTakeOverKeepsLaterEdits holds the take-over to the rule that it
// never loses what the person wrote: an edit made after one take-over and
// before the next, an edit made while tuios asks, a look file deleted by
// hand, and a second look applied over the first.
func TestLookTakeOverKeepsLaterEdits(t *testing.T) {
	const amber = "look = 1\nname = \"Amber\"\n\n[appearance]\ntheme = \"nord\"\nborder_style = \"rounded\"\nborder_focused_color = \"#ffb000\"\n"

	// A take-over that leaves a file empty, then a second take-over after
	// the person wrote the file again. The remove must keep what they wrote
	// second: the newer value and the key that is not a look key.
	t.Run("second take-over after an edit", func(t *testing.T) {
		c := newLookCase(t, "[appearance]\ngap = 0\n", map[string]string{"amber": amber})
		only := c.dropIn("zz-only.toml")
		if err := os.MkdirAll(filepath.Dir(only), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(only, []byte("[appearance]\nborder_focused_color = \"#ff0000\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		c.run("look", "apply", c.looks["amber"], "--yes", "--take-over")
		if got := readFileString(t, only); got != "" {
			t.Fatalf("the take-over did not leave zz-only.toml empty, so this cannot test it:\n%q", got)
		}
		edited := "[appearance]\nborder_focused_color = \"#00ff00\"\n\n[startup]\ntiled = true\n"
		if err := os.WriteFile(only, []byte(edited), 0o600); err != nil {
			t.Fatal(err)
		}
		c.run("look", "apply", c.looks["amber"], "--yes", "--take-over")
		out := c.run("look", "remove", "--yes")
		got := readFileString(t, only)
		if !strings.Contains(got, "tiled = true") {
			t.Fatalf("ASSERTION: the remove wrote back the copy from before the first take-over and lost startup.tiled:\n%s\n%s", got, out)
		}
		if !strings.Contains(got, `"#00ff00"`) || strings.Contains(got, `"#ff0000"`) {
			t.Fatalf("ASSERTION: the remove put back the older border_focused_color, not the one set again:\n%s\n%s", got, out)
		}
	})

	// An edit made while tuios asks its question stops the command, and the
	// edit stays.
	t.Run("edit during the question", func(t *testing.T) {
		c := newLookCase(t, "[appearance]\ntheme = \"dracula\"\n", map[string]string{"amber": amber})
		cli := startIn(t, c.base, startOpts{cols: 120, rows: 40, shippedLooks: true, animations: true,
			args: []string{"look", "apply", c.looks["amber"], "--take-over"}})
		if err := cli.WaitForText("Apply this look? [y/N]", uiTimeout); err != nil {
			t.Fatalf("the question is not on screen: %v\n%s", err, cli.Snapshot())
		}
		edited := "# edited while tuios asked\n[appearance]\ntheme = \"dracula\"\ngap = 2\n"
		saveConfigLikeAnEditor(t, c.base, edited)
		if err := cli.Type("y\r"); err != nil {
			t.Fatal(err)
		}
		if err := cli.WaitForText("changed while tuios asked", uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the take-over did not stop on the edit: %v\n%s", err, cli.Snapshot())
		}
		if got := c.main(); got != edited {
			t.Fatalf("ASSERTION: the take-over wrote over the edit made while it asked:\n%s", got)
		}
		if _, err := os.Stat(c.dropIn("look.toml")); err == nil {
			t.Fatal("ASSERTION: the stopped take-over still applied the look")
		}
		// A stopped command leaves nothing: no temporary file and no saved
		// values.
		for _, dir := range []string{configDirIn(c.base), filepath.Dir(c.dropIn("x"))} {
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".tmp") || e.Name() == "look.saved.json" {
					t.Fatalf("ASSERTION: the stopped take-over left %s in %s", e.Name(), dir)
				}
			}
		}
	})

	// The person deleted look.toml by hand. The remove must still put the
	// saved values back.
	t.Run("look file deleted by hand", func(t *testing.T) {
		mainBody := "# mine\n[appearance]\ntheme = \"dracula\" # keep\nborder_style = \"double\"\n"
		c := newLookCase(t, mainBody, map[string]string{"amber": amber})
		c.run("look", "apply", c.looks["amber"], "--yes", "--take-over")
		if strings.Contains(c.main(), "theme =") {
			t.Fatalf("the take-over kept the theme, so this cannot test it:\n%s", c.main())
		}
		if err := os.Remove(c.dropIn("look.toml")); err != nil {
			t.Fatal(err)
		}
		out := c.run("look", "remove", "--yes")
		if got := c.main(); got != mainBody {
			t.Fatalf("ASSERTION: with look.toml gone, the remove did not put config.toml back:\n%s\n--- output\n%s", got, out)
		}
		if _, err := os.Stat(c.dropIn("look.saved.json")); err == nil {
			t.Fatal("ASSERTION: the remove left the saved values behind")
		}
	})

	// Look B over look A's take-over: the keys A took and B does not set
	// come back to the person.
	t.Run("second look over a take-over", func(t *testing.T) {
		const plain = "look = 1\nname = \"Plain\"\n\n[appearance]\nborder_style = \"thick\"\n"
		c := newLookCase(t, "[appearance]\ntheme = \"dracula\"\nborder_style = \"double\"\n",
			map[string]string{"amber": amber, "plain": plain})
		c.run("look", "apply", c.looks["amber"], "--yes", "--take-over")
		c.run("look", "apply", c.looks["plain"], "--yes", "--take-over")
		out, err := tuiosCLI(t, c.base, "config", "origin", "appearance.theme")
		if err != nil || !originLine(out, "appearance.theme", "config.toml") || !strings.Contains(c.main(), `theme = "dracula"`) {
			t.Fatalf("ASSERTION: the second look did not give the theme back to config.toml: %v\n%s\n--- config.toml\n%s", err, out, c.main())
		}
		c.run("look", "remove", "--yes")
		if got := c.main(); !strings.Contains(got, `border_style = "double"`) || !strings.Contains(got, `theme = "dracula"`) {
			t.Fatalf("ASSERTION: the remove of the second look did not put border_style back:\n%s", got)
		}
	})
}

// galleryFeedLook is one look the fake gallery serves: the feed entry and the
// look file, which the real gallery serves from the same text.
type galleryFeedLook struct{ id, title, login, text string }

// fakeGallery serves a gallery feed and its looks on a loopback address, the
// way tuios.dev serves /gallery/feed and /r/<id>.toml. It records each path
// asked for.
func fakeGallery(t *testing.T, looks []galleryFeedLook) (*httptest.Server, *sync.Map) {
	t.Helper()
	type file struct {
		Label string `json:"label"`
		Text  string `json:"text"`
	}
	type entry struct {
		ID    string            `json:"id"`
		Login string            `json:"login"`
		Title string            `json:"title"`
		Files []file            `json:"files"`
		Look  map[string]string `json:"look"`
	}
	feed := struct {
		Version int     `json:"version"`
		Updated string  `json:"updated"`
		Entries []entry `json:"entries"`
	}{Version: 1, Updated: "2026-10-22T00:00:00Z"}
	byPath := map[string]string{}
	for _, l := range looks {
		feed.Entries = append(feed.Entries, entry{ID: l.id, Login: l.login, Title: l.title,
			Files: []file{{Label: "toml", Text: l.text}}, Look: map[string]string{"id": l.id}})
		byPath["/r/"+l.id+".toml"] = l.text
	}
	// An entry with no look is a screenshot alone. The list leaves it out.
	feed.Entries = append(feed.Entries, entry{ID: "999", Login: "someone", Title: "Just a picture"})
	feedJSON, err := json.Marshal(feed)
	if err != nil {
		t.Fatalf("encode the feed: %v", err)
	}
	hits := &sync.Map{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Store(r.URL.Path, true)
		if r.URL.Path == "/gallery/feed" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(feedJSON)
			return
		}
		if text, ok := byPath[r.URL.Path]; ok {
			_, _ = w.Write([]byte(text))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

// TestLookPickAppliesAGalleryLook serves a fake gallery and drives the look
// list: `tuios look list` names each look, a gallery that does not answer is
// one sentence and a failed exit, and `tuios look pick` on a terminal applies
// the look the person moves to, through the same questions as apply.
//
// How this could pass wrongly, written down first:
//   - The pick could apply the first look whatever the cursor says. The test
//     moves to the second look, and only the second sets a rounded border.
//   - The look could come from somewhere other than the gallery. The test
//     checks the server served the second look's file.
func TestLookPickAppliesAGalleryLook(t *testing.T) {
	base := t.TempDir()
	useShippedLooks(base)
	writeConfig(t, base, "[appearance]\nborder_style = \"double\"\n")
	alpha := "look = 1\nname = \"Alpha\"\n\n[appearance]\ngap = 1\n"
	beta := `look = 1
name = "Beta"

[appearance]
border_style = "rounded"

[theme]
id = "beta"
bg = "#101820"
fg = "#f2aa4c"
red = "#e05050"
green = "#50e050"
blue = "#5050e0"
`
	srv, hits := fakeGallery(t, []galleryFeedLook{
		{id: "101", title: "Alpha", login: "ann", text: alpha},
		{id: "202", title: "Beta", login: "bob", text: beta},
	})
	env := []string{"TUIOS_LOOK_GALLERY=" + srv.URL}

	out, err := tuiosCLIEnv(t, base, env, "look", "list")
	if err != nil {
		t.Fatalf("tuios look list: %v\n%s", err, out)
	}
	for _, want := range []string{"101", "Alpha", "ann", "202", "Beta", "bob"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ASSERTION: look list does not show %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Just a picture") {
		t.Fatalf("ASSERTION: look list shows an entry with no look:\n%s", out)
	}
	asJSON, err := tuiosCLIEnv(t, base, env, "look", "list", "--json")
	var listed []struct {
		ID, Name, Author string
		Colors           []string
	}
	if err != nil || json.Unmarshal([]byte(asJSON), &listed) != nil || len(listed) != 2 ||
		listed[1].Name != "Beta" || len(listed[1].Colors) != 5 || listed[1].Colors[0] != "#101820" {
		t.Fatalf("ASSERTION: look list --json is not the two looks with Beta's colours: %v\n%s", err, asJSON)
	}

	offline, err := tuiosCLIEnv(t, base, []string{"TUIOS_LOOK_GALLERY=http://127.0.0.1:1"}, "look", "list")
	if err == nil || strings.Count(strings.TrimSpace(offline), "\n") != 0 || !strings.Contains(offline, "cannot get the look list") {
		t.Fatalf("ASSERTION: with no gallery, look list did not fail with one sentence: %v\n%s", err, offline)
	}

	cli := startIn(t, base, startOpts{cols: 100, rows: 30, shippedLooks: true, animations: true, env: env,
		args: []string{"look", "pick"}})
	if err := cli.WaitForText("Choose a look", uiTimeout); err != nil {
		t.Fatalf("the look list is not on screen: %v\n%s", err, cli.Snapshot())
	}
	if err := cli.Type("j"); err != nil {
		t.Fatalf("move down: %v", err)
	}
	if err := cli.WaitForText("2 of 2", uiTimeout); err != nil {
		t.Fatalf("the cursor did not move to Beta: %v\n%s", err, cli.Snapshot())
	}
	saveArtifact(t, cli, artifactDir(t), "look-pick")
	savePNG(t, cli.Screen(), shot.XTermPalette(), artifactDir(t), "look-pick")
	if err := cli.Type("\r"); err != nil {
		t.Fatalf("choose Beta: %v", err)
	}
	for _, q := range []string{"Apply this look? [y/N]", "Let the look set these keys?"} {
		if err := cli.WaitForText(q, uiTimeout); err != nil {
			t.Fatalf("the question %q is not on screen: %v\n%s", q, err, cli.Snapshot())
		}
		if err := cli.Type("y\r"); err != nil {
			t.Fatalf("answer %q: %v", q, err)
		}
	}
	if err := cli.WaitForText("The look is applied", uiTimeout); err != nil {
		t.Fatalf("the pick did not apply the look: %v\n%s", err, cli.Snapshot())
	}
	saveArtifact(t, cli, artifactDir(t), "look-pick-applied")
	savePNG(t, cli.Screen(), shot.XTermPalette(), artifactDir(t), "look-pick-applied")
	if _, ok := hits.Load("/r/202.toml"); !ok {
		t.Fatal("ASSERTION: tuios did not download Beta's look file from the gallery")
	}
	show, err := tuiosCLI(t, base, "look", "show")
	if err != nil || !strings.Contains(show, "Beta") || !strings.Contains(show, "look-beta") {
		t.Fatalf("ASSERTION: look show does not name Beta and its theme: %v\n%s", err, show)
	}
	origin, err := tuiosCLI(t, base, "config", "origin", "appearance.border_style")
	if err != nil || !originLine(origin, "appearance.border_style", "look.toml") {
		t.Fatalf("ASSERTION: border_style does not come from the look after the take-over: %v\n%s", err, origin)
	}
	saveSyncArtifact(t, "look-pick-transcript.txt", out+"\n"+asJSON+"\n"+offline+"\n--- the pick\n"+cli.Snapshot()+"\n--- look show\n"+show)
}
