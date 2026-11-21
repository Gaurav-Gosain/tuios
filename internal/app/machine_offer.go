package app

import (
	"maps"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const (
	offerAdd = iota
	offerNotNow
	offerNever
)

var offerChoices = [...]string{offerAdd: "Add", offerNotNow: "Not now", offerNever: "Never"}

func machineOfferText(machine string) string {
	return machine + " is not a machine here. Add it?"
}

func isMachineOffer(t *NotifTarget) bool { return t != nil && t.OfferTarget != "" }

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

func (m *OS) answerMachineOffer(t NotifTarget, choice int) tea.Cmd {
	m.Notifications = slices.DeleteFunc(m.Notifications, func(n Notification) bool {
		return n.Target != nil && n.Target.OfferTarget == t.OfferTarget
	})
	switch choice {
	case offerAdd:
		return m.OpenMachineSheet(t.OfferDest)
	case offerNever:
		m.setMachineOfferNever(t.OfferTarget, true)
	}
	return nil
}

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
		m.machineOffersRemoved[target] = true
	}
	m.saveSidebarState()
}

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
