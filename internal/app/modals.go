package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/ui/forms"
	"github.com/unixsuperhero/gmtm/internal/ui/help"
	"github.com/unixsuperhero/gmtm/internal/ui/list"
	"github.com/unixsuperhero/gmtm/internal/ui/modal"
	"github.com/unixsuperhero/gmtm/internal/ui/notemodal"
	"github.com/unixsuperhero/gmtm/internal/ui/palette"
	"github.com/unixsuperhero/gmtm/internal/ui/pathpicker"
	"github.com/unixsuperhero/gmtm/internal/ui/twopane"
)

// ptr adapts a pointer model (Update(msg) tea.Cmd) to modal.Component.
type ptr[T interface {
	Update(tea.Msg) tea.Cmd
	View() string
}] struct{ c T }

func (p ptr[T]) Update(m tea.Msg) (modal.Component, tea.Cmd) { return p, p.c.Update(m) }
func (p ptr[T]) View() string                                { return p.c.View() }

type ptrBody interface{ isPtr() }

func (p ptr[T]) isPtr() {}

func formOf(l modal.Layer) (*forms.Form, bool) {
	p, ok := l.Body.(ptr[*forms.Form])
	return p.c, ok
}

func (a *App) layer(id string) (modal.Layer, bool) {
	for _, l := range a.modals.Layers() {
		if l.ID == id {
			return l, true
		}
	}
	return modal.Layer{}, false
}

func (a *App) note() (*notemodal.Model, bool) {
	l, ok := a.layer("note")
	if !ok {
		return nil, false
	}
	p, ok := l.Body.(ptr[*notemodal.Model])
	return p.c, ok
}

// removeLayer drops the layer with id wherever it is, keeping the others.
func (a *App) removeLayer(id string) {
	var s modal.Stack
	for _, l := range a.modals.Layers() {
		if l.ID != id {
			s = s.Push(l)
		}
	}
	a.modals = s
}

func (a *App) modalSize() (int, int) {
	iw, ih := a.frame().Inner()
	return max(10, iw-4), max(3, ih-4)
}

func (a *App) push(id, title string, body modal.Component, full bool) {
	l := modal.Layer{ID: id, Title: title, Body: body}
	if full {
		iw, ih := a.frame().Inner()
		l.Width, l.Height = iw-2, ih-2
	}
	a.modals = a.modals.Push(l)
}

func (a *App) resizeModals() {
	w, h := a.modalSize()
	var s modal.Stack
	for _, l := range a.modals.Layers() {
		switch b := l.Body.(type) {
		case ptr[*forms.Form]:
			b.c.Width = w
		case ptr[*notemodal.Model]:
			b.c.SetSize(w, h)
		case ptr[*pathpicker.Model]:
			b.c.SetSize(w, h)
			iw, ih := a.frame().Inner()
			l.Width, l.Height = iw-2, ih-2
		case ptr[*twopane.Model]:
			b.c.SetSize(w, h)
			iw, ih := a.frame().Inner()
			l.Width, l.Height = iw-2, ih-2
		}
		s = s.Push(l)
	}
	a.modals = s
}

// broadcastModals delivers a non-key message to every pointer layer (async
// replies are bound by their own ids/generations) and to the top value layer.
func (a *App) broadcastModals(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	ls := a.modals.Layers()
	for i, l := range ls {
		if _, ok := l.Body.(ptrBody); ok {
			_, c := l.Body.Update(msg)
			cmds = append(cmds, c)
		} else if i == len(ls)-1 {
			var c tea.Cmd
			a.modals, c = a.modals.Update(msg)
			cmds = append(cmds, c)
		}
	}
	return tea.Batch(cmds...)
}

// confirm asks a yes/no question in a modal; fn runs on yes.
func (a *App) confirm(id, question string, fn func() tea.Cmd) {
	a.confirms[id] = fn
	a.push("confirm:"+id, "Confirm", modal.Wrap(confirmBox{id: id, q: question}), false)
}

type confirmBox struct{ id, q string }

func (c confirmBox) Update(msg tea.Msg) (confirmBox, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return c, nil
	}
	switch strings.ToLower(k.String()) {
	case "y":
		return c, func() tea.Msg { return confirmMsg{c.id, true} }
	case "n", "esc", "q":
		return c, func() tea.Msg { return confirmMsg{c.id, false} }
	}
	return c, nil
}

func (c confirmBox) View() string { return c.q + "\n\n y yes · n/esc no" }

// handleModuleMsg handles the typed messages modal bodies emit.
func (a *App) handleModuleMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case palette.ExecuteMsg:
		a.removeLayer("palette")
		return a.exec(m.ActionID, m.TargetID), true
	case palette.CloseMsg:
		a.removeLayer("palette")
		return nil, true
	case help.CloseMsg:
		a.removeLayer("help")
		return nil, true
	case list.CustomizeGroupsMsg:
		return a.exec("group.customize", ""), true
	case list.GroupConfigChosenMsg:
		a.removeLayer("group")
		p := a.page()
		p.lst = p.lst.ApplyGroupConfig(m.Config)
		return nil, true
	case list.GroupConfigCancelledMsg:
		a.removeLayer("group")
		return nil, true
	case forms.PickRequestMsg:
		return a.openPicker(m), true
	case pathpicker.ChosenMsg:
		return a.pickDone(m.Paths, nil), true
	case pathpicker.CancelMsg:
		return a.pickDone(nil, forms.ErrPickerCanceled), true
	case twopaneMsg:
		return a.onTwopane(m), true
	case twopane.CloseMsg:
		for _, id := range []string{"jump", "context", "meta"} {
			if top, ok := a.modals.Top(); ok && top.ID == id {
				a.removeLayer(id)
			}
		}
		return nil, true
	case twopane.OpenMsg:
		top, _ := a.modals.Top()
		a.removeLayer(top.ID)
		return openContent(a.twoSrc, m), true
	case notemodal.SaveMsg:
		return a.saveNote(m), true
	case noteSavedMsg:
		n, ok := a.note()
		if m.err != nil {
			if ok {
				n.SetError(m.err.Error())
			}
			a.status = "note not saved: " + m.err.Error()
			return nil, true
		}
		if ok {
			n.SetSaved()
		}
		a.removeLayer("note")
		a.status = "note saved"
		return a.loadStore(), true
	case notemodal.CancelMsg:
		if !m.Dirty {
			a.removeLayer("note")
			return nil, true
		}
		a.confirm("note-discard", "Discard the unsaved note?", func() tea.Cmd { a.removeLayer("note"); return nil })
		return nil, true
	case forms.SubmitMsg:
		return a.submit(m), true
	case forms.CancelMsg:
		id := "form:" + m.FormID
		if !m.Dirty {
			a.removeLayer(id)
			return nil, true
		}
		a.confirm("discard-"+m.FormID, "Discard the unsaved form draft?", func() tea.Cmd { a.removeLayer(id); return nil })
		return nil, true
	case txDoneMsg:
		if m.err != nil {
			a.status = "not saved: " + m.err.Error() + " (draft kept)"
			return nil, true
		}
		if m.layer != "" {
			a.removeLayer(m.layer)
		}
		a.status = m.ok
		return a.loadStore(), true
	case launchMsg:
		return a.onLaunch(m), true
	case closedMsg:
		if m.err != nil {
			a.status = "close failed: " + m.err.Error() + " (window kept)"
			return nil, true
		}
		a.status = "closed " + m.target.Key()
		return a.refreshCmd(), true
	}
	return nil, false
}
