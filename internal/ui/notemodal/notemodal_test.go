package notemodal

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func k(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func typ(m *Model, s string) {
	for _, r := range s {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func msgOf(c tea.Cmd) tea.Msg {
	if c == nil {
		return nil
	}
	return c()
}

// fakeCSI mimics Bubble Tea v1's unexported unknownCSISequenceMsg String().
type fakeCSI []byte

func (u fakeCSI) String() string { return fmt.Sprintf("?CSI%+v?", []byte(u)[2:]) }

func TestEnterInsertsNewline(t *testing.T) {
	m := New("website", nil)
	typ(m, "a")
	if got := msgOf(m.Update(k(tea.KeyEnter))); got != nil {
		if _, ok := got.(SaveMsg); ok {
			t.Fatal("enter saved")
		}
	}
	typ(m, "b")
	if m.Text() != "a\nb" {
		t.Fatalf("text %q", m.Text())
	}
}

func TestCtrlEnterSaves(t *testing.T) {
	m := New("website", nil)
	if !strings.Contains(m.View(), "not distinguishable") || !strings.Contains(m.View(), "ctrl+s") {
		t.Fatal("limitation/alternative not shown")
	}
	typ(m, "hello")
	got := msgOf(m.Update(fakeCSI("\x1b[27;5;13~")))
	s, ok := got.(SaveMsg)
	if !ok || s.Text != "hello" || s.Project != "website" || s.NoProject {
		t.Fatalf("got %#v", got)
	}
	if !m.CtrlEnterSupported() || strings.Contains(m.View(), "not distinguishable") {
		t.Fatal("detection not recorded")
	}
	if !IsCtrlEnter(fakeCSI("\x1b[13;5u")) || IsCtrlEnter(k(tea.KeyEnter)) || IsCtrlEnter(fakeCSI("\x1b[1;5A")) {
		t.Fatal("IsCtrlEnter classification")
	}
	m2 := New("website", nil)
	typ(m2, "x")
	if s, ok := msgOf(m2.Update(k(tea.KeyCtrlS))).(SaveMsg); !ok || s.Text != "x" {
		t.Fatal("ctrl+s alternative")
	}
}

func TestEscapeLadder(t *testing.T) {
	m := New("website", nil)
	if c := m.Update(k(tea.KeyEsc)); c != nil || m.Insert() {
		t.Fatal("first esc should enter normal mode")
	}
	if c, ok := msgOf(m.Update(k(tea.KeyEsc))).(CancelMsg); !ok || c.Dirty {
		t.Fatalf("clean cancel: %#v", c)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	typ(m, "draft")
	m.Update(k(tea.KeyEsc))
	if c, ok := msgOf(m.Update(k(tea.KeyEsc))).(CancelMsg); !ok || !c.Dirty {
		t.Fatalf("dirty cancel: %#v", c)
	}
}

func TestErrorRetainsDraftAndUnknownNeedsChoice(t *testing.T) {
	m := New("unknown", []string{"website", "api"})
	typ(m, "note body")
	if c := m.Update(k(tea.KeyCtrlS)); c != nil {
		t.Fatal("saved with unknown destination")
	}
	if !m.Choosing() || !strings.Contains(m.Err(), "unknown") {
		t.Fatal("unknown should require explicit choice")
	}
	m.Update(k(tea.KeyEnter)) // first option: no-project
	s, ok := msgOf(m.Update(k(tea.KeyCtrlS))).(SaveMsg)
	if !ok || !s.NoProject || s.Project != "" {
		t.Fatalf("explicit no-project save: %#v", s)
	}
	m.SetError("disk full")
	if m.Text() != "note body" || m.Destination() != "no-project" || !strings.Contains(m.View(), "disk full") {
		t.Fatal("draft not retained after failure")
	}
	m.Update(k(tea.KeyCtrlT))
	typ(m, "api")
	m.Update(k(tea.KeyEnter))
	if s, ok := msgOf(m.Update(k(tea.KeyCtrlS))).(SaveMsg); !ok || s.Project != "api" {
		t.Fatalf("retry after change: %#v", s)
	}
}
