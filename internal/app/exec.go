package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/launch"
	"github.com/unixsuperhero/gmtm/internal/live"
	"github.com/unixsuperhero/gmtm/internal/store"
	"github.com/unixsuperhero/gmtm/internal/tuios"
	"github.com/unixsuperhero/gmtm/internal/ui/help"
	"github.com/unixsuperhero/gmtm/internal/ui/keys"
	"github.com/unixsuperhero/gmtm/internal/ui/list"
	"github.com/unixsuperhero/gmtm/internal/ui/modal"
	"github.com/unixsuperhero/gmtm/internal/ui/notemodal"
	"github.com/unixsuperhero/gmtm/internal/ui/palette"
)

// target resolves the row an action applies to: the palette's TargetID when
// given, else the current page's selection.
func (a *App) target(id string) (row, bool) {
	if id != "" {
		return a.page().find(id)
	}
	return a.page().selected()
}

// exec runs one registry action. Keys, the palette and help all land here.
func (a *App) exec(id, targetID string) tea.Cmd {
	p := a.page()
	r, hasRow := a.target(targetID)
	needWin := func() (*domain.Window, bool) {
		if !hasRow || r.win == nil {
			a.status = "select a window on the Panes page first"
			return nil, false
		}
		return r.win, true
	}
	switch id {
	case "note.new":
		return a.openNote()
	case "new.terminal", "new.agent", "new.vim":
		return a.quickCreate(domain.Kind(strings.TrimPrefix(id, "new.")), r, hasRow)
	case "form.terminal", "form.agent", "form.vim":
		return a.openCreateForm(domain.Kind(strings.TrimPrefix(id, "form.")), r, hasRow)
	case "open.live":
		w, ok := needWin()
		if !ok {
			return nil
		}
		return a.startLive(w.Target)
	case "close.window":
		w, ok := needWin()
		if !ok {
			return nil
		}
		t, c := w.Target, a.d.Client
		a.confirm("close", fmt.Sprintf("Close window %s (%s)?\nThis does not complete a project or acknowledge output.", windowName(*w), t.Key()), func() tea.Cmd {
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				return closedMsg{err: tuios.CloseWindow(ctx, c, t.Session, t.WindowID), target: t}
			}
		})
		return nil
	case "pin", "star":
		if !hasRow {
			a.status = "nothing selected"
			return nil
		}
		on := !a.data.Pins[r.id]
		if id == "star" {
			on = !a.data.Stars[r.id]
		}
		key := r.id
		return a.tx(id+" "+onOff(on), "", func(tx store.Tx) error {
			if id == "pin" {
				return tx.SetPinned(key, on)
			}
			return tx.SetStarred(key, on)
		})
	case "detail.next", "detail.prev":
		d := 1
		if id == "detail.prev" {
			d = len(paneTabs) - 1
		}
		a.panes().tab = (a.panes().tab + d) % len(paneTabs)
		a.actTarget = ""
		return nil
	case "prompt.retry":
		if a.retry == nil {
			a.status = "no failed prompt to retry"
			return nil
		}
		rt, l := *a.retry, a.d.Launch
		return func() tea.Msg {
			o, reason := l.RetryPrompt(context.Background(), rt.target, rt.prompt, rt.queue)
			return statusMsg{fmt.Sprintf("retry to %s: %s (%s)", rt.target.Key(), o, reason)}
		}
	case "context.save":
		w, ok := needWin()
		if !ok {
			return nil
		}
		return a.openContextSaveForm(*w)
	case "group.cycle":
		p.lst = p.lst.CycleGroup()
		return nil
	case "group.customize":
		a.push("group", "Grouping", modal.Wrap(list.NewGroupConfigModal(p.lst)), false)
		return nil
	case "search":
		p.lst = p.lst.SetSearching(true)
		return nil
	case "clear.filters":
		p.lst = p.lst.ClearAll()
		return nil
	case "palette":
		w, h := a.modalSize()
		o := palette.Options{Page: p.id, Width: w, Height: h}
		if hasRow {
			o.TargetID, o.TargetLabel = r.id, strings.Join(strings.Fields(r.label), " ")
		}
		a.push("palette", "Palette", modal.Wrap(palette.New(a.reg, o)), false)
		return nil
	case "help":
		return a.openHelp("normal")
	case "jump", "context", "metamode":
		mode := map[string]string{"jump": "jump", "context": "context", "metamode": "meta"}[id]
		return a.openTwopane(mode)
	case "project.choose":
		return a.openProjectChooser()
	case "project.activate":
		if !hasRow || !r.isProj {
			return nil
		}
		a.setActive(r.project)
		return nil
	case "context.open":
		if !hasRow || r.item == nil {
			return nil
		}
		a.content = &contentView{title: r.item.Title, text: itemText(*r.item)}
		return nil
	case "context.reassociate":
		if !hasRow || r.item == nil {
			return nil
		}
		return a.openReassociateForm(*r.item)
	case "context.clear":
		if !hasRow || r.item == nil {
			return nil
		}
		iid := r.item.ID
		return a.tx("project association cleared (no-project)", "", func(tx store.Tx) error { return tx.SetContextProject(iid, "") })
	case "setting.edit":
		if !hasRow || r.setting == nil {
			return nil
		}
		return a.openSettingForm(*r.setting)
	case "setting.reset":
		if !hasRow || r.setting == nil {
			return nil
		}
		k := r.setting.Key
		return a.tx(k+" reset to default", "", func(tx store.Tx) error { return tx.ResetSetting(k) })
	case "server.start":
		c, inv := a.d.Client, a.d.Inv
		a.status = "starting server " + c.Server() + "…"
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := c.StartServer(ctx); err != nil {
				return serverStartMsg{err}
			}
			return serverStartMsg{inv.Refresh(ctx)}
		}
	case "refresh":
		return a.refreshCmd()
	case "quit":
		s := a.live
		a.live = nil
		return tea.Sequence(func() tea.Msg {
			if s != nil {
				_ = s.Stop()
			}
			return nil
		}, tea.Quit)
	case "page.next":
		a.setPage((a.cur + 1) % len(a.pages))
		return nil
	case "page.prev":
		a.setPage((a.cur + len(a.pages) - 1) % len(a.pages))
		return nil
	}
	if strings.HasPrefix(id, "page.") {
		for i, pg := range a.pages {
			if "page."+pg.id == id {
				a.setPage(i)
			}
		}
		return nil
	}
	if strings.HasPrefix(id, "live.") {
		a.status = "live leader actions apply in live mode (" + a.leader + " then the key)"
		return nil
	}
	a.status = "action " + id + " is not available here"
	return nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (a *App) setPage(i int) {
	a.cur = i
	a.content = nil
}

func (a *App) setActive(project string) {
	a.active, a.activeNone = project, project == ""
	a.status = "active project: " + a.projectText()
}

func (a *App) openHelp(mode string) tea.Cmd {
	w, h := a.modalSize()
	a.push("help", "Help", modal.Wrap(help.New(a.reg, help.Context{Mode: mode, Page: a.page().id, Leader: a.leader}).SetSize(w, h)), false)
	return nil
}

func (a *App) openNote() tea.Cmd {
	if n, ok := a.note(); ok {
		n.Focus() // repeat invocation focuses the existing draft
		return nil
	}
	n := notemodal.New(a.projectText(), projectNames(a.snap, a.data))
	w, h := a.modalSize()
	n.SetSize(w, h)
	a.push("note", "New note", ptr[*notemodal.Model]{n}, false)
	return nil
}

func (a *App) saveNote(m notemodal.SaveMsg) tea.Cmd {
	st := a.d.Store
	project := m.Project
	if m.NoProject {
		project = ""
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx, err := st.Begin(ctx)
		if err != nil {
			return noteSavedMsg{err}
		}
		if err := tx.SaveContextItem(&domain.ContextItem{Kind: "note", Body: m.Text, Project: project}); err != nil {
			_ = tx.Rollback()
			return noteSavedMsg{err}
		}
		return noteSavedMsg{tx.Commit()}
	}
}

// tx runs fn in one store transaction; on success layer (if any) is popped.
func (a *App) tx(ok, layer string, fn func(store.Tx) error) tea.Cmd {
	st := a.d.Store
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx, err := st.Begin(ctx)
		if err != nil {
			return txDoneMsg{err: err}
		}
		if err := fn(tx); err != nil {
			_ = tx.Rollback()
			return txDoneMsg{err: err}
		}
		if err := tx.Commit(); err != nil {
			return txDoneMsg{err: err}
		}
		return txDoneMsg{ok: ok, layer: layer}
	}
}

// ---- creation ----

// quickCreate uses the focused row's project only; focus never selects the
// destination window or session.
func (a *App) quickCreate(kind domain.Kind, r row, hasRow bool) tea.Cmd {
	spec := domain.LaunchSpec{Kind: kind, Host: domain.HostLocal}
	if hasRow {
		spec.Project = r.rowProject()
	}
	if kind == domain.KindAgent {
		if spec.Harness = a.d.Launch.DefaultHarness(); spec.Harness == "" {
			a.status = "no recognized agent harness (claude, codex, gemini) is installed"
			return nil
		}
	}
	return a.launchCmd(spec, "")
}

func (a *App) launchCmd(spec domain.LaunchSpec, formID string) tea.Cmd {
	l := a.d.Launch
	a.status = fmt.Sprintf("creating %s in session %s…", spec.Kind, launch.SessionFor(spec))
	return func() tea.Msg {
		res, err := l.Launch(context.Background(), spec)
		return launchMsg{res: res, err: err, spec: spec, formID: formID}
	}
}

func (a *App) onLaunch(m launchMsg) tea.Cmd {
	if m.err != nil {
		a.status = "launch failed: " + m.err.Error()
		if m.formID != "" {
			a.status += " (form draft kept)"
		}
		return nil
	}
	if m.formID != "" {
		a.removeLayer("form:" + m.formID)
	}
	s := fmt.Sprintf("created %s %s", m.spec.Kind, m.res.Target.Key())
	if m.res.Outcome != "" {
		s += fmt.Sprintf(" · prompt %s", m.res.Outcome)
	}
	if m.res.Reason != "" {
		s += " (" + m.res.Reason + ")"
	}
	switch m.res.Outcome {
	case domain.OutcomeFailed, domain.OutcomeBlocked, domain.OutcomeUnknown:
		a.retry = &retryInfo{target: m.res.Target, prompt: m.spec.Prompt, queue: m.spec.Queue}
		s += " · R retries on the same window"
	}
	a.status = s
	return tea.Batch(a.refreshCmd(), a.loadStore())
}

// ---- live ----

func (a *App) startLive(t domain.Target) tea.Cmd {
	cols, rows := a.liveSize()
	old, c, leader := a.live, a.d.Client, a.leader
	a.live, a.liveStarting = nil, true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if old != nil { // stop and release the old lease before acquiring the next
			s, err := old.Switch(ctx, t, live.WithLeader(leader))
			return liveStartMsg{s, err}
		}
		s, err := live.Start(ctx, c, t, cols, rows, live.WithLeader(leader))
		return liveStartMsg{s, err}
	}
}

func (a *App) stopLive(reason string) tea.Cmd {
	s := a.live
	a.live = nil
	if s == nil {
		return nil
	}
	return func() tea.Msg { return liveStoppedMsg{err: s.Stop(), reason: reason} }
}

func (a *App) handleLiveMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case liveStartMsg:
		a.liveStarting = false
		if m.err != nil {
			a.status = "live failed: " + m.err.Error()
			return nil, true
		}
		a.live = m.s
		a.status = ""
		return m.s.Init(), true
	case liveStoppedMsg:
		a.status = "left live mode"
		if m.reason != "" {
			a.status += ": " + m.reason
		}
		if m.err != nil {
			a.status += " (release: " + m.err.Error() + ")"
		}
		return nil, true
	case live.DetachMsg:
		return a.stopLive(""), true
	case live.ExitMsg:
		if a.live != nil && a.live.ID == m.ID {
			return a.stopLive(m.Reason), true
		}
		return nil, true
	case live.ErrorMsg:
		if a.live != nil && a.live.ID == m.ID {
			a.status = "live: " + m.Err.Error()
		}
		return nil, true
	case live.SwitchMsg:
		if a.live == nil {
			return nil, true
		}
		p := a.panes()
		before := p.lst.SelectedID()
		nav := keys.Down
		if m.Delta < 0 {
			nav = keys.Up
		}
		p.lst = p.lst.Move(nav)
		w, ok := a.selectedWindow()
		if !ok || p.lst.SelectedID() == before {
			a.status = "no other pane to switch to"
			return nil, true
		}
		return a.startLive(w.Target), true
	case live.ZoomMsg:
		a.status = "zoom/fullscreen: the installed TUIOS API has no zoom verb (unsupported)"
		return nil, true
	case live.LeaderHelpMsg:
		return a.openHelp("live"), true
	case live.UnknownLeaderMsg:
		a.status = fmt.Sprintf("unknown leader command %s %s (consumed, not sent); %s ? for help", a.leader, m.Key, a.leader)
		return nil, true
	}
	return nil, false
}

// ---- settings ----

func (a *App) applySettings() {
	changed := false
	for _, s := range a.data.Settings {
		if s.Key == "live.leader" {
			a.leader = s.Value
			continue
		}
		id, ok := settingAction[s.Key]
		if !ok {
			continue
		}
		cur := a.reg.Keys(id)
		if len(cur) == 1 && cur[0] == s.Value {
			continue
		}
		if err := a.reg.Remap(id, []string{s.Value}); err != nil {
			a.status = "setting " + s.Key + " ignored: " + err.Error()
			continue
		}
		changed = true
	}
	if changed {
		a.rebuildRouter()
	}
}
