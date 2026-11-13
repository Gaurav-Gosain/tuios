package acp_test

import (
	"context"
	"fmt"
	"log"
	"os"

	"tuios.dev/acp"
)

// Start an agent over stdio, open a session in the current folder and run
// one prompt, printing the agent's reply as it streams. Permission requests
// are dismissed, so the agent does nothing it would have to ask for.
func Example() {
	ctx := context.Background()
	agent := &acp.Process{Path: "opencode", Args: []string{"acp"}, Stderr: os.Stderr}
	c, err := acp.Connect(ctx, agent, acp.ClientOptions{
		Info: acp.Implementation{Name: "example", Version: "0.1.0"},
		OnUpdate: func(u acp.Update) {
			if m, ok := u.(*acp.MessageUpdate); ok && m.Role == acp.RoleAgent && m.Op == acp.MessageAppend {
				fmt.Print(m.Text())
			}
		},
		OnPermission: func(p *acp.PermissionRequest) { p.Cancel() },
	})
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	s, err := c.NewSession(ctx, cwd)
	if err != nil {
		log.Fatal(err)
	}
	end, err := s.Prompt(ctx, acp.Text("Summarise the TODO comments in this folder."))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\nstopped:", end.Reason)
}
