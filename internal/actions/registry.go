// Package actions defines every user action once. Page bindings, row actions,
// the command palette, help and the CLI all read this registry.
package actions

import (
	"fmt"
	"sort"
	"strings"
)

// Mode names used for binding scopes.
const (
	ModeNormal  = "normal"
	ModeInsert  = "insert"
	ModeLive    = "live"
	ModeJump    = "jump"
	ModeContext = "context"
	ModePalette = "palette"
	ModeHelp    = "help"
	ModeNote    = "note"
	ModeGlobal  = "global" // reserved before any mode routing (e.g. New note F4)
)

// Action is defined once and reused everywhere.
type Action struct {
	ID          string   // "new.terminal"
	Verb        string   // "New"
	Object      string   // "Terminal"
	Description string
	Modes       []string // modes where the binding applies
	Keys        []string // default key sequences, e.g. "n t", "N t", "ctrl+a"
	Page        string   // "" = any page
	NeedsTarget bool     // object-first route: applies to a selected row
	CLI         string   // optional CLI subcommand name
}

func (a Action) Label() string {
	if a.Object == "" {
		return a.Verb
	}
	return a.Verb + " → " + a.Object
}

// Registry holds actions and effective bindings (defaults + remaps).
type Registry struct {
	actions map[string]Action
	order   []string
	remaps  map[string][]string // actionID → keys
}

func New() *Registry {
	return &Registry{actions: map[string]Action{}, remaps: map[string][]string{}}
}

func (r *Registry) Register(a Action) error {
	if a.ID == "" {
		return fmt.Errorf("action without id")
	}
	if _, dup := r.actions[a.ID]; dup {
		return fmt.Errorf("duplicate action %q", a.ID)
	}
	if len(a.Modes) == 0 {
		a.Modes = []string{ModeNormal}
	}
	r.actions[a.ID] = a
	r.order = append(r.order, a.ID)
	return nil
}

func (r *Registry) MustRegister(a Action) {
	if err := r.Register(a); err != nil {
		panic(err)
	}
}

func (r *Registry) Get(id string) (Action, bool) { a, ok := r.actions[id]; return a, ok }

func (r *Registry) All() []Action {
	out := make([]Action, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.actions[id])
	}
	return out
}

// Remap replaces an action's effective keys. Returns an error on a conflict
// within any shared mode; the remap is not applied.
func (r *Registry) Remap(id string, keys []string) error {
	a, ok := r.actions[id]
	if !ok {
		return fmt.Errorf("unknown action %q", id)
	}
	for _, k := range keys {
		for _, other := range r.All() {
			if other.ID == id {
				continue
			}
			if sharesMode(a.Modes, other.Modes) && contains(r.Keys(other.ID), k) {
				return fmt.Errorf("binding %q conflicts with %s in mode(s) %s", k, other.ID, strings.Join(intersect(a.Modes, other.Modes), ","))
			}
		}
	}
	r.remaps[id] = keys
	return nil
}

// Keys returns the effective keys for an action.
func (r *Registry) Keys(id string) []string {
	if k, ok := r.remaps[id]; ok {
		return k
	}
	return r.actions[id].Keys
}

// Lookup finds the action bound to key in mode (and page "" or page).
func (r *Registry) Lookup(mode, page, key string) (Action, bool) {
	for _, a := range r.All() {
		if !contains(a.Modes, mode) && !contains(a.Modes, ModeGlobal) {
			continue
		}
		if a.Page != "" && page != "" && a.Page != page {
			continue
		}
		if contains(r.Keys(a.ID), key) {
			return a, true
		}
	}
	return Action{}, false
}

// Effective returns every action with its effective keys, sorted by id, for help.
func (r *Registry) Effective() []Bound {
	out := []Bound{}
	for _, a := range r.All() {
		out = append(out, Bound{Action: a, Keys: r.Keys(a.ID), Remapped: r.remaps[a.ID] != nil})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Action.ID < out[j].Action.ID })
	return out
}

type Bound struct {
	Action   Action
	Keys     []string
	Remapped bool
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
func sharesMode(a, b []string) bool { return len(intersect(a, b)) > 0 }
func intersect(a, b []string) []string {
	var out []string
	for _, x := range a {
		if contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}
