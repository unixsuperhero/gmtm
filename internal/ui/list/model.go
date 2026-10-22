package list

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/unixsuperhero/gmtm/internal/ui/keys"
)

// KeyMap holds the list's own (configurable) keys.
type KeyMap struct {
	Search         string
	Clear          string
	CycleGroup     string
	CustomizeGroup string
}

// DefaultKeyMap: "/" search, "C" clear, z/Z grouping.
var DefaultKeyMap = KeyMap{Search: "/", Clear: "C", CycleGroup: "z", CustomizeGroup: "Z"}

// CustomizeGroupsMsg asks the app to open the GroupConfigModal.
type CustomizeGroupsMsg struct{}

// Model is the list widget (value type).
type Model struct {
	Keys   KeyMap
	Detail func(Item) string // optional second line for the selected row
	Pins   map[string]bool
	Stars  map[string]bool

	defs      []FieldDef
	items     []Item
	query     string
	searching bool
	filters   map[string]map[string]bool
	sort      SortSpec
	groups    []GroupConfig
	groupIdx  int

	res        Result
	sel        int
	selectedID string
	offset     int // first visible body line
	width      int
	height     int
}

// New creates an empty list with the given field definitions.
func New(defs []FieldDef) Model {
	m := Model{Keys: DefaultKeyMap, defs: defs, filters: map[string]map[string]bool{},
		groups: []GroupConfig{{Name: "none"}}, Pins: map[string]bool{}, Stars: map[string]bool{}}
	return m.refresh()
}

// SetSize sets the widget's cell size.
func (m Model) SetSize(w, h int) Model { m.width, m.height = w, h; return m.ensureVisible() }

// SetItems replaces the data, keeping selection by stable ID when present.
func (m Model) SetItems(items []Item) Model { m.items = items; return m.refresh() }

// Items returns all items (unfiltered).
func (m Model) Items() []Item { return m.items }

// Defs returns the field definitions.
func (m Model) Defs() []FieldDef { return m.defs }

// SetQuery sets the search query.
func (m Model) SetQuery(q string) Model { m.query = q; return m.refresh() }

// Query returns the search query.
func (m Model) Query() string { return m.query }

// Searching reports whether the search input has focus (keys are text).
func (m Model) Searching() bool { return m.searching }

// SetSearching focuses or blurs the search input.
func (m Model) SetSearching(on bool) Model { m.searching = on; return m }

// SetFilter replaces field's filter with values (none clears it).
func (m Model) SetFilter(field string, values ...string) Model {
	m.filters = cloneFilters(m.filters)
	if len(values) == 0 {
		delete(m.filters, field)
	} else {
		set := map[string]bool{}
		for _, v := range values {
			set[v] = true
		}
		m.filters[field] = set
	}
	return m.refresh()
}

// ToggleFilter adds or removes one value from field's filter.
func (m Model) ToggleFilter(field, value string) Model {
	m.filters = cloneFilters(m.filters)
	set := m.filters[field]
	if set == nil {
		set = map[string]bool{}
		m.filters[field] = set
	}
	if set[value] {
		delete(set, value)
	} else {
		set[value] = true
	}
	if len(set) == 0 {
		delete(m.filters, field)
	}
	return m.refresh()
}

// Filters returns active filters as field → sorted values.
func (m Model) Filters() map[string][]string {
	out := map[string][]string{}
	for f, set := range m.filters {
		out[f] = sortedKeys(set)
	}
	return out
}

func cloneFilters(in map[string]map[string]bool) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(in))
	for k, v := range in {
		c := make(map[string]bool, len(v))
		for x := range v {
			c[x] = true
		}
		out[k] = c
	}
	return out
}

// SetSort sets the active sort.
func (m Model) SetSort(s SortSpec) Model { m.sort = s; return m.refresh() }

// Sort returns the active sort.
func (m Model) Sort() SortSpec { return m.sort }

// SetGroupConfigs replaces the grouping configs cycled by z (index 0 active).
func (m Model) SetGroupConfigs(cfgs []GroupConfig) Model {
	if len(cfgs) == 0 {
		cfgs = []GroupConfig{{Name: "none"}}
	}
	m.groups = append([]GroupConfig(nil), cfgs...)
	m.groupIdx = 0
	return m.refresh()
}

// GroupConfigs returns all grouping configs and the active index.
func (m Model) GroupConfigs() ([]GroupConfig, int) { return m.groups, m.groupIdx }

// GroupConfig returns the active grouping config.
func (m Model) GroupConfig() GroupConfig { return m.groups[m.groupIdx] }

// CycleGroup activates the next grouping config.
func (m Model) CycleGroup() Model {
	m.groupIdx = (m.groupIdx + 1) % len(m.groups)
	return m.refresh()
}

// ApplyGroupConfig replaces the config with the same name (or appends) and activates it.
func (m Model) ApplyGroupConfig(c GroupConfig) Model {
	m.groups = append([]GroupConfig(nil), m.groups...)
	for i, g := range m.groups {
		if g.Name == c.Name {
			m.groups[i] = c
			m.groupIdx = i
			return m.refresh()
		}
	}
	m.groups = append(m.groups, c)
	m.groupIdx = len(m.groups) - 1
	return m.refresh()
}

// SetScope sets the included groups of the active config.
func (m Model) SetScope(groups ...string) Model {
	m.groups = append([]GroupConfig(nil), m.groups...)
	m.groups[m.groupIdx].Scope = append([]string(nil), groups...)
	return m.refresh()
}

// ClearAll clears query, filters, sort and scope (grouping fields stay).
func (m Model) ClearAll() Model {
	m.query = ""
	m.searching = false
	m.filters = map[string]map[string]bool{}
	m.sort = SortSpec{}
	m.groups = append([]GroupConfig(nil), m.groups...)
	m.groups[m.groupIdx].Scope = nil
	return m.refresh()
}

// HasActive reports whether any query/filter/sort/scope is active.
func (m Model) HasActive() bool {
	return m.query != "" || len(m.filters) > 0 || m.sort.Field != "" || len(m.GroupConfig().Scope) > 0
}

// Result returns the computed view.
func (m Model) Result() Result { return m.res }

// Selected returns the selected item.
func (m Model) Selected() (Item, bool) {
	if m.sel >= 0 && m.sel < len(m.res.Items) {
		return m.res.Items[m.sel], true
	}
	return nil, false
}

// SelectedID returns the selected item's stable id ("" if none).
func (m Model) SelectedID() string { return m.selectedID }

// Select selects the item with id if visible.
func (m Model) Select(id string) Model {
	for i, it := range m.res.Items {
		if it.ID() == id {
			return m.setSel(i)
		}
	}
	return m
}

func (m Model) setSel(i int) Model {
	if n := len(m.res.Items); i >= n {
		i = n - 1
	}
	if i < 0 {
		i = 0
	}
	m.sel = i
	m.selectedID = ""
	if it, ok := m.Selected(); ok {
		m.selectedID = it.ID()
	}
	return m.ensureVisible()
}

func (m Model) refresh() Model {
	m.res = Compute(m.items, m.query, m.filters, m.sort, m.groups[m.groupIdx])
	if m.selectedID != "" {
		for i, it := range m.res.Items {
			if it.ID() == m.selectedID {
				return m.setSel(i)
			}
		}
	}
	return m.setSel(m.sel)
}

// fieldNames returns def'd fields matching pred plus observed undeclared fields.
func (m Model) fieldNames(pred func(FieldDef) bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range m.defs {
		seen[d.Name] = true
		if pred(d) {
			out = append(out, d.Name)
		}
	}
	var extra []string
	for _, it := range m.items {
		for k := range it.Fields() {
			if !seen[k] {
				seen[k] = true
				extra = append(extra, k)
			}
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// FilterableFields lists fields that can be filtered.
func (m Model) FilterableFields() []string {
	return m.fieldNames(func(d FieldDef) bool { return d.Filterable })
}

// GroupableFields lists fields that can be grouped.
func (m Model) GroupableFields() []string {
	return m.fieldNames(func(d FieldDef) bool { return d.Groupable })
}

// SortableFields lists fields that can be sorted.
func (m Model) SortableFields() []string {
	return m.fieldNames(func(d FieldDef) bool { return d.Sortable })
}

// FilterValues unions a field's defined values with observed values.
func (m Model) FilterValues(field string) []string {
	set := map[string]bool{}
	for _, d := range m.defs {
		if d.Name == field {
			for _, v := range d.Values {
				set[v] = true
			}
		}
	}
	for _, it := range m.items {
		if v, ok := it.Fields()[field]; ok && v != "" {
			set[v] = true
		}
	}
	return sortedKeys(set)
}

// Update handles keys and paste.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	key := km.String()
	if m.searching {
		switch {
		case key == "enter" || key == "esc":
			m.searching = false
			return m, nil
		case key == "backspace":
			r := []rune(m.query)
			if len(r) > 0 {
				m.query = string(r[:len(r)-1])
			}
			return m.refresh(), nil
		case key == "ctrl+u":
			return m.SetQuery(""), nil
		case key == "up" || key == "ctrl+p":
			return m.setSel(m.sel - 1), nil
		case key == "down" || key == "ctrl+n":
			return m.setSel(m.sel + 1), nil
		case km.Type == tea.KeyRunes || km.Type == tea.KeySpace:
			m.query += strings.ReplaceAll(string(km.Runes), "\n", " ")
			return m.refresh(), nil
		}
		return m, nil
	}
	switch key {
	case m.Keys.Search:
		m.searching = true
		return m, nil
	case m.Keys.Clear:
		return m.ClearAll(), nil
	case m.Keys.CycleGroup:
		return m.CycleGroup(), nil
	case m.Keys.CustomizeGroup:
		return m, func() tea.Msg { return CustomizeGroupsMsg{} }
	}
	if n, ok := keys.NavFor(key); ok {
		return m.Move(n), nil
	}
	return m, nil
}

func (m Model) bodyHeight() int {
	switch {
	case m.height >= 3:
		return m.height - 2
	case m.height == 2:
		return 1
	}
	return max(m.height, 1)
}

// Move applies a navigation motion to the selection.
func (m Model) Move(n keys.Nav) Model {
	half := max(1, m.bodyHeight()/2)
	switch n {
	case keys.Down:
		return m.setSel(m.sel + 1)
	case keys.Up:
		return m.setSel(m.sel - 1)
	case keys.HalfDown:
		return m.setSel(m.sel + half)
	case keys.HalfUp:
		return m.setSel(m.sel - half)
	case keys.PageDown:
		return m.setSel(m.sel + max(1, m.bodyHeight()))
	case keys.PageUp:
		return m.setSel(m.sel - max(1, m.bodyHeight()))
	case keys.Top:
		return m.setSel(0)
	case keys.Bottom:
		return m.setSel(len(m.res.Items) - 1)
	case keys.NextSection, keys.PrevSection:
		return m.section(n == keys.NextSection)
	}
	return m
}

func (m Model) firstOfGroup(g int) int {
	for i, gi := range m.res.GroupOf {
		if gi == g {
			return i
		}
	}
	return m.sel
}

func (m Model) section(next bool) Model {
	if len(m.res.Groups) == 0 || len(m.res.Items) == 0 {
		if next {
			return m.setSel(len(m.res.Items) - 1)
		}
		return m.setSel(0)
	}
	g := m.res.GroupOf[m.sel]
	if next {
		if g+1 < len(m.res.Groups) {
			return m.setSel(m.firstOfGroup(g + 1))
		}
		return m.setSel(len(m.res.Items) - 1)
	}
	if first := m.firstOfGroup(g); first != m.sel {
		return m.setSel(first)
	}
	if g > 0 {
		return m.setSel(m.firstOfGroup(g - 1))
	}
	return m
}
