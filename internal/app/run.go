package app

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/actions"
	"github.com/unixsuperhero/gmtm/internal/inventory"
	"github.com/unixsuperhero/gmtm/internal/launch"
	"github.com/unixsuperhero/gmtm/internal/store"
	"github.com/unixsuperhero/gmtm/internal/tuios"
)

// Options select the server, database and initial active project.
type Options struct {
	Server  string
	DB      string
	Project string
}

// Registry returns the action registry with every definition registered.
func Registry() *actions.Registry { return actions.NewDefault() }

// Run opens the store (reporting failure; never an ephemeral fallback),
// creates the single client for opts.Server, runs the inventory and the TUI.
// Exiting disconnects; managed windows and the daemon keep running.
func Run(opts Options) error {
	client, err := tuios.New(opts.Server)
	if err != nil {
		return err
	}
	path := opts.DB
	if path == "" {
		path = store.DefaultPath()
	}
	st, err := store.Open(path)
	if err != nil {
		return fmt.Errorf("open database %s: %w", path, err)
	}
	defer st.Close()
	inv := inventory.New(client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = inv.Run(ctx) }()
	a := New(Deps{Client: client, Inv: inv, Store: st, Launch: launch.New(client, inv, st), Registry: Registry(), Project: opts.Project, WaitUpdates: true})
	_, err = tea.NewProgram(a, tea.WithAltScreen()).Run()
	if a.live != nil {
		_ = a.live.Stop()
	}
	return err
}
