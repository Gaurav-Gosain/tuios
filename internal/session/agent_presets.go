package session

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// Agent presets: [agents.presets.NAME] names a set of start-agent params, so
// a lead starts a worker as start-agent --preset worker --name w3.
//
// A preset is merged into the caller's params once, at the start of the call,
// on the daemon the caller talks to: a scalar the caller passes wins, the
// lists (args, add_dirs, after_ready) are the preset's then the caller's, env
// is the preset's with the caller's on top, and grants the caller passes
// replace the preset's. The merged params are what start-agent checks, so a
// preset never grants more than its caller may give. A start on another
// machine sends the merged params over the link (the CLI asks this daemon
// with agent-preset first), so a lead's presets work on every host.

// SetAgentPresets replaces [agents.presets].
func (d *Daemon) SetAgentPresets(p map[string]config.AgentPreset) {
	if len(p) == 0 {
		d.agentPresets.Store(nil)
		return
	}
	d.agentPresets.Store(&p)
}

// agentPresetNames lists the presets, sorted.
func (d *Daemon) agentPresetNames() []string {
	m := d.agentPresets.Load()
	if m == nil {
		return nil
	}
	names := make([]string, 0, len(*m))
	for name := range *m {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// presetParams is a preset as start-agent params. A field left empty is
// not sent, so the caller's own stands.
type presetParams struct {
	Agent          string            `json:"agent,omitempty"`
	Args           []string          `json:"args,omitempty"`
	AddDirs        []string          `json:"add_dirs,omitempty"`
	Grants         []string          `json:"grants,omitempty"`
	Model          string            `json:"model,omitempty"`
	Effort         string            `json:"effort,omitempty"`
	PermissionMode string            `json:"permission_mode,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	AfterReady     []string          `json:"after_ready,omitempty"`
	ReadyTimeout   int               `json:"ready_timeout,omitempty"`
}

// expandAgentPreset merges the preset params names into params and returns
// them without the preset, or params unchanged when they name none.
func (d *Daemon) expandAgentPreset(params json.RawMessage) (json.RawMessage, *verbError) {
	var m map[string]json.RawMessage
	if len(params) > 0 {
		if verr := decodeParams(params, &m); verr != nil {
			return nil, verr
		}
	}
	raw, ok := m["preset"]
	if !ok {
		return params, nil
	}
	var name string
	if err := jsonx.Unmarshal(raw, &name); err != nil {
		return nil, invalidParam("preset", "preset is the name of an [agents.presets] entry, as a string")
	}
	delete(m, "preset")
	var preset config.AgentPreset
	ok = false
	if all := d.agentPresets.Load(); all != nil {
		preset, ok = (*all)[name]
	}
	if !ok {
		names := d.agentPresetNames()
		msg := "there is no preset " + echoName(name) + " in [agents.presets]"
		if len(names) > 0 {
			msg += ". The presets are " + strings.Join(names, ", ")
		}
		return nil, invalidParam("preset", msg, names...)
	}
	env := map[string]string{}
	for _, kv := range preset.Env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, invalidParam("preset", "preset "+echoName(name)+" has env "+echoName(kv)+": each is NAME=VALUE")
		}
		env[k] = v
	}
	from, _ := jsonx.Marshal(presetParams{preset.Agent, preset.Args, preset.AddDirs, preset.Grants, preset.Model,
		preset.Effort, preset.PermissionMode, env, preset.AfterReady, preset.ReadyTimeout})
	var fields map[string]json.RawMessage
	_ = jsonx.Unmarshal(from, &fields)
	for key, v := range fields {
		own, has := m[key]
		switch {
		case !has:
			m[key] = v
		case key == "env":
			var mine map[string]string
			if err := jsonx.Unmarshal(own, &mine); err != nil {
				return nil, invalidParam(key, "env is an object of name to value")
			}
			maps.Copy(env, mine)
			m[key], _ = jsonx.Marshal(env)
		case key == "args" || key == "add_dirs" || key == "after_ready":
			var theirs, mine []string
			_ = jsonx.Unmarshal(v, &theirs)
			if err := jsonx.Unmarshal(own, &mine); err != nil {
				return nil, invalidParam(key, key+" is a list of strings")
			}
			m[key], _ = jsonx.Marshal(append(theirs, mine...))
		}
	}
	out, err := jsonx.Marshal(m)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, err.Error())
	}
	return out, nil
}

// verbAgentPreset answers agent-preset: params merged with a preset, ready
// for start-agent on any machine.
func (d *Daemon) verbAgentPreset(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Preset string                     `json:"preset"`
		Params map[string]json.RawMessage `json:"params"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Params == nil {
		p.Params = map[string]json.RawMessage{}
	}
	p.Params["preset"], _ = jsonx.Marshal(p.Preset)
	in, _ := jsonx.Marshal(p.Params)
	out, verr := d.expandAgentPreset(in)
	if verr != nil {
		return nil, verr
	}
	return map[string]any{"params": out}, nil
}

// addDirArgs is one add-dir flag per directory, for the harness named, with ~
// as this machine's home folder.
func (d *Daemon) addDirArgs(launch agentLaunch, dirs []string) ([]string, *verbError) {
	if len(dirs) == 0 {
		return nil, nil
	}
	flag := ""
	if reg := d.agentMatcher.registry; reg != nil && launch.harness != "" {
		if m := reg.Lookup(launch.harness); m != nil {
			flag = m.Launch.AddDirFlag
		}
	}
	if flag == "" {
		return nil, invalidParam("add_dirs", "the agent's harness has no add-dir flag ([launch] add_dir_flag in its manifest), so add_dirs cannot be passed. Pass the harness's own flag in args")
	}
	out := make([]string, 0, 2*len(dirs))
	for _, dir := range dirs {
		out = append(out, flag, ExpandHome(dir))
	}
	return out, nil
}

// afterReadyPause is the gap between two after_ready steps, so a TUI that
// opens a menu on one step has drawn it before the next.
const afterReadyPause = 300 * time.Millisecond

// sendAfterReady sends the after_ready steps to a ready agent, in order: a
// step that is one key name is that key, anything else is typed as text.
// Each step is refused while the pane waits on a prompt of its own, which
// stops the rest. It returns sent, or stopped with why.
func (d *Daemon) sendAfterReady(sess *Session, windowID string, steps []string) (string, string) {
	if pty, err := d.resolvePTYForTarget(sess, windowID); err == nil && d.harnessKnown(sess, windowID) {
		if err := waitInputLive(d.ctx, pty, d.inputProfileFor(sess, windowID), inputLiveQuiet, inputLiveMaxWait); err != nil {
			return PromptNotSent, "Could not write to the agent's pane: " + err.Error()
		}
	}
	for i, step := range steps {
		if i > 0 {
			time.Sleep(afterReadyPause)
		}
		if verr := d.refuseBlockedAgent(sess, windowID); verr != nil {
			return "stopped", "Step " + echoName(step) + " and the ones after it were not sent: " + verr.Message
		}
		var err error
		if key, ok := afterReadyKey(step); ok {
			_, err = d.writeKeysToWindow(sess, windowID, step, false, false, []sendKey{key})
		} else {
			_, err = d.writeKeysToWindow(sess, windowID, step, true, false, nil)
		}
		if err != nil {
			return "stopped", "Step " + echoName(step) + " and the ones after it were not sent: " + err.Error()
		}
	}
	return PromptSent, ""
}

// afterReadyKey reads a step as one named key, such as Enter or C-c. A single
// character, or anything with a space, is text.
func afterReadyKey(step string) (sendKey, bool) {
	if strings.ContainsAny(step, " \t\n") || len([]rune(step)) < 2 {
		return sendKey{}, false
	}
	k, err := parseKeyToken(step)
	if err != nil || k.named == nil {
		return sendKey{}, false
	}
	return k, true
}
