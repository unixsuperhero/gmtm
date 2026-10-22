package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/actions"
	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/ui/list"
)

// page is one registered page: a list plus an optional detail area. Each page
// owns its list (selection, search, filters, sort, grouping, scope, scroll)
// and its chosen detail tab.
type page struct {
	id, title string
	lst       list.Model
	build     func(domain.Snapshot, storeData) []list.Item
	// detail renders the area under the list (w×h); nil = list only.
	detail     func(a *App, w, h int) string
	detailRows int // rows reserved for the detail area (0 = none)
	tab        int // chosen detail tab (page-owned)
	empty      func(a *App) string
}

func (p *page) selected() (row, bool) {
	it, ok := p.lst.Selected()
	if !ok {
		return row{}, false
	}
	r, ok := it.(row)
	return r, ok
}

func (p *page) find(id string) (row, bool) {
	for _, it := range p.lst.Items() {
		if r, ok := it.(row); ok && r.id == id {
			return r, true
		}
	}
	return row{}, false
}

// refresh rebuilds items; list.SetItems keeps selection by stable ID and the
// list's query/filters/sort/grouping/scope/scroll are untouched.
func (p *page) refresh(s domain.Snapshot, d storeData, project string, scoped bool) {
	if p.id == actions.PagePanes {
		p.lst = p.lst.SetItems(windowRows(s, project, scoped))
	} else {
		p.lst = p.lst.SetItems(p.build(s, d))
	}
	p.lst.Pins, p.lst.Stars = d.Pins, d.Stars
}

func (p *page) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	p.lst, cmd = p.lst.Update(msg)
	return cmd
}

var paneTabs = []string{"Screen", "Activity", "Transcript", "Turns"}

func fd(name string, groupable bool) list.FieldDef {
	return list.FieldDef{Name: name, Filterable: true, Sortable: true, Groupable: groupable}
}

// newPages registers the pages in switcher order.
func newPages() []*page {
	panes := &page{id: actions.PagePanes, title: "Panes", detailRows: 12,
		lst:    list.New([]list.FieldDef{fd("host", true), fd("session", true), fd("project", true), fd("kind", true), fd("state", true), fd("attention", true), fd("harness", true), {Name: "lastActivity", Sortable: true}, {Name: "name", Sortable: true}}),
		detail: func(a *App, w, h int) string { return a.paneDetail(w, h) },
		empty:  func(a *App) string { return a.emptyInventory() }}
	panes.lst = panes.lst.SetGroupConfigs([]list.GroupConfig{{Name: "none"}, {Name: "by project", Fields: []string{"project", "session"}}, {Name: "by kind", Fields: []string{"kind"}}, {Name: "by attention", Fields: []string{"attention"}}})
	sessions := &page{id: actions.PageSessions, title: "Sessions",
		lst:   list.New([]list.FieldDef{fd("host", true), fd("project", true), {Name: "name", Sortable: true}}),
		build: func(s domain.Snapshot, _ storeData) []list.Item { return sessionRows(s) }, empty: func(a *App) string { return a.emptyInventory() }}
	hosts := &page{id: actions.PageHosts, title: "Hosts",
		lst:   list.New([]list.FieldDef{fd("status", true), {Name: "name", Sortable: true}}),
		build: func(s domain.Snapshot, _ storeData) []list.Item { return hostRows(s) }}
	projects := &page{id: actions.PageProjects, title: "Projects",
		lst:   list.New([]list.FieldDef{fd("source", true), {Name: "name", Sortable: true}}),
		build: projectRows}
	ctx := &page{id: actions.PageContext, title: "Context", detailRows: 8,
		lst:    list.New([]list.FieldDef{fd("kind", true), fd("project", true), fd("tags", true), {Name: "title", Sortable: true}}),
		build:  func(_ domain.Snapshot, d storeData) []list.Item { return contextRows(d) },
		detail: func(a *App, w, h int) string { return a.contextDetail(w, h) },
		empty:  func(*App) string { return "No saved context yet. Save from a window with 'a' or press F4 for a note." }}
	ctx.lst = ctx.lst.SetGroupConfigs([]list.GroupConfig{{Name: "none"}, {Name: "by project", Fields: []string{"project"}}, {Name: "by kind", Fields: []string{"kind"}}})
	settings := &page{id: actions.PageSettings, title: "Settings",
		lst:   list.New([]list.FieldDef{fd("state", true)}),
		build: func(_ domain.Snapshot, d storeData) []list.Item { return settingRows(d) }}
	return []*page{panes, sessions, hosts, projects, ctx, settings}
}
