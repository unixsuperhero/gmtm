package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/setup"
)

type setupStatusMsg struct {
	items   []setup.Item
	err     error
	install bool
	installed bool
}

func (a *App) captureSetupStatus(install bool) tea.Cmd {
	manager := a.d.Setup
	if manager == nil {
		a.status = "capture setup unavailable: no setup manager configured"
		return nil
	}
	a.status = "inspecting capture setup…"
	return func() tea.Msg {
		items, err := manager.Status()
		return setupStatusMsg{items: items, err: err, install: install}
	}
}

func setupSummary(items []setup.Item) string {
	var b strings.Builder
	for _, item := range items {
		fmt.Fprintf(&b, "%s: %s\n%s\n%s\n\n", item.ID, item.Status, item.Path, item.Detail)
	}
	return sanitizeLine(b.String())
}

func (a *App) onSetupStatus(m setupStatusMsg) tea.Cmd {
	if m.err != nil {
		a.status = "capture setup: " + m.err.Error()
		return nil
	}
	a.status = ""
	if m.installed {
		a.content = &contentView{title: "Capture setup installed", text: setupSummary(m.items) + "Reload OMP and open a new shell to activate capture.\nUse setup status to inspect files; setup edit opens them in your editor."}
		return nil
	}
	if !m.install {
		a.content = &contentView{title: "Capture setup status", text: setupSummary(m.items)}
		return nil
	}
	manager := a.d.Setup
	a.confirm("capture-setup", "Install capture integration for "+a.d.Client.Server()+"?\n\n"+setupSummary(m.items)+"Existing unrelated configuration is preserved. Reload OMP and open a new shell after installation.", func() tea.Cmd {
		a.status = "installing capture integration…"
		return func() tea.Msg {
			items, err := manager.Install()
			return setupStatusMsg{items: items, err: err, installed: true}
		}
	})
	return nil
}
