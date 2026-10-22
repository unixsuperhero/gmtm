package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/gmtm/internal/capture"
	"github.com/unixsuperhero/gmtm/internal/setup"
)

func TestJumpSearchesCapturedOutputAndOpensExactTurn(t *testing.T) {
	h := newHarness(t, [3]string{"work", "w1", "terminal"}, [3]string{"work", "w2", "other"})
	repo, err := capture.Open(filepath.Join(t.TempDir(), "capture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	h.a.d.Capture = repo
	for _, turn := range []capture.Turn{
		{ID: "one", Server: "clean-mtm", Host: "local", Session: "work", Window: "w1", Kind: "terminal", Prompt: "printf al pha", RawCommand: "printf alpha", Response: "needle-output", StartedAt: 1000, CompletedAt: 2000, State: "done", OutputStatus: "captured"},
		{ID: "two", Server: "clean-mtm", Host: "local", Session: "work", Window: "w1", Kind: "terminal", Prompt: "printf beta", Response: "different output", StartedAt: 3000, CompletedAt: 4000, State: "done", OutputStatus: "captured"},
		{ID: "foreign", Server: "clean-mtm", Host: "local", Session: "work", Window: "w2", Kind: "terminal", Prompt: "foreign command", Response: "needle-output from wrong pane", StartedAt: 5000, CompletedAt: 6000, State: "done", OutputStatus: "captured"},
	} {
		if err := repo.Upsert(context.Background(), turn); err != nil {
			t.Fatal(err)
		}
	}
	h.keys("J", "/", "needle-output", "enter", "enter")
	if h.a.content == nil || !strings.Contains(h.a.content.text, "printf alpha") || !strings.Contains(h.a.content.text, "needle-output") {
		t.Fatalf("exact captured turn not opened: %+v", h.a.content)
	}
	if strings.Contains(h.a.content.text, "foreign") || strings.Contains(h.a.content.text, "printf beta") || h.a.live != nil {
		t.Fatalf("wrong target/turn or implicit live mode: %+v", h.a.content)
	}
}

func TestSettingsSetupCancelDoesNotInstall(t *testing.T) {
	h := newHarness(t)
	manager, err := setup.New(setup.Options{Home: t.TempDir(), DB: filepath.Join(t.TempDir(), "capture.db"), Server: "clean-mtm"})
	if err != nil {
		t.Fatal(err)
	}
	h.a.d.Setup = manager
	h.keys("6", "enter")
	if top, ok := h.a.modals.Top(); !ok || top.ID != "confirm:capture-setup" {
		t.Fatal("Settings did not offer capture setup confirmation")
	}
	h.keys("n")
	items, err := manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Status == "installed" {
			t.Fatalf("cancel installed %s", item.Path)
		}
	}
}
