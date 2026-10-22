package list

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/gmtm/internal/ui/keys"
)

type it struct {
	id, label string
	f         map[string]string
}

func (i it) ID() string                { return i.id }
func (i it) Fields() map[string]string { return i.f }
func (i it) Label() string             { return i.label }
func (i it) SearchText() string        { return i.label + " " + i.id }

func data() []Item {
	return []Item{
		it{"w1", "api server", map[string]string{"kind": "agent", "project": "website", "n": "10"}},
		it{"w2", "vim notes", map[string]string{"kind": "vim", "project": "website", "n": "2"}},
		it{"w3", "shell", map[string]string{"kind": "terminal", "project": "infra", "n": "30"}},
		it{"w4", "api client", map[string]string{"kind": "agent", "project": "infra", "n": "1"}},
		it{"w5", "scratch", map[string]string{"kind": "terminal", "project": "", "n": ""}},
	}
}

func ids(items []Item) []string {
	out := []string{}
	for _, i := range items {
		out = append(out, i.ID())
	}
	return out
}

var defs = []FieldDef{
	{Name: "kind", Filterable: true, Groupable: true, Sortable: true, Values: []string{"agent", "terminal", "vim", "unknown"}},
	{Name: "project", Filterable: true, Groupable: true},
	{Name: "n", Sortable: true},
}

func TestCompute(t *testing.T) {
	cases := []struct {
		name    string
		query   string
		filters map[string]map[string]bool
		sort    SortSpec
		group   GroupConfig
		want    []string
		groups  []string
		matched int
	}{
		{name: "all", want: []string{"w1", "w2", "w3", "w4", "w5"}, matched: 5},
		{name: "filter kind", filters: map[string]map[string]bool{"kind": {"agent": true}}, want: []string{"w1", "w4"}, matched: 2},
		{name: "filter two values", filters: map[string]map[string]bool{"kind": {"agent": true, "vim": true}}, want: []string{"w1", "w2", "w4"}, matched: 3},
		{name: "search", query: "api", want: []string{"w1", "w4"}, matched: 2},
		{name: "search + filter", query: "api", filters: map[string]map[string]bool{"project": {"infra": true}}, want: []string{"w4"}, matched: 1},
		{name: "sort numeric asc empties last", sort: SortSpec{Field: "n"}, want: []string{"w4", "w2", "w1", "w3", "w5"}, matched: 5},
		{name: "sort numeric desc empties last", sort: SortSpec{Field: "n", Desc: true}, want: []string{"w3", "w1", "w2", "w4", "w5"}, matched: 5},
		{name: "group project", group: GroupConfig{Fields: []string{"project"}}, want: []string{"w3", "w4", "w1", "w2", "w5"},
			groups: []string{"infra", "website", "(none)"}, matched: 5},
		{name: "group two fields", group: GroupConfig{Fields: []string{"project", "kind"}}, want: []string{"w4", "w3", "w1", "w2", "w5"},
			groups: []string{"infra / agent", "infra / terminal", "website / agent", "website / vim", "(none) / terminal"}, matched: 5},
		{name: "scope", group: GroupConfig{Fields: []string{"project"}, Scope: []string{"website"}}, want: []string{"w1", "w2"},
			groups: []string{"website"}, matched: 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Compute(data(), c.query, c.filters, c.sort, c.group)
			if got := ids(r.Items); !reflect.DeepEqual(got, c.want) {
				t.Errorf("items %v want %v", got, c.want)
			}
			if c.groups != nil && !reflect.DeepEqual(r.Groups, c.groups) {
				t.Errorf("groups %v want %v", r.Groups, c.groups)
			}
			if r.Matched != c.matched || r.Total != 5 {
				t.Errorf("counts %d/%d", r.Matched, r.Total)
			}
		})
	}
}

func TestSelectionRetainedAcrossRefresh(t *testing.T) {
	m := New(defs).SetSize(40, 10).SetItems(data())
	m = m.Select("w3")
	// reorder and add items
	d := data()
	d = append([]Item{it{"w0", "new", map[string]string{}}}, d[2], d[0], d[1])
	m = m.SetItems(d)
	if m.SelectedID() != "w3" {
		t.Fatalf("selected %q", m.SelectedID())
	}
	// selected item removed: clamp to nearby index
	m = m.SetItems(d[:1])
	if m.SelectedID() != "w0" {
		t.Fatalf("selected after removal %q", m.SelectedID())
	}
	// filter hides then reveals: selection id restored if returning
	m = m.SetItems(data()).Select("w2").SetFilter("kind", "agent")
	if m.SelectedID() == "w2" {
		t.Fatal("hidden item still selected")
	}
}

func TestMotions(t *testing.T) {
	m := New(defs).SetSize(40, 6).SetItems(data()).
		SetGroupConfigs([]GroupConfig{{Name: "none"}, {Name: "proj", Fields: []string{"project"}}})
	steps := []struct {
		nav  keys.Nav
		want string
	}{
		{keys.Bottom, "w5"}, {keys.Top, "w1"}, {keys.Down, "w2"}, {keys.Up, "w1"}, {keys.PageDown, "w5"}, {keys.HalfUp, "w3"},
	}
	for i, s := range steps {
		m = m.Move(s.nav)
		if m.SelectedID() != s.want {
			t.Fatalf("step %d %v: %s want %s", i, s.nav, m.SelectedID(), s.want)
		}
	}
	m = m.CycleGroup().Move(keys.Top) // infra: w3 w4 | website: w1 w2 | none: w5
	sec := []struct {
		nav  keys.Nav
		want string
	}{{keys.NextSection, "w1"}, {keys.NextSection, "w5"}, {keys.PrevSection, "w1"}, {keys.Down, "w2"}, {keys.PrevSection, "w1"}, {keys.PrevSection, "w3"}}
	for i, s := range sec {
		m = m.Move(s.nav)
		if m.SelectedID() != s.want {
			t.Fatalf("section step %d: %s want %s", i, m.SelectedID(), s.want)
		}
	}
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestKeysSearchAndGrouping(t *testing.T) {
	m := New(defs).SetSize(60, 10).SetItems(data()).
		SetGroupConfigs([]GroupConfig{{Name: "none"}, {Name: "proj", Fields: []string{"project"}}})
	m, _ = m.Update(runes("/"))
	if !m.Searching() {
		t.Fatal("not searching")
	}
	for _, r := range "api" {
		m, _ = m.Update(runes(string(r)))
	}
	m, _ = m.Update(runes("j")) // motion letter is text while searching
	if m.Query() != "apij" {
		t.Fatalf("query %q", m.Query())
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.Searching() || m.Query() != "api" || m.Result().Matched != 2 {
		t.Fatalf("after esc: searching=%v q=%q matched=%d", m.Searching(), m.Query(), m.Result().Matched)
	}
	if !strings.Contains(m.StatusText(), "2/5") || !strings.Contains(m.StatusText(), "C clear") {
		t.Fatalf("status %q", m.StatusText())
	}
	m, _ = m.Update(runes("z"))
	if m.GroupConfig().Name != "proj" {
		t.Fatal("z did not cycle")
	}
	_, cmd := m.Update(runes("Z"))
	if cmd == nil {
		t.Fatal("Z no cmd")
	}
	if _, ok := cmd().(CustomizeGroupsMsg); !ok {
		t.Fatal("Z wrong msg")
	}
	m, _ = m.Update(runes("C"))
	if m.HasActive() || m.Result().Matched != 5 {
		t.Fatal("clear failed")
	}
}

func TestFieldsDerived(t *testing.T) {
	d := append(data(), it{"x", "tagged", map[string]string{"tag": "backend", "kind": "custom"}})
	m := New(defs).SetItems(d)
	if got := m.FilterableFields(); !reflect.DeepEqual(got, []string{"kind", "project", "tag"}) {
		t.Errorf("filterable %v", got)
	}
	if got := m.FilterValues("kind"); !reflect.DeepEqual(got, []string{"agent", "custom", "terminal", "unknown", "vim"}) {
		t.Errorf("values %v", got)
	}
}

func TestViewKeepsSelectionVisibleAndWidth(t *testing.T) {
	var d []Item
	for i := 0; i < 50; i++ {
		d = append(d, it{string(rune('a'+i%26)) + string(rune('0'+i/26)), "界界界 very long label that will be truncated", map[string]string{}})
	}
	m := New(defs).SetSize(12, 6).SetItems(d)
	m.Detail = func(Item) string { return "detail" }
	m.Pins["a0"] = true
	m = m.Move(keys.Bottom)
	out := ansi.Strip(m.View())
	lines := strings.Split(out, "\n")
	if len(lines) != 6 {
		t.Fatalf("lines %d", len(lines))
	}
	for _, l := range lines {
		if ansi.StringWidth(l) != 12 {
			t.Fatalf("width %d: %q", ansi.StringWidth(l), l)
		}
	}
	if !strings.Contains(out, ">") || !strings.Contains(out, "detail") {
		t.Fatalf("selection not visible:\n%s", out)
	}
	if !strings.Contains(m.FooterText(), "50 of 50") || !strings.Contains(m.FooterText(), "more above") {
		t.Fatalf("footer %q", m.FooterText())
	}
	m = m.Move(keys.Top)
	if !strings.Contains(ansi.Strip(m.View()), "^") {
		t.Fatal("pin marker missing")
	}
}

func TestGroupConfigModal(t *testing.T) {
	m := New(defs).SetItems(data())
	g := NewGroupConfigModal(m)
	// fields: kind, project, n? n is not groupable; extra observed none.
	if !reflect.DeepEqual(g.fields, []string{"kind", "project"}) {
		t.Fatalf("fields %v", g.fields)
	}
	press := func(s string) {
		var k tea.KeyMsg
		switch s {
		case "space":
			k = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
		case "tab":
			k = tea.KeyMsg{Type: tea.KeyTab}
		default:
			k = runes(s)
		}
		g, _ = g.Update(k)
	}
	press("j")     // project
	press("space") // choose project
	press("j")     // kind (now second)
	press("space") // choose kind
	if got := g.ChosenFields(); !reflect.DeepEqual(got, []string{"project", "kind"}) {
		t.Fatalf("chosen %v", got)
	}
	press("K") // move kind before project
	if got := g.ChosenFields(); !reflect.DeepEqual(got, []string{"kind", "project"}) {
		t.Fatalf("reordered %v", got)
	}
	press("tab")
	press("space") // exclude first group
	cfg := g.Config()
	if len(cfg.Scope) != len(g.Groups())-1 {
		t.Fatalf("scope %v groups %v", cfg.Scope, g.Groups())
	}
	_, cmd := g.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd().(GroupConfigChosenMsg)
	m = m.ApplyGroupConfig(msg.Config)
	if m.Result().Matched != 4 {
		t.Fatalf("scoped matched %d", m.Result().Matched)
	}
	// scope preserved on refresh
	m = m.SetItems(data())
	if m.Result().Matched != 4 || len(m.GroupConfig().Scope) != len(cfg.Scope) {
		t.Fatal("scope lost on refresh")
	}
}
