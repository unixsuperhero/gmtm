package app

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/actions"
	"github.com/unixsuperhero/gmtm/internal/capture"
	"github.com/unixsuperhero/gmtm/internal/inventory"
	"github.com/unixsuperhero/gmtm/internal/launch"
	"github.com/unixsuperhero/gmtm/internal/setup"
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
	records, err := capture.Open(path)
	if err != nil {
		return fmt.Errorf("open capture history: %w", err)
	}
	defer records.Close()
	setupManager, err := setup.New(setup.Options{Server: opts.Server, DB: path})
	if err != nil {
		return fmt.Errorf("configure capture setup: %w", err)
	}
	history := capture.NewService(client, records)
	inv := inventory.New(client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = inv.Run(ctx) }()
	a := New(Deps{Client: client, Inv: inv, Store: st, Launch: launch.New(client, inv, st), Registry: Registry(), Capture: records, History: history, Setup: setupManager, Project: opts.Project, WaitUpdates: true})
	program := tea.NewProgram(a, tea.WithAltScreen())
	historyDone := make(chan struct{})
	go func() {
		defer close(historyDone)
		if err := history.Run(ctx); err != nil && ctx.Err() == nil {
			program.Send(statusMsg{"capture collector stopped: " + err.Error()})
		}
	}()
	_, err = program.Run()
	cancel()
	<-historyDone
	if a.live != nil {
		_ = a.live.Stop()
	}
	return err
}
