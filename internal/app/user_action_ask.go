package app

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The ask steps of a user action (config/user_action_args.go). A step asks
// the person in the palette with the prompt verb (session/prompt.go), the way
// tuios prompt does, so any attached client may answer it. The calls block
// until the person answers, so they run in a command, and the answers come
// back to Update as userActionAskedMsg, which runs the action.

// userActionAskedMsg carries the values of an action whose steps asked.
type userActionAskedMsg struct {
	action config.UserAction
	extra  map[string]string
	values map[string]string
	err    error
}

// askUserAction asks the steps of a that given has no value for, then runs
// it.
func (m *OS) askUserAction(a config.UserAction, extra, given map[string]string) tea.Cmd {
	if !m.inboxCanAct("An action that asks") {
		return nil
	}
	call := m.inboxActCaller()
	return func() tea.Msg {
		values, err := a.ResolveArgs(given, func(s config.ActionAsk) (string, error) {
			return AskActionStep(call, s)
		})
		return userActionAskedMsg{action: a, extra: extra, values: values, err: err}
	}
}

// AskActionStep asks one step with the prompt verb through call, and
// returns the answer. tuios actions run asks the same way.
func AskActionStep(call func(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error), s config.ActionAsk) (string, error) {
	params := map[string]any{"kind": session.PromptInput, "title": s.Title, "placeholder": s.Placeholder}
	if params["title"] == "" {
		params["title"] = "Enter a value for " + s.Name
	}
	if s.Kind == session.PromptPick {
		items := make([]session.PromptItem, 0, len(s.Items))
		for _, it := range s.Items {
			id, title := config.SplitAskItem(it)
			items = append(items, session.PromptItem{ID: id, Title: title})
		}
		params["kind"], params["items"], params["allow_other"] = session.PromptPick, items, s.AllowOther
	}
	raw, err := call("prompt", params, 135*time.Second)
	if err != nil {
		return "", err
	}
	var res session.PromptResult
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	switch {
	case res.Status != session.PromptAnswered:
		return "", &config.ErrActionAskEnded{Name: s.Name, Status: res.Status}
	case res.Other != "":
		return res.Other, nil
	case len(res.IDs) > 0:
		return res.IDs[0], nil
	}
	return res.Value, nil
}

// userActionAsked runs an action once its steps were answered. A cancel at
// a client runs nothing and says nothing more: the person chose it.
func (m *OS) userActionAsked(msg userActionAskedMsg) tea.Cmd {
	if ended, ok := errors.AsType[*config.ErrActionAskEnded](msg.err); ok && ended.Status == session.PromptCancelled {
		return nil
	}
	if msg.err != nil {
		m.userActionFailed(msg.action, msg.err.Error())
		return nil
	}
	return m.runUserActionWith(msg.action, msg.extra, msg.values)
}

// RunUserActionArgs runs the user action name with arguments key=value, for
// a tape's Action command and tuios actions run. It implements
// tape.Executor.
func (m *OS) RunUserActionArgs(name string, args []string) error {
	given := map[string]string{}
	for _, kv := range args {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return errors.New("an argument is key=value, not " + kv)
		}
		given[k] = v
	}
	m.queueScriptCmd(m.RunUserAction(name, nil, given))
	m.MarkAllDirty()
	return nil
}
