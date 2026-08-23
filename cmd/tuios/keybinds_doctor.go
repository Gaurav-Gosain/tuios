package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// keybindsDoctor prints the same analysis the keybind overlay draws.
//
// It exists because a conflict report that only exists as pixels is useless to
// an agent configuring tuios: it would have to drive the overlay and read a
// screenshot to learn something the config already determines. The overlay and
// this share one implementation, so what an agent reads and what a human sees
// cannot drift apart.
//
// guest names a program to treat as the one running in the pane. From a
// terminal there is no pane to read, so the observed tier is empty here and the
// report says nothing about a pane rather than making something up; naming a
// program marks the rows that would be live if it were running, without
// promoting them out of the reference tier.
func keybindsDoctor(asJSON bool, guest string) error {
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		fmt.Fprintln(os.Stderr, "Using default keybindings...")
		userConfig = config.DefaultConfig()
	}
	registry := config.NewKeybindRegistry(userConfig)
	report := registry.Report(config.PaneFacts{Command: guest})

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	printKeybindReport(report)
	return nil
}

// printKeybindReport writes the report as plain text.
//
// Deliberately unstyled, unlike `keybinds list`: this output is read by agents
// and pasted into issues at least as often as it is looked at, and colour codes
// in a pasted report are noise. Every finding carries its tier in the text,
// because a reader who only sees this has nowhere else to learn that a third of
// it is a curated list.
func printKeybindReport(rep config.KeybindReport) {
	fmt.Printf("tuios keybinds: %s\n", rep.Summary())
	fmt.Printf("leader: %s\n", rep.Leader)

	fmt.Println("\nEVIDENCE")
	for _, tier := range []config.Evidence{config.EvidenceCertain, config.EvidenceObserved, config.EvidenceReference} {
		fmt.Printf("  %-10s %s\n", tier, rep.EvidenceNote[tier])
	}

	if len(rep.Observations) > 0 {
		fmt.Println("\nOBSERVED")
		for _, o := range rep.Observations {
			fmt.Printf("  %-14s %s\n", o.What, o.Detail)
		}
	}

	fmt.Printf("\nCONFLICTS WITHIN TUIOS (%s)\n", config.EvidenceCertain)
	if len(rep.Collisions) == 0 {
		fmt.Println("  none: each key does one thing in its scope")
	}
	for _, c := range rep.Collisions {
		across := ""
		if c.CrossSection {
			across = "  [claims are in different config tables]"
		}
		fmt.Printf("  %-22s %s runs %s%s\n", c.Press, c.ScopeName, c.Winner, across)
		for _, l := range c.Losers {
			fmt.Printf("  %-22s   dead: %s [%s]\n", "", l.Action, l.Section)
		}
	}

	fmt.Printf("\nKEYS TUIOS TAKES FROM THE PANE (%s)\n", config.EvidenceCertain)
	fmt.Println("  These never reach the program running in a pane. Everything else is forwarded.")
	for _, s := range rep.Swallowed {
		fmt.Printf("  %-22s %s [%s]\n", s.Key, s.Desc, s.Origin)
	}

	fmt.Printf("\nGUEST CLASHES (%s)\n", config.EvidenceReference)
	if len(rep.GuestClashes) == 0 {
		fmt.Println("  no common program binds any of the keys above by default")
	}
	for _, c := range rep.GuestClashes {
		live := ""
		if c.Running {
			live = "  <- named as running"
		}
		fmt.Printf("  %-14s %-32s %s%s\n", c.Key, c.Program, c.ProgramUse, live)
	}

	if len(rep.Ambiguous) > 0 {
		fmt.Printf("\nKEYS A TERMINAL MAY NOT DISTINGUISH (%s)\n", config.EvidenceCertain)
		for _, a := range rep.Ambiguous {
			fmt.Printf("  %-14s also %s [%s]\n", a.Key, strings.Join(a.Partners, ", "), a.Scope)
		}
	}
	fmt.Println()
}

// keybindsExplain prints everything tuios does with one key: the recorder's
// answer, for a caller that cannot press anything.
func keybindsExplain(key string, asJSON bool, guest string) error {
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		userConfig = config.DefaultConfig()
	}
	registry := config.NewKeybindRegistry(userConfig)
	fate := registry.Fate(key, config.PaneFacts{Command: guest})

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(fate)
	}

	fmt.Printf("%s\n", fate.Key)
	if fate.Free {
		fmt.Println("  free: nothing in tuios claims it, and the pane receives it")
	}
	for _, a := range fate.Acts {
		dead := ""
		if a.Shadowed {
			dead = fmt.Sprintf("  (dead: %s owns the key)", a.ShadowedBy)
		}
		fmt.Printf("  %-16s %s [%s]%s\n", a.Scope, a.Desc, a.Section, dead)
	}
	if fate.SwallowedInTerminal {
		fmt.Printf("  %-16s kept from the pane: %s\n", "pane", fate.SwallowReason)
	} else if !fate.Free {
		fmt.Printf("  %-16s forwarded to the pane\n", "pane")
	}
	if fate.Ambiguity != "" {
		fmt.Printf("\n  %s\n", fate.Ambiguity)
	}
	if len(fate.GuestWants) > 0 {
		fmt.Printf("\n  wanted by (%s):\n", config.EvidenceReference)
		for _, g := range fate.GuestWants {
			fmt.Printf("    %-32s %s\n", g.Program, g.ProgramUse)
		}
	}
	fmt.Println()
	return nil
}
