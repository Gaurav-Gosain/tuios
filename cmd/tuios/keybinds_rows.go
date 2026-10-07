package main

import (
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// keybindRow is one row of `tuios keybinds list`. The rows are built in
// internal/app so the GUI bridge sends the same rows the list prints.
type keybindRow = app.KeybindRow

// keybindScopeUnbound is the scope of an action that no table binds.
const keybindScopeUnbound = app.KeybindScopeUnbound

// keybindRows builds every row of `tuios keybinds list` for cfg.
func keybindRows(cfg *config.UserConfig) []keybindRow { return app.KeybindRows(cfg) }
