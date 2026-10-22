package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/gmtm/internal/capture"
	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/store"
	"github.com/unixsuperhero/gmtm/internal/tuios/tuiostest"
)

func contextCaptureRepo(t *testing.T, h *harness) *capture.Repository {
	t.Helper()
	repo, err := capture.Open(filepath.Join(t.TempDir(), "capture.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	h.a.d.Capture = repo
	return repo
}

func saveContextTurn(t *testing.T, repo *capture.Repository, w domain.Window, prompt, command, response string) {
	t.Helper()
	if err := repo.Upsert(context.Background(), capture.Turn{
		ID: w.Target.Key(), Server: w.Target.Server, Host: w.Target.Host,
		Session: w.Target.Session, Window: w.Target.WindowID, Kind: "terminal",
		Prompt: prompt, RawCommand: command, Response: response,
		StartedAt: 1000, CompletedAt: 2000, State: "done", OutputStatus: "captured",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestContextModalAutomaticallyExtractsAndPreviews(t *testing.T) {
	h := newHarness(t, [3]string{"work", "w1", "terminal"})
	repo := contextCaptureRepo(t, h)
	cwd := t.TempDir()
	for name, body := range map[string]string{"guide.md": "# Captured guide", "main.go": "package example"} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(cwd, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	h.a.snap.Windows[0].CWD = cwd
	h.a.refreshPages()
	w := h.a.snap.Windows[0]
	saveContextTurn(t, repo, w, "Read ./guide.md", "cat ./main.go", "See https://example.test/reference and ./assets/")
	h.d.Handle("capture-pane", tuiostest.Result(map[string]any{"content": "https://example.test/reference ./guide.md"}))
	h.d.Handle("file-list", tuiostest.Result(map[string]any{"entries": []any{map[string]any{"name": "logo.png"}}}))

	for opening := range 2 {
		msg := h.a.exec("context", "")().(twopaneMsg)
		h.a.onTwopane(msg)
		items, err := h.st.AllContextItems(context.Background())
		if err != nil || len(items) != 4 || len(h.a.data.Items) != 4 {
			t.Fatalf("opening %d: saved=%+v displayed=%+v err=%v", opening, items, h.a.data.Items, err)
		}
		kinds := map[string]bool{}
		for _, it := range items {
			kinds[it.Kind] = true
			if it.Source == nil || it.Source.Key() != w.Target.Key() || it.Project != w.Project {
				t.Fatalf("lost source attribution: %+v", it)
			}
		}
		for _, kind := range []string{"url", "file", "doc", "dir"} {
			if !kinds[kind] {
				t.Errorf("missing %s in %+v", kind, items)
			}
		}
		for _, item := range msg.src.Root().Items {
			switch item.Kind {
			case "doc", "file", "url":
				text, err := msg.src.Preview(context.Background(), item)
				want := map[string]string{"doc": "# Captured guide", "file": "package example", "url": "https://example.test/reference"}[item.Kind]
				if err != nil || !strings.Contains(text, want) {
					t.Errorf("%s preview %q, %v", item.Kind, text, err)
				}
			case "dir":
				if _, ok := item.Data.(filesRef); !ok || !item.Nested {
					t.Fatalf("directory is not browseable: %+v", item)
				}
				level, err := msg.src.Children(context.Background(), item)
				if err != nil || len(level.Items) != 1 || level.Items[0].Title != "logo.png" {
					t.Fatalf("directory listing %+v, %v", level, err)
				}
			}
		}
	}
}

func TestContextPageScopesExtractionAndRetainsClosedSources(t *testing.T) {
	h := newHarness(t)
	repo := contextCaptureRepo(t, h)
	h.a.setActive("work")
	windows := []domain.Window{
		{Target: domain.Target{Server: "clean-mtm", Host: "local", Session: "work", WindowID: "local"}, Project: "work"},
		{Target: domain.Target{Server: "clean-mtm", Host: "remote", Session: "work", WindowID: "remote"}, Project: "work"},
		{Target: domain.Target{Server: "other-server", Host: "local", Session: "work", WindowID: "foreign"}, Project: "work"},
		{Target: domain.Target{Server: "clean-mtm", Host: "local", Session: "other", WindowID: "other"}, Project: "other"},
	}
	for _, w := range windows {
		saveContextTurn(t, repo, w, "", "", "https://example.test/"+w.Target.WindowID)
	}
	h.a.snap.Windows = windows
	h.a.refreshPages()
	h.d.Handle("capture-pane", tuiostest.Result(map[string]any{"content": "https://example.test/screen"}))
	before := len(h.requests("capture-pane"))
	msg := h.a.exec("page.context", "")().(contextLoadedMsg)
	h.a.update(msg)
	if len(msg.items) != 4 || len(h.a.page().lst.Items()) != 4 {
		t.Fatalf("saved/page context not refreshed: %+v", msg)
	}
	calls := h.requests("capture-pane")[before:]
	if len(calls) != 1 || !strings.Contains(calls[0], `"window":"local"`) || !strings.Contains(calls[0], `"lines":200`) {
		t.Fatalf("capture escaped active local target or was unbounded: %v", calls)
	}
	for _, it := range msg.items {
		if it.Project != "work" || strings.HasSuffix(it.Path, "/other") || it.Source == nil {
			t.Fatalf("incorrect context association: %+v", it)
		}
	}
	h.a.snap.Windows = nil
	h.a.refreshPages()
	closed := h.a.exec("page.context", "")().(contextLoadedMsg)
	h.a.update(closed)
	if len(h.a.page().lst.Items()) != 4 {
		t.Fatalf("closed-pane context disappeared: %+v", closed)
	}
	modal := h.a.exec("context", "")().(twopaneMsg)
	if modal.src.win != nil || len(modal.src.items) != 4 {
		t.Fatalf("project context without selection: %+v", modal.src)
	}
}

func TestContextPreviewRemoteSafetyAndPageOpen(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(path, []byte("LOCAL SECRET CONTENT"), 0600); err != nil {
		t.Fatal(err)
	}
	remote := domain.Target{Server: "clean-mtm", Host: "remote", Session: "work", WindowID: "remote"}
	src := &ctxSource{client: h.a.d.Client}
	for _, kind := range []string{"doc", "file", "dir"} {
		it := domain.ContextItem{Kind: kind, Path: path, Source: &remote}
		item := savedContextItem(it)
		text, err := src.Preview(context.Background(), item)
		if err != nil || strings.Contains(text, "LOCAL SECRET CONTENT") || !strings.Contains(text, "remote") || item.Nested {
			t.Fatalf("unsafe remote %s preview: %q, %+v, %v", kind, text, item, err)
		}
	}
	local := remote
	local.Host = "local"
	it := domain.ContextItem{ID: 1, Kind: "doc", Path: path, Source: &local, Title: "Guide", Body: "manual annotation"}
	h.a.data.Items = []domain.ContextItem{it}
	h.a.refreshPages()
	h.a.setPage(4)
	msg := h.a.exec("context.open", "")().(contentMsg)
	if !strings.Contains(msg.text, "LOCAL SECRET CONTENT") || !strings.Contains(msg.text, "manual annotation") || !strings.Contains(msg.text, local.Key()) {
		t.Fatalf("page open lost content or metadata: %+v", msg)
	}
	if _, err := readHead(filepath.Dir(path)); err == nil {
		t.Fatal("non-regular file accepted as preview")
	}
	h.d.Handle("file-list", tuiostest.Result(map[string]any{"entries": []any{map[string]any{"name": "secret.md"}}}))
	dir := domain.ContextItem{Kind: "dir", Path: filepath.Dir(path), Source: &local}
	opened := h.a.openSavedContext(dir)().(twopaneMsg)
	if root := opened.src.Root(); len(root.Items) != 1 || root.Items[0].Title != "secret.md" || !strings.Contains(root.Origin, local.Key()) {
		t.Fatalf("page directory did not browse attributed contents: %+v", root)
	}
}

type failingContextWrites struct{ store.Store }

func (s failingContextWrites) Begin(context.Context) (store.Tx, error) {
	return nil, errors.New("context write unavailable")
}

func TestContextExtractionFailurePreservesSavedNotes(t *testing.T) {
	h := newHarness(t, [3]string{"work", "w1", "terminal"})
	contextCaptureRepo(t, h)
	note := domain.ContextItem{Kind: "note", Title: "Keep", Body: "manual note", Project: h.a.snap.Windows[0].Project}
	tx, err := h.st.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.SaveContextItem(&note); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	h.a.d.Store = failingContextWrites{h.st}
	h.d.Handle("capture-pane", tuiostest.Result(map[string]any{"content": "https://example.test/new"}))
	msg := h.a.exec("context", "")().(twopaneMsg)
	h.a.onTwopane(msg)
	if len(msg.src.items) != 1 || msg.src.items[0].Body != "manual note" || !strings.Contains(msg.src.ctxNote, "context write unavailable") || !strings.Contains(h.a.status, "context write unavailable") {
		t.Fatalf("saved notes or explicit failure lost: %+v, %q", msg.src, h.a.status)
	}
}
