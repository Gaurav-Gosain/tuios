package text

import "testing"

func TestUsecaseText(t *testing.T) {
	drawPinned(t, "usecase-beta\nactor User\nsystemBoundary Shop\n  login(Log in)\n  pay[Pay]\nend\nUser --> login\nlogin ..>: include pay", 0, `
               ┌─ Shop ─────────────────────────────────┐
               │                                        │
   o           │                                        │
  /|\   ───┐   │   ╭────────╮                 ┌───────┐ │
  / \      └───┼──►│ Log in │┄┄┄ «include» ┄┄►│  Pay  │ │
  User         │   ╰────────╯                 └───────┘ │
               └────────────────────────────────────────┘
`)
}
