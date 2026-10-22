package contextsvc

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/store"
)

// Context opens can overlap while their asynchronous commands finish. Serialize
// their read/deduplicate/write sequence so a repeated open does not add copies.
var saveMu sync.Mutex

// Save adds newly discovered references without overwriting manual context or
// changing associations the user has already edited. References stay attributed
// to their source pane even when another pane mentions the same resource.
func Save(ctx context.Context, st store.Store, w domain.Window, refs []Reference) error {
	if len(refs) == 0 {
		return nil
	}
	saveMu.Lock()
	defer saveMu.Unlock()
	items, err := st.AllContextItems(ctx)
	if err != nil {
		return err
	}
	type key struct{ kind, path string }
	seen := make(map[key]bool, len(refs))
	for _, item := range items {
		if item.Source != nil && *item.Source == w.Target {
			seen[key{item.Kind, item.Path}] = true
		}
	}
	var tx store.Tx
	for _, ref := range refs {
		k := key{ref.Kind, ref.Path}
		if ref.Path == "" || seen[k] {
			continue
		}
		if tx == nil {
			tx, err = st.Begin(ctx)
			if err != nil {
				return err
			}
			defer tx.Rollback()
		}
		title := ref.Path
		if ref.Kind != "url" {
			title = filepath.Base(ref.Path)
			if ref.Kind == "dir" && title != "/" {
				title += "/"
			}
		}
		item := domain.ContextItem{Kind: ref.Kind, Title: title, Path: ref.Path,
			Project: w.Project, Source: &w.Target, Tags: []string{"extracted"}}
		if err := tx.SaveContextItem(&item); err != nil {
			return err
		}
		seen[k] = true
	}
	if tx == nil {
		return nil
	}
	return tx.Commit()
}
