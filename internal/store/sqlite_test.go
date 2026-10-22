package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/unixsuperhero/gmtm/internal/domain"
)

var bg = context.Background()

func openT(t *testing.T) (Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sub", "mtm.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, p
}

func commit(t *testing.T, s Store, f func(Tx)) {
	t.Helper()
	tx, err := s.Begin(bg)
	if err != nil {
		t.Fatal(err)
	}
	f(tx)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestProjectsCRUD(t *testing.T) {
	s, _ := openT(t)
	p := &domain.Project{Name: "alpha", Status: "active", Tags: []string{"b", "a"}}
	commit(t, s, func(tx Tx) { must(t, tx.CreateProject(p)) })
	got, err := s.ProjectByName(bg, "alpha")
	must(t, err)
	if got == nil || got.Status != "active" || !reflect.DeepEqual(got.Tags, []string{"a", "b"}) {
		t.Fatalf("got %+v", got)
	}
	p.Status, p.Tags = "done", []string{"c"}
	commit(t, s, func(tx Tx) { must(t, tx.UpdateProject(p)) })
	l, _ := s.Projects(bg)
	if len(l) != 1 || l[0].Status != "done" || !reflect.DeepEqual(l[0].Tags, []string{"c"}) {
		t.Fatalf("list %+v", l)
	}
	if v, _ := s.FieldValues(bg, "tag"); !reflect.DeepEqual(v, []string{"a", "b", "c"}) {
		t.Fatalf("tag values %v", v)
	}
	commit(t, s, func(tx Tx) { must(t, tx.DeleteProject(p.ID)) })
	if g, _ := s.ProjectByName(bg, "alpha"); g != nil {
		t.Fatal("not deleted")
	}
}

func TestContextItems(t *testing.T) {
	s, _ := openT(t)
	src := &domain.Target{Server: "clean-mtm", Host: "local", Session: "p1", WindowID: "w1"}
	c := &domain.ContextItem{Kind: "file", Title: "x", Path: "/a", Project: "p1", Source: src, Tags: []string{"t"}}
	n := &domain.ContextItem{Kind: "note", Body: "first line\nsecond"}
	commit(t, s, func(tx Tx) { must(t, tx.SaveContextItem(c)); must(t, tx.SaveContextItem(n)) })
	got, _ := s.ContextItem(bg, c.ID)
	if got.Project != "p1" || got.Source == nil || *got.Source != *src || !reflect.DeepEqual(got.Tags, []string{"t"}) {
		t.Fatalf("%+v", got)
	}
	gn, _ := s.ContextItem(bg, n.ID)
	if gn.Title != "first line" || gn.Source != nil || gn.Project != "" {
		t.Fatalf("note %+v", gn)
	}
	commit(t, s, func(tx Tx) { must(t, tx.SetContextProject(c.ID, "p2")) })
	if l, _ := s.ContextItems(bg, "p1"); len(l) != 0 {
		t.Fatal("p1 not empty")
	}
	if l, _ := s.ContextItems(bg, "p2"); len(l) != 1 {
		t.Fatal("p2 missing")
	}
	commit(t, s, func(tx Tx) { must(t, tx.SetContextProject(c.ID, "")) })
	if l, _ := s.ContextItems(bg, ""); len(l) != 2 {
		t.Fatalf("no-project %d", len(l))
	}
	got, _ = s.ContextItem(bg, c.ID)
	if got.Source == nil || *got.Source != *src {
		t.Fatal("source changed")
	}
	commit(t, s, func(tx Tx) { must(t, tx.DeleteContextItem(c.ID)) })
	if g, _ := s.ContextItem(bg, c.ID); g != nil {
		t.Fatal("not deleted")
	}
	if all, _ := s.AllContextItems(bg); len(all) != 1 {
		t.Fatal("all")
	}
}

func TestFailedTxLeavesNothing(t *testing.T) {
	s, _ := openT(t)
	tx, err := s.Begin(bg)
	must(t, err)
	must(t, tx.CreateProject(&domain.Project{Name: "dup", Tags: []string{"x"}}))
	must(t, tx.AddFieldValue("status", "new"))
	if tx.CreateProject(&domain.Project{Name: "dup"}) == nil {
		t.Fatal("expected unique violation")
	}
	must(t, tx.Rollback())
	if l, _ := s.Projects(bg); len(l) != 0 {
		t.Fatalf("partial rows %v", l)
	}
	if v, _ := s.FieldValues(bg, "status"); len(v) != 0 {
		t.Fatalf("partial field values %v", v)
	}
	if v, _ := s.FieldValues(bg, "tag"); len(v) != 0 {
		t.Fatalf("partial tag values %v", v)
	}
}

func TestStagedFieldValue(t *testing.T) {
	s, _ := openT(t)
	tx, _ := s.Begin(bg)
	must(t, tx.AddFieldValue("status", "blocked"))
	if v, _ := s.FieldValues(bg, "status"); len(v) != 0 {
		t.Fatalf("visible before commit: %v", v)
	}
	must(t, tx.Commit())
	if v, _ := s.FieldValues(bg, "status"); !reflect.DeepEqual(v, []string{"blocked"}) {
		t.Fatalf("after commit: %v", v)
	}
	commit(t, s, func(tx Tx) { must(t, tx.AddFieldValue("status", "blocked")) }) // idempotent
}

func TestPinsStars(t *testing.T) {
	s, _ := openT(t)
	commit(t, s, func(tx Tx) {
		must(t, tx.SetPinned("a", true))
		must(t, tx.SetPinned("b", true))
		must(t, tx.SetStarred("a", true))
	})
	commit(t, s, func(tx Tx) { must(t, tx.SetPinned("b", false)) })
	p, _ := s.Pins(bg)
	st, _ := s.Stars(bg)
	if !reflect.DeepEqual(p, map[string]bool{"a": true}) || !reflect.DeepEqual(st, map[string]bool{"a": true}) {
		t.Fatalf("pins %v stars %v", p, st)
	}
}

func TestSettings(t *testing.T) {
	s, _ := openT(t)
	d := map[string]string{"theme": "dark", "tick": "5"}
	commit(t, s, func(tx Tx) { must(t, tx.SetSetting("theme", "light")); must(t, tx.SetSetting("other", "z")) })
	got, _ := s.Settings(bg, d)
	want := []Setting{{"theme", "light", "dark", true}, {"tick", "5", "5", false}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	commit(t, s, func(tx Tx) { must(t, tx.ResetSetting("theme")) })
	got, _ = s.Settings(bg, d)
	if got[0].Stored || got[0].Value != "dark" {
		t.Fatalf("%+v", got[0])
	}
}

func TestMigrationPreservesData(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.db")
	old, err := openWithMigrations(p, 1)
	must(t, err)
	if v, _ := old.SchemaVersion(bg); v != 1 {
		t.Fatalf("v=%d", v)
	}
	commit(t, old, func(tx Tx) {
		must(t, tx.CreateProject(&domain.Project{Name: "keep"}))
		must(t, tx.SaveContextItem(&domain.ContextItem{Kind: "note", Body: "hello", Project: "keep"}))
	})
	must(t, old.Close())
	s, err := Open(p)
	must(t, err)
	defer s.Close()
	if v, _ := s.SchemaVersion(bg); v != len(migrations) || v < 2 {
		t.Fatalf("v=%d", v)
	}
	if g, _ := s.ProjectByName(bg, "keep"); g == nil {
		t.Fatal("project lost")
	}
	if l, _ := s.ContextItems(bg, "keep"); len(l) != 1 || l[0].Body != "hello" {
		t.Fatalf("items %v", l)
	}
	var n int
	must(t, s.(*sqlStore).db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='idx_context_items_project'`).Scan(&n))
	if n != 1 {
		t.Fatal("v2 index missing")
	}
}
