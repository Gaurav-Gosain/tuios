package app

import (
	"maps"
	"os"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The offer to add a machine.
//
// The daemon marks a pane whose foreground program is an ssh or mosh login to
// a machine no [hosts] entry reaches (session.WindowState.SSHOffer). The
// client shows one message for it in the dock, with no focus taken and
// nothing typed into the pane:
//
//	buildbox is not a machine here. Add it?  Add  Not now  Never
//
//   - Add opens a new pane that runs tuios hosts setup with the destination.
//   - Not now takes the message away. The machine is not offered again until
//     tuios starts again.
//   - Never stores the target in sidebar.json (machine_offers_never), and the
//     Alerts tab of Settings lists it with a row that removes it.
//   - No choice: the message leaves after notifications.duration, which counts
//     as Not now.
//
// A machine is offered once per run of the client, whichever pane logs in to
// it.

// Machine offer choices, in the order the dock draws them.
const (
	offerAdd = iota
	offerNotNow
	offerNever
)

// offerChoices are the words the dock draws after the message.
var offerChoices = [...]string{offerAdd: "Add", offerNotNow: "Not now", offerNever: "Never"}

// machineOfferText is the message for a machine.
func machineOfferText(machine string) string {
	return machine + " is not a machine here. Add it?"
}

// isMachineOffer reports whether a target is the offer to add a machine.
func isMachineOffer(t *NotifTarget) bool { return t != nil && t.OfferTarget != "" }

// noteMachineOffer shows the offer for a pane the daemon marked, once per
// machine per run. A pane on another machine is that machine's to offer.
func (m *OS) noteMachineOffer(w *terminal.Window, offer *session.SSHOffer) {
	if offer == nil || w == nil || w.Host != "" || !m.Settings.NotificationMachineOffer {
		return
	}
	if offer.Target == "" || offer.Dest == "" || offer.Machine == "" {
		return
	}
	if m.machineOffersShown[offer.Target] || m.machineOffersNever[offer.Target] {
		return
	}
	if m.machineOffersShown == nil {
		m.machineOffersShown = map[string]bool{}
	}
	m.machineOffersShown[offer.Target] = true
	m.ShowNotificationFrom(machineOfferText(offer.Machine), "info", m.Settings.NotificationDuration, NotifTarget{
		SessionID:    m.sidebarCurrentSessionID(),
		WindowID:     w.ID,
		OfferDest:    offer.Dest,
		OfferMachine: offer.Machine,
		OfferTarget:  offer.Target,
	})
}

// answerMachineOffer takes the offer off the dock and acts on the choice.
func (m *OS) answerMachineOffer(t NotifTarget, choice int) tea.Cmd {
	m.Notifications = slices.DeleteFunc(m.Notifications, func(n Notification) bool {
		return n.Target != nil && n.Target.OfferTarget == t.OfferTarget
	})
	switch choice {
	case offerAdd:
		exe, err := os.Executable()
		if err != nil {
			m.ShowNotification("tuios cannot find its own program. Run tuios hosts setup "+t.OfferDest+" in a pane.", "error", m.Settings.NotificationDuration*2)
			return nil
		}
		// The destination came from a process's command line. It is one
		// argument after --, so it can be neither a flag nor a command.
		m.AddWindow("Add "+t.OfferMachine, exe, "hosts", "setup", "--", t.OfferDest)
		m.EnterTerminalMode()
	case offerNever:
		m.setMachineOfferNever(t.OfferTarget, true)
	}
	return nil
}

// setMachineOfferNever adds a target to the never list, or takes it off, and
// saves the list.
func (m *OS) setMachineOfferNever(target string, never bool) {
	if m.machineOffersNever == nil {
		m.machineOffersNever = map[string]bool{}
	}
	if m.machineOffersRemoved == nil {
		m.machineOffersRemoved = map[string]bool{}
	}
	if never {
		m.machineOffersNever[target] = true
		delete(m.machineOffersRemoved, target)
	} else {
		delete(m.machineOffersNever, target)
		// The file is merged on save, so a target another client still
		// holds has to be named as removed, or it comes back.
		m.machineOffersRemoved[target] = true
	}
	m.saveSidebarState()
}

// machineOfferNeverItems are the Settings rows of the never list, one for
// each target, sorted. Enter or a click on a row removes it.
func (m *OS) machineOfferNeverItems() []settingsRow {
	var rows []settingsRow
	for _, target := range slices.Sorted(maps.Keys(m.machineOffersNever)) {
		rows = append(rows, custom("", settingItem{
			Label:   target,
			Desc:    "tuios does not offer to add this machine. Press enter to remove it from the list.",
			Control: controlStatus,
			value:   func(*OS) string { return "never offered" },
			activate: func(m *OS) tea.Cmd {
				m.setMachineOfferNever(target, false)
				m.ShowNotification(target+" is off the list. tuios can offer it again.", "info", m.Settings.NotificationDuration)
				return nil
			},
		}))
	}
	return rows
}
