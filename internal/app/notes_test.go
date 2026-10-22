package app

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/actions"
	"github.com/unixsuperhero/gmtm/internal/domain"
)

func TestSavedNotesProjectScopeAndDiscovery(t *testing.T) {
	h := newHarness(t)
	tx, err := h.st.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, item := range []domain.ContextItem{
		{Kind: "note", Body: "Archived project note\nRetain the deployment checklist", Project: "archive"},
		{Kind: "note", Body: "Other project note", Project: "other"},
		{Kind: "note", Body: "Unassigned note"},
		{Kind: "doc", Title: "Not a note", Path: "/README.md", Project: "archive"},
	} {
		if err := tx.SaveContextItem(&item); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	h.keys("7")
	if h.a.page().id != actions.PageNotes || len(h.a.page().lst.Items()) != 3 {
		t.Fatalf("unscoped notes did not include all notes exclusively: %+v", h.a.page().lst.Items())
	}
	// No live session or project record exists: the saved note must make its
	// project discoverable without creating a replacement project on selection.
	h.keys("P", "archive", "ctrl+s")
	if projects, err := h.st.Projects(context.Background()); err != nil || len(projects) != 0 {
		t.Fatalf("note-only project was not recognized: %+v %v", projects, err)
	}
	items := h.a.page().lst.Items()
	if len(items) != 1 || items[0].(row).item.Project != "archive" {
		t.Fatalf("project scope leaked other notes or documents: %+v", items)
	}
	h.keys("enter")
	if h.a.content == nil || !strings.Contains(h.a.content.text, "Retain the deployment checklist") || !strings.Contains(h.a.content.text, "project archive") {
		t.Fatalf("saved note body or project missing: %+v", h.a.content)
	}
	h.keys("esc", "P", "no-project", "ctrl+s")
	items = h.a.page().lst.Items()
	if len(items) != 1 || items[0].(row).item.Body != "Unassigned note" {
		t.Fatalf("no-project scope leaked assigned notes: %+v", items)
	}
	h.keys("P", "empty", "ctrl+s")
	if len(h.a.page().lst.Items()) != 0 || !strings.Contains(h.a.View(), "No saved notes for empty") {
		t.Fatal("empty project retained an out-of-scope note or lacked guidance")
	}
}

func TestSavedNotesCreateReloadAndReassociate(t *testing.T) {
	h := newHarness(t)
	h.keys("P", "work", "ctrl+s", "7", "f4", "Release checklist", "enter", "Check migrations", "ctrl+s")
	items := h.a.page().lst.Items()
	if len(items) != 1 || items[0].(row).item.Project != "work" || items[0].(row).item.Body != "Release checklist\nCheck migrations" {
		t.Fatalf("saved note did not appear in its project: %+v", items)
	}
	id := items[0].(row).item.ID
	deps := h.a.d
	deps.Project = "work"
	h.a = New(deps)
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	h.run(h.a.Init())
	h.keys("7", "enter")
	if h.a.content == nil || !strings.Contains(h.a.content.text, "Check migrations") {
		t.Fatal("saved note unavailable after app restart")
	}
	h.keys("esc", "e", "ctrl+a")
	h.send(tea.KeyMsg{Type: tea.KeyCtrlK})
	h.keys("archive", "ctrl+s")
	if len(h.a.page().lst.Items()) != 0 {
		t.Fatal("reassociated note remained in old project")
	}
	h.keys("P", "archive", "ctrl+s")
	items = h.a.page().lst.Items()
	if len(items) != 1 || items[0].(row).item.ID != id || items[0].(row).item.Body != "Release checklist\nCheck migrations" {
		t.Fatal("reassociation lost the saved note or its contents")
	}
	// Notes refresh reads SQLite rather than requiring a pane or daemon capture.
	tx, err := h.st.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := tx.SetContextProject(id, "work"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	h.send(tea.KeyMsg{Type: tea.KeyCtrlR})
	if len(h.a.page().lst.Items()) != 0 {
		t.Fatal("notes refresh did not reload project associations")
	}
}
