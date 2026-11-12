package integration

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// The Rex integration: tuios.lua in Rex's config folder, required from
// init.lua, so Rex's command palette has tuios actions. Rex is a terminal, not
// a harness, so it is not one of Targets: it has no hooks and reports no
// state. integration install, status and uninstall take it by the name rex.
//
// integrations/rex/tuios.lua is the source. assets/rex/tuios.lua is a copy
// for the archive, and TestRexLuaMatchesItsSource keeps the two the same.

// RexID is the name integration install, status and uninstall take for Rex.
const RexID = "rex"

// rexLuaAsset is tuios.lua in assetArchive.
const rexLuaAsset = "assets/rex/tuios.lua"

// rexMarker ends the line install adds to init.lua. Uninstall removes only
// lines that end with it.
const rexMarker = "-- added by tuios integration"

// rexRequireLine is the line install adds to init.lua.
const rexRequireLine = `require("tuios").setup{} ` + rexMarker

// rexHeader starts every tuios.lua tuios shipped, the ones before M.version
// too, so a copy tuios wrote is told from a file of the person's own.
const rexHeader = "-- tuios for Rex:"

// rexRequireRE finds a require of tuios in init.lua that is not commented
// out: the person's own line or the marked one.
var rexRequireRE = regexp.MustCompile(`(?m)^[ \t]*[^-\n]*require[ \t]*\(?[ \t]*["']tuios["']`)

// rexVersionRE reads M.version from a tuios.lua.
var rexVersionRE = regexp.MustCompile(`(?m)^M\.version[ \t]*=[ \t]*([0-9]+)`)

// RexRunner runs the rex CLI with args and returns what it printed on
// stdout. An error carries what it printed on stderr.
type RexRunner func(args ...string) (string, error)

// RexLua is the tuios.lua this build ships.
func RexLua() []byte { return assets()[rexLuaAsset] }

// rexLuaVersion reads M.version from a tuios.lua: 0 for a copy from before
// it had one, -1 for a file that is not tuios's.
func rexLuaVersion(data []byte) int {
	if !bytes.HasPrefix(data, []byte(rexHeader)) {
		return -1
	}
	m := rexVersionRE.FindSubmatch(data)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

// RexConfigPath is init.lua as Rex finds it: REX_CONFIG, else rex/init.lua
// in the account's configuration folder.
func RexConfigPath(env Env) string {
	if p := strings.TrimSpace(env.env("REX_CONFIG")); p != "" {
		return p
	}
	if runtime.GOOS == "windows" {
		if d := env.env("AppData"); d != "" {
			return filepath.Join(d, "rex", "init.lua")
		}
	}
	if d := strings.TrimSpace(env.env("XDG_CONFIG_HOME")); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "rex", "init.lua")
	}
	return filepath.Join(env.Home, ".config", "rex", "init.lua")
}

// RexCheck is what tuios_check reports from inside Rex.
type RexCheck struct {
	// Tuios is the tuios the actions run, as the Rex server finds it.
	Tuios   string `json:"tuios"`
	Version string `json:"version"`
	// LuaVersion is M.version of the tuios.lua Rex loaded.
	LuaVersion int `json:"lua_version"`
	// Session is the tuios session of the focused Rex pane, if any.
	Session     string `json:"session"`
	SessionNote string `json:"session_note"`
	// Agent is the default harness of the start action, and whether
	// start-agent would find it.
	Agent          string `json:"agent"`
	AgentInstalled *bool  `json:"agent_installed"`
	AgentNote      string `json:"agent_note"`
}

// RexStatus is what integration status says about Rex.
type RexStatus struct {
	// Config is init.lua, Lua the tuios.lua beside it.
	Config string
	Lua    string
	// LuaState is missing, current, older, edited or foreign.
	LuaState   string
	LuaVersion int
	// Line is marked (install's), by_hand (the person's own) or missing.
	Line string
	// Loaded says the Rex server lists the tuios actions. LoadedNote says
	// why it could not be asked.
	Loaded     bool
	LoadedNote string
	// Check is tuios_check's answer, nil when it could not run; CheckNote
	// says why.
	Check     *RexCheck
	CheckNote string
}

// RexResult is what install or uninstall did.
type RexResult struct {
	Config string
	Lua    string
	// Notes are lines a person reads, in order.
	Notes []string
	Check *RexCheck
}

// rexLuaState sorts the tuios.lua at path against the one this build ships.
func rexLuaState(path string) (state string, version int, data []byte, err error) {
	data, err = readOptional(path)
	if err != nil {
		return "", 0, nil, err
	}
	shipped := RexLua()
	switch v := rexLuaVersion(data); {
	case data == nil:
		return "missing", 0, nil, nil
	case bytes.Equal(data, shipped):
		return "current", v, data, nil
	case v < 0:
		return "foreign", v, data, nil
	case v < rexLuaVersion(shipped):
		return "older", v, data, nil
	default:
		return "edited", v, data, nil
	}
}

// rexLine sorts init.lua's require of tuios.
func rexLine(init []byte) string {
	for _, line := range strings.Split(string(init), "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), rexMarker) {
			return "marked"
		}
	}
	if rexRequireRE.Match(init) {
		return "by_hand"
	}
	return "missing"
}

// RexInstall writes tuios.lua next to init.lua and adds the require line,
// then has Rex check the config, puts both files back when the check fails,
// reloads the server and runs tuios_check as the proof. force replaces a
// tuios.lua the person edited or wrote. A line of the person's own that
// requires tuios is left as it is.
func RexInstall(env Env, rex RexRunner, force bool) (RexResult, error) {
	cfg := RexConfigPath(env)
	lua := filepath.Join(filepath.Dir(cfg), "tuios.lua")
	res := RexResult{Config: cfg, Lua: lua}
	state, version, oldLua, err := rexLuaState(lua)
	if err != nil {
		return res, err
	}
	switch state {
	case "edited", "foreign":
		if !force {
			what := "is not one tuios wrote"
			if state == "edited" {
				what = "has changes of your own"
			}
			return res, errfmt.Errorf("%s %s, so it was left as it is. Run with --force to replace it; the file as it is now is kept as %s%s", lua, what, lua, BackupSuffix)
		}
	case "older":
		res.Notes = append(res.Notes, "Replaced tuios.lua version "+strconv.Itoa(version)+" with version "+strconv.Itoa(rexLuaVersion(RexLua()))+".")
	}
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		return res, errfmt.Errorf("failed to make %s: %w", filepath.Dir(cfg), err)
	}
	oldInit, err := readOptional(cfg)
	if err != nil {
		return res, err
	}
	luaChanged := state != "current"
	if luaChanged {
		if err := writeAtomic(lua, oldLua, RexLua()); err != nil {
			return res, err
		}
		res.Notes = append(res.Notes, "Wrote "+lua+".")
	}
	newInit := oldInit
	switch rexLine(oldInit) {
	case "marked":
	case "by_hand":
		res.Notes = append(res.Notes, cfg+" already requires tuios on a line of your own, so it was left as it is.")
	default:
		newInit = append([]byte{}, oldInit...)
		if len(newInit) > 0 && !bytes.HasSuffix(newInit, []byte("\n")) {
			newInit = append(newInit, '\n')
		}
		newInit = append(newInit, []byte(rexRequireLine+"\n")...)
		if err := writeAtomic(cfg, oldInit, newInit); err != nil {
			return res, rexUndo(res, err, lua, oldLua, luaChanged, cfg, nil, false)
		}
		res.Notes = append(res.Notes, "Added the require line to "+cfg+".")
	}
	initChanged := !bytes.Equal(newInit, oldInit)
	if out, err := rex("config", "check"); err != nil {
		return res, rexUndo(res, errfmt.Errorf("rex config check refused the config: %s%s", oneLine(out), rexErr(err)), lua, oldLua, luaChanged, cfg, oldInit, initChanged)
	}
	if _, err := rex("config", "reload"); err != nil {
		res.Notes = append(res.Notes, "Rex did not reload its config"+rexErr(err)+". The actions load when Rex next starts, or run rex config reload.")
		return res, nil
	}
	check, note := RexRunCheck(rex)
	res.Check = check
	if note != "" {
		res.Notes = append(res.Notes, note)
	}
	return res, nil
}

// rexUndo puts back the files install changed, after a later step failed,
// and returns cause with what was put back.
func rexUndo(res RexResult, cause error, lua string, oldLua []byte, luaChanged bool, cfg string, oldInit []byte, initChanged bool) error {
	var failed []string
	restore := func(path string, old []byte) {
		now, _ := readOptional(path)
		var err error
		if old == nil {
			err = os.Remove(path)
		} else {
			err = writeAtomic(path, now, old)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			failed = append(failed, path+": "+err.Error())
		}
	}
	if initChanged {
		restore(cfg, oldInit)
	}
	if luaChanged {
		restore(lua, oldLua)
	}
	if len(failed) > 0 {
		return errfmt.Errorf("%w. Putting the files back also failed: %s", cause, strings.Join(failed, "; "))
	}
	return errfmt.Errorf("%w. The files were put back as they were", cause)
}

// RexUninstall removes the line install added and a tuios.lua that is the one
// this build ships, then reloads Rex. A line or file of the person's own
// stays, and the result says so.
func RexUninstall(env Env, rex RexRunner) (RexResult, error) {
	cfg := RexConfigPath(env)
	lua := filepath.Join(filepath.Dir(cfg), "tuios.lua")
	res := RexResult{Config: cfg, Lua: lua}
	init, err := readOptional(cfg)
	if err != nil {
		return res, err
	}
	if init != nil {
		var kept []string
		for _, line := range strings.SplitAfter(string(init), "\n") {
			if !strings.HasSuffix(strings.TrimSpace(line), rexMarker) {
				kept = append(kept, line)
			}
		}
		if out := []byte(strings.Join(kept, "")); !bytes.Equal(out, init) {
			if err := writeAtomic(cfg, init, out); err != nil {
				return res, err
			}
			res.Notes = append(res.Notes, "Removed the require line from "+cfg+".")
			init = out
		}
		if rexLine(init) == "by_hand" {
			res.Notes = append(res.Notes, cfg+" still requires tuios on a line of your own. Remove it yourself if you want the actions gone.")
		}
	}
	state, _, data, err := rexLuaState(lua)
	if err != nil {
		return res, err
	}
	switch state {
	case "current", "older":
		if err := os.Remove(lua); err != nil {
			return res, errfmt.Errorf("failed to remove %s: %w", lua, err)
		}
		res.Notes = append(res.Notes, "Removed "+lua+".")
	case "edited", "foreign":
		res.Notes = append(res.Notes, lua+" has changes of your own, so it was left in place.")
	}
	_ = data
	if _, err := rex("config", "reload"); err != nil {
		res.Notes = append(res.Notes, "Rex did not reload its config"+rexErr(err)+". The actions go when Rex next starts.")
	}
	return res, nil
}

// RexStatusOf reports the files, whether Rex loaded the actions, and what
// tuios_check says from inside Rex.
func RexStatusOf(env Env, rex RexRunner) (RexStatus, error) {
	cfg := RexConfigPath(env)
	st := RexStatus{Config: cfg, Lua: filepath.Join(filepath.Dir(cfg), "tuios.lua")}
	state, version, _, err := rexLuaState(st.Lua)
	if err != nil {
		return st, err
	}
	st.LuaState, st.LuaVersion = state, version
	init, err := readOptional(cfg)
	if err != nil {
		return st, err
	}
	st.Line = rexLine(init)
	if rex == nil {
		st.LoadedNote = "rex is not on PATH"
		return st, nil
	}
	out, err := rex("actions")
	if err != nil {
		st.LoadedNote = "rex actions failed" + rexErr(err)
		return st, nil
	}
	st.Loaded = strings.Contains(out, "tuios_agents")
	if st.Loaded {
		st.Check, st.CheckNote = RexRunCheck(rex)
	}
	return st, nil
}

// RexRunCheck runs tuios_check inside Rex and reads its answer. The note says
// why there is none.
func RexRunCheck(rex RexRunner) (*RexCheck, string) {
	out, err := rex("do", "tuios_check")
	if err != nil {
		if strings.Contains(err.Error(), "no sessions") {
			return nil, "Rex has no session open, so tuios_check could not run yet. Open one and run tuios: Check the Rex Integration from the palette."
		}
		return nil, "tuios_check did not run in Rex" + rexErr(err)
	}
	i := strings.Index(out, "{")
	if i < 0 {
		return nil, "tuios_check gave no answer: " + oneLine(out)
	}
	var c RexCheck
	if err := jsonx.Unmarshal([]byte(out[i:]), &c); err != nil {
		return nil, "tuios_check gave an answer tuios cannot read: " + oneLine(out)
	}
	return &c, ""
}

// rexErr is err as ": message", for a sentence that names what failed.
func rexErr(err error) string {
	if err == nil {
		return ""
	}
	return ": " + oneLine(err.Error())
}

// oneLine joins s onto one line for a message.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
