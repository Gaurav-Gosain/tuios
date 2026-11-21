package app_test

import (
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/input"
)

func init() { app.SetFuzzInput(input.HandleInput) }
