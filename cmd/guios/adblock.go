package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"slices"
	"sync"

	"github.com/egoist/mygo"

	"github.com/Gaurav-Gosain/tuios/internal/guios/adblock"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// adBlock blocks ads and trackers in browser panes (browser-block-ads):
// the first time a pane shows a page with the setting on, it downloads
// the filter lists the settings name (browser-filter-list), if it has
// to, and compiles them off the main thread, and again when the user
// chooses other lists. Pages made before that is done get the rules when
// it is.
type adBlock struct {
	started bool
	// lists are those compiled, or compiling, last; gen counts the
	// compiles, so that only the latest one's rules apply.
	lists []string
	gen   int
	rules []*mygo.ContentRules // main thread
}

// adRules returns the rules browser panes' pages apply: none while the
// setting is off or the rules are not compiled yet.
func (a *App) adRules() []*mygo.ContentRules {
	if !a.set.BlockAds {
		return nil
	}
	a.compileAds()
	return a.ads.rules
}

// compileAds compiles the lists the settings name, unless those compiled
// or compiling last are those.
func (a *App) compileAds() {
	if a.ads.started && slices.Equal(a.ads.lists, a.set.AdLists) {
		return
	}
	a.ads.started, a.ads.lists = true, slices.Clone(a.set.AdLists)
	a.ads.gen++
	gen, lists := a.ads.gen, a.ads.lists
	go func() {
		rules, err := compileAdBlock(lists)
		if err != nil {
			log.Printf("kero: ad blocking: %v", err)
		}
		a.update(func() {
			if a.ads.gen != gen {
				return // other lists compile now
			}
			a.ads.rules = rules
			a.applyAdBlock()
		})
	}()
}

// applyAdBlock gives the open browser panes' pages the rules, or takes
// them away, as the settings are; they apply to the loads from then on.
func (a *App) applyAdBlock() {
	rules := a.adRules()
	for _, e := range a.webs {
		if e.view != nil && !e.view.IsDestroyed() {
			e.view.Page().SetContentRules(rules...)
		}
	}
}

// compiling has one compile run at a time: each compiles the lists
// kero-ads-0, kero-ads-1 and so on, and replaces what the one before it
// compiled under those ids.
var compiling sync.Mutex

// compileAdBlock compiles the filter lists of ids, as many content rule
// lists as they take, keeping those it compiled if one fails.
func compileAdBlock(ids []string) ([]*mygo.ContentRules, error) {
	compiling.Lock()
	defer compiling.Unlock()
	var sources []adblock.Source
	for _, id := range ids {
		if s, ok := adblock.Find(id); ok {
			sources = append(sources, s)
		}
	}
	if len(sources) == 0 {
		return nil, nil
	}
	filters, err := adblock.Load(context.Background(), filepath.Join(proto.Dir(), "adblock"), sources)
	if err != nil {
		return nil, err
	}
	var r adblock.Rules
	for _, f := range filters {
		r.Convert(f)
	}
	var rules []*mygo.ContentRules
	for i, list := range r.Lists() {
		cr, cerr := mygo.CompileContentRules(fmt.Sprintf("kero-ads-%d", i), list)
		if cerr != nil {
			err = cerr
			continue
		}
		rules = append(rules, cr)
	}
	return rules, err
}
