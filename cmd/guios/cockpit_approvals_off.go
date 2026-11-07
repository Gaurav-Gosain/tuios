package main

import (
	"slices"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
	"github.com/Gaurav-Gosain/tuios/internal/guios/cli"
	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Approvals are off by default ([agents.approvals] in tuios's config.toml):
// while a prompt is held for guios, the harness shows nothing in its pane,
// so a person turns that on. Out of the box every Claude Code approval then
// reads "tuios cannot read this prompt", and nothing said that guios could
// answer it (Mac round 1, W-M3). The card says so where it offers the pane,
// with the Settings switch's own Turn on.

// ckApprovalsOff reports whether the card should offer to turn approvals on
// for the item: an approval of a Claude Code agent while they are off.
func (a *App) ckApprovalsOff(it session.AttentionItem, ag client.Agent) bool {
	return !a.set.Approvals && ag.Harness == approvalsHarness &&
		(it.Kind == session.AttentionApproval || it.Kind == session.AttentionPlan)
}

// ckApprovalsOffLine draws the offer under the card's "cannot read" line:
// what turning approvals on does, and a secondary Turn on.
func (a *App) ckApprovalsOffLine(c *ui.Context, pal *palette, it session.AttentionItem, ag client.Agent) {
	if !a.ckApprovalsOff(it, ag) {
		return
	}
	ui.Text(c, locales.CockpitApprovalsOff()).FontSize(fsSmall).TextColor(pal.textMuted).MaxLines(2).MinWidth(0)
	if sizedButton(c, "ans-approvals-on", locales.CockpitApprovalsTurnOn(), "", false, hControl, rControl, pal) {
		a.ckTurnOnApprovals()
	}
}

// ckTurnOnApprovals does what Settings > Agents > Approvals does: it turns
// the switch on, which writes the config line the daemon reads at once,
// and installs the Claude Code integration when it is missing, since the
// prompts reach guios only through its hook.
func (a *App) ckTurnOnApprovals() {
	states := cli.SkillStates()
	if i := slices.IndexFunc(states, func(st cli.SkillState) bool { return st.Agent == approvalsHarness }); i >= 0 && states[i].State == "missing" {
		if err := cli.InstallSkill(approvalsHarness); err != nil {
			a.toast(err.Error())
			return
		}
	}
	s := a.set.Clone()
	s.Approvals = true
	a.applySettings(s)
	a.ckPromptToast(locales.CockpitApprovalsOn())
}
