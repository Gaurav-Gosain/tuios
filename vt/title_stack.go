package vt

const maxTitleStack = 10

type savedTitle struct {
	icon, title       string
	hasIcon, hasTitle bool
	explicitIcon      string
}

func (e *Emulator) pushTitle(which int) bool {
	if which < 0 || which > 2 {
		return false
	}
	entry := savedTitle{
		icon: e.iconName, hasIcon: which != 2,
		title: e.title, hasTitle: which != 1,
		explicitIcon: e.explicitIcon,
	}
	if len(e.titleStack) >= maxTitleStack {
		e.titleStack = append(e.titleStack[:0], e.titleStack[1:]...)
	}
	e.titleStack = append(e.titleStack, entry)
	return true
}

func (e *Emulator) popTitle(which int) bool {
	if which < 0 || which > 2 {
		return false
	}
	if len(e.titleStack) == 0 {
		return true
	}
	entry := e.titleStack[len(e.titleStack)-1]
	e.titleStack = e.titleStack[:len(e.titleStack)-1]
	if entry.hasIcon && which != 2 {
		e.iconName = entry.icon
		if e.cb.IconName != nil {
			e.cb.IconName(entry.icon)
		}
		if entry.explicitIcon != e.explicitIcon {
			e.explicitIcon = entry.explicitIcon
			if e.cb.ExplicitIconName != nil {
				e.cb.ExplicitIconName(entry.explicitIcon)
			}
		}
	}
	if entry.hasTitle && which != 1 {
		e.title = entry.title
		if e.cb.Title != nil {
			e.cb.Title(entry.title)
		}
	}
	return true
}

func (s savedTitle) entry() TitleEntry {
	return TitleEntry{Title: s.title, Icon: s.icon, HasTitle: s.hasTitle, HasIcon: s.hasIcon, ExplicitIcon: s.explicitIcon}
}

func savedTitleOf(e TitleEntry) savedTitle {
	return savedTitle{title: e.Title, icon: e.Icon, hasTitle: e.HasTitle, hasIcon: e.HasIcon, explicitIcon: e.ExplicitIcon}
}
