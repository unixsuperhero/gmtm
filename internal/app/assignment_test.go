package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/store"
	"github.com/unixsuperhero/gmtm/internal/tuios/tuiostest"
)

func assignmentWindow(t *testing.T, h *harness) domain.Window {
	t.Helper()
	if len(h.a.snap.Windows) != 1 {
		t.Fatalf("expected one inventory window, got %+v", h.a.snap.Windows)
	}
	return h.a.snap.Windows[0]
}

func assertAssignment(t *testing.T, h *harness, target domain.Target, project string) {
	t.Helper()
	w := assignmentWindow(t, h)
	if !reflect.DeepEqual(w.Target, target) || w.Project != project {
		t.Fatalf("assignment changed target or lost ownership: window=%+v target=%+v project=%q", w, target, project)
	}
	overrides, err := h.st.PaneProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, exists := overrides[target.Key()]; !exists || got != project {
		t.Fatalf("persisted override = %q, exists=%v; want %q", got, exists, project)
	}
}

func assertAssignmentReadOnly(t *testing.T, h *harness) {
	t.Helper()
	for _, request := range h.d.Requests() {
		switch request.Verb {
		case "hello", "list-sessions", "list-windows", "list-agents", "list-hosts", "get-window", "capture-pane":
		default:
			t.Errorf("pane assignment sent daemon mutation/unexpected request: %s %s", request.Verb, request.Params)
		}
	}
	if events := h.log.all(); len(events) != 0 {
		t.Errorf("pane assignment opened a stream or sent input: %v", events)
	}
}

func openAssignment(t *testing.T, h *harness) {
	t.Helper()
	h.keys("A")
	top, _ := h.a.modals.Top()
	f, ok := formOf(top)
	if !ok || top.ID != "form:pane-project" || f.ID != "pane-project" {
		t.Fatalf("A did not open pane project form: %+v (status %q)", top, h.a.status)
	}
	if f.Text("project") != "" || !f.Insert() {
		t.Fatalf("assignment must start with a blank editable project field: %q", f.Text("project"))
	}
}

func TestPaneAssignmentExistingAndNewAcrossKinds(t *testing.T) {
	for _, kind := range []domain.Kind{domain.KindTerminal, domain.KindVim, domain.KindAgent} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%v", kind, existing), func(t *testing.T) {
				h := newHarness(t, [3]string{domain.SessionNoProject, "w1", "pane"})
				defer assertAssignmentReadOnly(t, h)
				if kind != domain.KindTerminal {
					attrs := map[string]any{"session": domain.SessionNoProject, "window_id": "w1", "state": "none"}
					if kind == domain.KindVim {
						attrs["foreground"] = "nvim"
					} else {
						attrs["harness_id"] = "claude"
						attrs["state"] = "working"
					}
					h.d.Handle("list-agents", tuiostest.Result(map[string]any{"agents": []any{attrs}}))
					h.run(h.a.refreshCmd())
				}
				before := assignmentWindow(t, h)
				if before.Kind != kind || before.Project != "" {
					t.Fatalf("invalid unowned %s fixture: %+v", kind, before)
				}
				var projectID int64
				if existing {
					h.keys("P", "backend", "ctrl+s")
					p, err := h.st.ProjectByName(context.Background(), "backend")
					if err != nil || p == nil {
						t.Fatalf("seed project: %+v %v", p, err)
					}
					projectID = p.ID
				}
				h.a.setActive("")
				sessions := append([]domain.Session(nil), h.a.snap.Sessions...)
				openAssignment(t, h)
				if existing {
					h.keys("bkd")
					h.send(tea.KeyMsg{Type: tea.KeyDown})
					h.keys("enter")
				} else {
					h.keys("backend")
					if p, err := h.st.ProjectByName(context.Background(), "backend"); err != nil || p != nil {
						t.Fatalf("typing created project before submit: %+v %v", p, err)
					}
				}
				h.keys("ctrl+s")
				assertAssignment(t, h, before.Target, "backend")
				if !h.a.modals.Empty() || h.a.active != "" || !h.a.activeNone {
					t.Fatalf("assignment changed active project or left modal open: active=%q status=%q", h.a.active, h.a.status)
				}
				if !reflect.DeepEqual(sessions, h.a.snap.Sessions) || assignmentWindow(t, h).Kind != kind {
					t.Fatal("assignment changed session inventory or pane kind")
				}
				projects, err := h.st.Projects(context.Background())
				if err != nil || len(projects) != 1 || projects[0].Name != "backend" || (existing && projects[0].ID != projectID) {
					t.Fatalf("project was not created/reused exactly once: %+v %v", projects, err)
				}
				if len(h.a.panes().lst.Items()) != 0 || len(h.a.contextWindows()) != 0 {
					t.Fatal("assigned pane still belongs to no-project scope")
				}
				for _, item := range projectRows(h.a.snap, h.a.data) {
					r := item.(row)
					want := "windows:0"
					if r.project == "backend" {
						want = "windows:1"
					}
					if !strings.Contains(r.label, want) {
						t.Errorf("incorrect project count: %s; want %s", r.label, want)
					}
				}
				h.a.setActive("backend")
				if items := h.a.panes().lst.Items(); len(items) != 1 || items[0].(row).fields["project"] != "backend" || len(h.a.contextWindows()) != 1 {
					t.Fatalf("assignment not reflected in project filters/context: %+v", items)
				}
			})
		}
	}
}

func TestPaneAssignmentCancelKeepsPriorOwnership(t *testing.T) {
	h := newHarness(t, [3]string{"original", "w1", "pane"})
	defer assertAssignmentReadOnly(t, h)
	before := assignmentWindow(t, h)
	openAssignment(t, h)
	h.keys("previous", "ctrl+s")
	assertAssignment(t, h, before.Target, "previous")
	openAssignment(t, h)
	h.keys("discarded-project", "esc", "esc", "y")
	assertAssignment(t, h, before.Target, "previous")
	if p, err := h.st.ProjectByName(context.Background(), "discarded-project"); err != nil || p != nil {
		t.Fatalf("cancellation created a project: %+v %v", p, err)
	}
	if !h.a.modals.Empty() || h.a.active != "" {
		t.Fatalf("cancellation left modal or changed active project: %q", h.a.status)
	}
}

type assignmentFailStore struct{ store.Store }

type assignmentFailTx struct{ store.Tx }

func (s assignmentFailStore) Begin(ctx context.Context) (store.Tx, error) {
	tx, err := s.Store.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return assignmentFailTx{tx}, nil
}

func (assignmentFailTx) SetPaneProject(string, string) error {
	return errors.New("pane assignment write unavailable")
}

func TestPaneAssignmentWriteFailureRollsBackProjectAndKeepsDraft(t *testing.T) {
	h := newHarness(t, [3]string{"original", "w1", "pane"})
	defer assertAssignmentReadOnly(t, h)
	before := assignmentWindow(t, h)
	openAssignment(t, h)
	h.keys("previous", "ctrl+s")
	h.a.d.Store = assignmentFailStore{h.st}
	openAssignment(t, h)
	h.keys("unsaved-project", "ctrl+s")
	assertAssignment(t, h, before.Target, "previous")
	top, _ := h.a.modals.Top()
	f, ok := formOf(top)
	if !ok || f.ID != "pane-project" || f.Text("project") != "unsaved-project" || h.a.active != "" {
		t.Fatalf("write failure lost draft or changed active project: %q", h.a.status)
	}
	if !strings.Contains(h.a.status, "pane assignment write unavailable") {
		t.Fatalf("write failure not reported: %q", h.a.status)
	}
	if p, err := h.st.ProjectByName(context.Background(), "unsaved-project"); err != nil || p != nil {
		t.Fatalf("failed assignment left a partially created project: %+v %v", p, err)
	}
	h.a.d.Store = h.st
	h.keys("ctrl+s")
	assertAssignment(t, h, before.Target, "unsaved-project")
	if !h.a.modals.Empty() {
		t.Fatal("retrying preserved draft did not finish assignment")
	}
}

func TestPaneAssignmentSurvivesReloadAndExplicitNoProject(t *testing.T) {
	for _, project := range []string{"assigned", ""} {
		t.Run(orNone(project), func(t *testing.T) {
			h := newHarness(t, [3]string{"session-project", "w1", "pane"})
			defer assertAssignmentReadOnly(t, h)
			before := assignmentWindow(t, h)
			if before.Project != "session-project" {
				t.Fatalf("expected session-derived project: %+v", before)
			}
			openAssignment(t, h)
			h.keys(orNone(project), "ctrl+s")
			assertAssignment(t, h, before.Target, project)
			h.run(h.a.refreshCmd())
			assertAssignment(t, h, before.Target, project)
			h.run(h.a.loadStore())
			assertAssignment(t, h, before.Target, project)
			h.a = New(h.a.d)
			h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
			h.run(h.a.Init())
			assertAssignment(t, h, before.Target, project)
			h.a.setActive(project)
			if len(h.a.panes().lst.Items()) != 1 || len(h.a.contextWindows()) != 1 {
				t.Fatal("fresh app did not use persisted ownership for project consumers")
			}
			h.a.setActive("session-project")
			if len(h.a.panes().lst.Items()) != 0 || len(h.a.contextWindows()) != 0 {
				t.Fatal("session-derived ownership overrode explicit pane metadata")
			}
		})
	}
}
