package jsonexplorer

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

const sample = `{"name":"mtm","n":3,"ok":true,"none":null,"tags":["a","b"],"sub":{"deep":{"k":"v"}}}`

func key(m *Model, s string) {
	var k tea.KeyMsg
	switch s {
	case "enter":
		k = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		k = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		k = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	m.Update(k)
}

func TestBuildTree(t *testing.T) {
	root, err := Build([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if root.TypeLabel() != "object{6}" || root.Children[4].TypeLabel() != "array[2]" {
		t.Fatalf("labels %s %s", root.TypeLabel(), root.Children[4].TypeLabel())
	}
	kinds := []Kind{KString, KNumber, KBool, KNull, KArray, KObject}
	for i, k := range kinds {
		if root.Children[i].Kind != k {
			t.Errorf("child %d kind %s want %s", i, root.Children[i].Kind, k)
		}
	}
	if p := root.Children[5].Children[0].Children[0].Path; p != "$.sub.deep.k" {
		t.Errorf("path %s", p)
	}
	if p := root.Children[4].Children[1].Path; p != "$.tags[1]" {
		t.Errorf("path %s", p)
	}
	if len(Flatten(root)) != 11 {
		t.Errorf("flatten %d", len(Flatten(root)))
	}
}

func TestFold(t *testing.T) {
	m, _ := New([]byte(sample), "t")
	if len(m.Visible()) != 7 {
		t.Fatalf("initial visible %d", len(m.Visible()))
	}
	key(m, "j")
	for m.Selected().Key != "tags" {
		key(m, "j")
	}
	key(m, "l")
	if len(m.Visible()) != 9 {
		t.Fatalf("after unfold %d", len(m.Visible()))
	}
	key(m, "j")
	key(m, "h") // leaf: go to parent
	if m.Selected().Key != "tags" {
		t.Fatalf("h on leaf selected %s", m.Selected().Key)
	}
	key(m, "h") // fold
	if len(m.Visible()) != 7 {
		t.Fatalf("after fold %d", len(m.Visible()))
	}
}

func TestSearchCounts(t *testing.T) {
	m, _ := New([]byte(sample), "t")
	key(m, "/")
	if m.Mode() != ModeInsert {
		t.Fatal("expected insert mode")
	}
	for _, r := range "deep" {
		key(m, string(r))
	}
	key(m, "enter")
	if m.Mode() != ModeNormal {
		t.Fatal("expected normal mode")
	}
	matched, total := m.MatchCount()
	if matched < 1 || total != 11 {
		t.Fatalf("matched %d total %d", matched, total)
	}
	if m.Selected().Key != "deep" {
		t.Fatalf("selected %s (folded ancestors should reveal)", m.Selected().Key)
	}
	key(m, "n")
	key(m, "N")
	if !strings.Contains(m.View(), "matched") {
		t.Error("footer lacks counts")
	}
}

func TestTypeFilter(t *testing.T) {
	m, _ := New([]byte(sample), "t")
	key(m, "t") // string
	if m.Filter() != KString {
		t.Fatal(m.Filter())
	}
	got := []string{}
	for _, n := range m.Visible() {
		got = append(got, n.Key)
	}
	want := "$ name tags [0] [1] sub deep k"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %v", got)
	}
	key(m, "esc")
	if m.Filter() != "" {
		t.Fatal("esc clears filter")
	}
}

func TestFrameIndicators(t *testing.T) {
	m, _ := New([]byte(sample), "t")
	m.Update(tea.WindowSizeMsg{Width: 70, Height: 24})
	v := m.View()
	for _, s := range []string{"cmtm", "mode: normal", "project: no-project", "path: $"} {
		if !strings.Contains(v, s) {
			t.Errorf("missing %q", s)
		}
	}
	key(m, "/")
	if !strings.Contains(m.View(), "mode: insert") {
		t.Error("insert not shown")
	}
	m.Update(tea.WindowSizeMsg{Width: 24, Height: 5})
	if !strings.Contains(m.View(), "too small") || !strings.Contains(m.View(), "project: no-project") {
		t.Error("too-small state lost indicators")
	}
}

func TestInvalidJSON(t *testing.T) {
	_, err := Build([]byte("{\n  \"a\": 1,\n  \"b\" 2\n}"))
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("err %v", err)
	}
	if pe.Line != 3 {
		t.Errorf("line %d col %d", pe.Line, pe.Col)
	}
	if _, err := Build([]byte(`{"a":`)); err == nil {
		t.Error("truncated should fail")
	}
	if _, err := Build([]byte(`{} x`)); err == nil {
		t.Error("trailing should fail")
	}
	if _, err := Build([]byte("  ")); err == nil {
		t.Error("empty should fail")
	}
}
