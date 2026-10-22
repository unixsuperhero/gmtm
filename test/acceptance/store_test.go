//go:build acceptance

package acceptance

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/store"
)

func TestStorePersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mtm.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := st.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.CreateProject(&domain.Project{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	src := &domain.Target{Server: "cmt-x", Host: domain.HostLocal, Session: "alpha", WindowID: "w1"}
	if err := tx.SaveContextItem(&domain.ContextItem{Kind: "note", Title: "n1", Body: "line one\nline two ☃", Project: "alpha", Source: src}); err != nil {
		t.Fatal(err)
	}
	if err := tx.SaveContextItem(&domain.ContextItem{Kind: "note", Title: "loose", Body: "x", Project: ""}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// failed transaction leaves nothing
	tx, _ = st.Begin(ctx)
	_ = tx.SaveContextItem(&domain.ContextItem{Kind: "note", Title: "partial", Body: "p", Project: "alpha"})
	if tx.CreateProject(&domain.Project{Name: "alpha"}) == nil {
		t.Fatal("duplicate project accepted")
	}
	_ = tx.Rollback()
	ver, _ := st.SchemaVersion(ctx)
	st.Close()

	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if v, _ := st.SchemaVersion(ctx); v != ver || v == 0 {
		t.Fatalf("schema version %d after reopen, was %d", v, ver)
	}
	items, err := st.ContextItems(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Body != "line one\nline two ☃" || items[0].Source == nil || items[0].Source.WindowID != "w1" {
		t.Fatalf("alpha items after restart %+v", items)
	}
	loose, _ := st.ContextItems(ctx, "")
	if len(loose) != 1 || loose[0].Title != "loose" {
		t.Fatalf("no-project items %+v", loose)
	}
	if ps, _ := st.Projects(ctx); len(ps) != 1 || ps[0].Name != "alpha" {
		t.Fatalf("projects %+v", ps)
	}
	all, _ := st.AllContextItems(ctx)
	for _, it := range all {
		if it.Title == "partial" {
			t.Fatal("rolled-back item persisted")
		}
	}
}
