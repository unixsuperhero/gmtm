package list

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/unixsuperhero/gmtm/internal/ui/frame"
)

// GroupConfigChosenMsg carries the config chosen in GroupConfigModal.
type GroupConfigChosenMsg struct{ Config GroupConfig }

// GroupConfigCancelledMsg reports the modal was dismissed.
type GroupConfigCancelledMsg struct{}

// GroupConfigModal picks grouping fields (and order) and included groups.
//
// Keys: j/k move, tab switches Fields/Groups, space toggles, J/K move a chosen
// field down/up in the order, a includes all groups, enter applies, esc cancels.
type GroupConfigModal struct {
	name     string
	items    []Item
	fields   []string // chosen first (in order), then the rest
	chosen   map[string]bool
	groups   []string
	excluded map[string]bool
	section  int
	cursor   [2]int
	Width    int
}

// NewGroupConfigModal starts from m's active config.
func NewGroupConfigModal(m Model) GroupConfigModal {
	cfg := m.GroupConfig()
	g := GroupConfigModal{name: cfg.Name, items: m.items, chosen: map[string]bool{}, excluded: map[string]bool{}}
	if g.name == "none" || g.name == "" {
		g.name = "custom"
	}
	g.fields = append(g.fields, cfg.Fields...)
	for _, f := range cfg.Fields {
		g.chosen[f] = true
	}
	for _, f := range m.GroupableFields() {
		if !g.chosen[f] {
			g.fields = append(g.fields, f)
		}
	}
	g = g.recompute()
	if len(cfg.Scope) > 0 {
		in := map[string]bool{}
		for _, s := range cfg.Scope {
			in[s] = true
		}
		for _, k := range g.groups {
			if !in[k] {
				g.excluded[k] = true
			}
		}
	}
	return g
}

// ChosenFields is the ordered list of grouping fields.
func (g GroupConfigModal) ChosenFields() []string {
	var out []string
	for _, f := range g.fields {
		if g.chosen[f] {
			out = append(out, f)
		}
	}
	return out
}

// Groups lists group keys for the chosen fields.
func (g GroupConfigModal) Groups() []string { return g.groups }

// Config builds the GroupConfig the modal currently describes.
func (g GroupConfigModal) Config() GroupConfig {
	c := GroupConfig{Name: g.name, Fields: g.ChosenFields()}
	if len(g.excluded) > 0 {
		for _, k := range g.groups {
			if !g.excluded[k] {
				c.Scope = append(c.Scope, k)
			}
		}
	}
	return c
}

func (g GroupConfigModal) recompute() GroupConfigModal {
	fields := g.ChosenFields()
	g.groups = nil
	if len(fields) > 0 {
		g.groups = Compute(g.items, "", nil, SortSpec{}, GroupConfig{Fields: fields}).AllKeys
	}
	ex := map[string]bool{}
	for _, k := range g.groups {
		if g.excluded[k] {
			ex[k] = true
		}
	}
	g.excluded = ex
	if g.cursor[1] >= len(g.groups) {
		g.cursor[1] = max(0, len(g.groups)-1)
	}
	return g
}

func (g GroupConfigModal) Init() tea.Cmd { return nil }

// Update handles keys.
func (g GroupConfigModal) Update(msg tea.Msg) (GroupConfigModal, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return g, nil
	}
	n := len(g.fields)
	if g.section == 1 {
		n = len(g.groups)
	}
	c := &g.cursor[g.section]
	switch km.String() {
	case "esc", "q":
		return g, func() tea.Msg { return GroupConfigCancelledMsg{} }
	case "enter":
		cfg := g.Config()
		return g, func() tea.Msg { return GroupConfigChosenMsg{Config: cfg} }
	case "tab", "shift+tab", "h", "l", "left", "right":
		g.section = 1 - g.section
	case "j", "down":
		if *c < n-1 {
			*c++
		}
	case "k", "up":
		if *c > 0 {
			*c--
		}
	case "g", "home":
		*c = 0
	case "G", "end":
		*c = max(0, n-1)
	case " ", "space", "x":
		if n == 0 {
			break
		}
		if g.section == 0 {
			f := g.fields[*c]
			g.chosen[f] = !g.chosen[f]
			g = g.reorderChosen()
			g = g.recompute()
		} else {
			k := g.groups[*c]
			if g.excluded[k] {
				delete(g.excluded, k)
			} else {
				g.excluded[k] = true
			}
		}
	case "a":
		g.excluded = map[string]bool{}
	case "J", "K":
		if g.section == 0 && n > 0 && g.chosen[g.fields[*c]] {
			d := 1
			if km.String() == "K" {
				d = -1
			}
			j := *c + d
			if j >= 0 && j < n && g.chosen[g.fields[j]] {
				g.fields[*c], g.fields[j] = g.fields[j], g.fields[*c]
				*c = j
				g = g.recompute()
			}
		}
	}
	return g, nil
}

// reorderChosen keeps chosen fields first, preserving relative order.
func (g GroupConfigModal) reorderChosen() GroupConfigModal {
	cur := ""
	if g.cursor[0] < len(g.fields) {
		cur = g.fields[g.cursor[0]]
	}
	var a, b []string
	for _, f := range g.fields {
		if g.chosen[f] {
			a = append(a, f)
		} else {
			b = append(b, f)
		}
	}
	g.fields = append(a, b...)
	for i, f := range g.fields {
		if f == cur {
			g.cursor[0] = i
		}
	}
	return g
}

// View renders the modal body.
func (g GroupConfigModal) View() string {
	w := g.Width
	if w <= 0 {
		w = 40
	}
	var lines []string
	head := func(i int, s string) string {
		if g.section == i {
			return "[" + s + "]"
		}
		return " " + s + " "
	}
	lines = append(lines, head(0, "Fields")+"  order: "+strings.Join(g.ChosenFields(), " > "))
	order := map[string]int{}
	for i, f := range g.ChosenFields() {
		order[f] = i + 1
	}
	for i, f := range g.fields {
		cur := " "
		if g.section == 0 && i == g.cursor[0] {
			cur = ">"
		}
		box := "[ ]"
		if g.chosen[f] {
			box = "[" + string(rune('0'+order[f]%10)) + "]"
		}
		lines = append(lines, frame.Truncate(cur+" "+box+" "+f, w))
	}
	lines = append(lines, "", head(1, "Groups")+"  (space toggle · a all)")
	if len(g.groups) == 0 {
		lines = append(lines, "  no grouping fields chosen")
	}
	for i, k := range g.groups {
		cur := " "
		if g.section == 1 && i == g.cursor[1] {
			cur = ">"
		}
		box := "[x]"
		if g.excluded[k] {
			box = "[ ]"
		}
		lines = append(lines, frame.Truncate(cur+" "+box+" "+k, w))
	}
	lines = append(lines, "", frame.Truncate("tab section · J/K reorder · enter apply · esc cancel", w))
	return strings.Join(lines, "\n")
}
