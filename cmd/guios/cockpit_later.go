package main

import (
	"encoding/json"

	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// What the cockpit's later steps fill in: the reply box, mail, the board,
// changes and checkpoints.

type ckTabs struct{}

func jsonUnmarshal(raw []byte, out any) error { return json.Unmarshal(raw, out) }

func (a *App) ckMailChanged(*proto.DaemonEvent)                             {}
func (a *App) ckTasksChanged()                                              {}
func (a *App) ckTabOpened()                                                 {}
func (a *App) changesView(c *ui.Context, pal *palette, ag client.Agent)     {}
func (a *App) checkpointsView(c *ui.Context, pal *palette, ag client.Agent) {}
func (a *App) mailView(c *ui.Context, pal *palette, ag client.Agent)        {}
func (a *App) boardView(c *ui.Context, pal *palette, session string)        {}
func (a *App) cockpitSide(c *ui.Context, pal *palette)                      {}
