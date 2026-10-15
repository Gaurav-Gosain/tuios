package vt

// maxTitleStack is how many entries XTWINOPS 22 keeps, as in xterm. A push
// past it drops the oldest entry.
const maxTitleStack = 10

// savedTitle is one entry of the title stack. A push can save the icon name,
// the window title or both, and a pop restores only what the entry holds.
type savedTitle struct {
	icon, title       string
	hasIcon, hasTitle bool
}

// pushTitle saves the icon name and the window title for XTWINOPS 22: which
// is 0 for both, 1 for the icon name and 2 for the window title. It reports
// false for any other value.
func (e *Emulator) pushTitle(which int) bool {
	if which < 0 || which > 2 {
		return false
	}
	entry := savedTitle{
		icon: e.iconName, hasIcon: which != 2,
		title: e.title, hasTitle: which != 1,
	}
	if len(e.titleStack) >= maxTitleStack {
		e.titleStack = append(e.titleStack[:0], e.titleStack[1:]...)
	}
	e.titleStack = append(e.titleStack, entry)
	return true
}

// popTitle restores what the last XTWINOPS 22 saved, for XTWINOPS 23, and
// drops the entry. which selects what to restore, as for pushTitle. An empty
// stack restores nothing.
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
	}
	if entry.hasTitle && which != 1 {
		e.title = entry.title
		if e.cb.Title != nil {
			e.cb.Title(entry.title)
		}
	}
	return true
}
