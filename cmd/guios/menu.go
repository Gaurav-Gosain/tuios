package main

import "github.com/egoist/mygo"

// appMenu is the macOS menu bar: the app menu and the Edit and Window menus
// of the system, so Cmd+Q, Cmd+H and Cmd+M work. M5 adds guios's own menus.
func appMenu(a *guiApp) *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Role: mygo.RoleEditMenu},
		{Role: mygo.RoleWindowMenu},
	})
}
