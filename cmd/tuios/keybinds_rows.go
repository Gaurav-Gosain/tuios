package main

import (
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

type keybindRow = app.KeybindRow

const keybindScopeUnbound = app.KeybindScopeUnbound

func keybindRows(cfg *config.UserConfig) []keybindRow { return app.KeybindRows(cfg) }
