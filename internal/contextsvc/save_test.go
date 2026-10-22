package contextsvc

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/store"
)

func TestSavePreservesAttributionAndEditedAssociation(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "context.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w := domain.Window{Target: domain.Target{Server: "clean-mtm", Host: "local", Session: "web", WindowID: "w1"}, Project: "web"}
	refs := []Reference{{Kind: "url", Path: "https://example.com/docs"}, {Kind: "doc", Path: "/src/README.md"}}
	if err := Save(ctx, st, w, append(refs, refs[0])); err != nil {
		t.Fatal(err)
	}
	items, err := st.AllContextItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("duplicate references saved: %+v", items)
	}
	for _, it := range items {
		if it.Source == nil || *it.Source != w.Target || it.Project != "web" {
			t.Fatalf("lost attribution: %+v", it)
		}
	}
	edited := items[0]
	edited.Project, edited.Title, edited.Body = "curated", "User title", "User note"
	tx, err := st.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.SaveContextItem(&edited); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := Save(ctx, st, w, refs); err != nil {
		t.Fatal(err)
	}
	got, err := st.ContextItem(ctx, edited.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != "curated" || got.Title != "User title" || got.Body != "User note" {
		t.Fatalf("extraction overwrote curated context: %+v", got)
	}
	w.Target.WindowID = "w2"
	if err := Save(ctx, st, w, refs[:1]); err != nil {
		t.Fatal(err)
	}
	items, err = st.AllContextItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("lost independent pane attribution: %+v", items)
	}
	second, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	persisted, err := second.ContextItem(ctx, edited.ID)
	if err != nil || persisted == nil || persisted.Project != "curated" {
		t.Fatalf("context did not persist: %+v, %v", persisted, err)
	}
}
