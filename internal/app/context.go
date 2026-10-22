package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/capture"
	"github.com/unixsuperhero/gmtm/internal/contextsvc"
	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/tuios"
	"github.com/unixsuperhero/gmtm/internal/ui/twopane"
)

type contextLoadedMsg struct {
	items []domain.ContextItem
	note  string
}

// contextWindows only extracts for the active project; mentioned paths never
// supply project associations. Stored items remain available without a window.
func (a *App) contextWindows() []domain.Window {
	var windows []domain.Window
	for _, w := range a.snap.Windows {
		if w.Project == a.active {
			windows = append(windows, w)
		}
	}
	return windows
}

// collectContext runs once on opening Context, with one bounded capture per
// live local target. Foreign/remote targets can contribute retained history,
// but are never sent to this process's daemon or inspected on its filesystem.
func collectContext(ctx context.Context, d Deps, windows []domain.Window, fallback []domain.ContextItem) (contextLoadedMsg, []capture.Turn) {
	result := contextLoadedMsg{items: fallback}
	var notes []string
	var outline []capture.Turn
	for _, w := range windows {
		if ctx.Err() != nil {
			notes = append(notes, "Context extraction: "+ctx.Err().Error())
			break
		}
		local := d.Client != nil && w.Target.Server == d.Client.Server() && hostOf(w.Target.Host) == domain.HostLocal
		history := d.History
		if !local || w.Closed || w.Unavailable {
			history = nil
		}
		turns, note := loadCapturedTurns(ctx, d.Capture, history, w)
		if note != "" {
			notes = append(notes, w.Target.Key()+": "+note)
		}
		outline = append(outline, turns...)
		var refs []contextsvc.Reference
		for _, turn := range turns {
			for _, text := range []string{turn.Prompt, turn.RawCommand, turn.Response} {
				refs = append(refs, contextsvc.Extract(text, w.CWD, local)...)
			}
		}
		if local && !w.Closed && !w.Unavailable {
			text, err := tuios.CapturePane(ctx, d.Client, w.Target.Session, w.Target.WindowID, "recent", 200, false)
			if err != nil {
				notes = append(notes, w.Target.Key()+": current pane capture: "+err.Error())
			} else {
				const maxPaneBytes = 128 << 10
				if len(text) > maxPaneBytes {
					text = text[len(text)-maxPaneBytes:]
				}
				refs = append(refs, contextsvc.Extract(text, w.CWD, local)...)
			}
		}
		if err := contextsvc.Save(ctx, d.Store, w, refs); err != nil {
			notes = append(notes, w.Target.Key()+": saving extracted context: "+err.Error())
		}
	}
	// A separate bounded read still exposes already committed items if capture
	// used the extraction deadline. A failed read keeps the last saved view.
	readCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	items, err := d.Store.AllContextItems(readCtx)
	if err != nil {
		notes = append(notes, "loading saved context: "+err.Error())
	} else {
		result.items = items
	}
	result.note = strings.Join(notes, "\n")
	return result, outline
}

func (a *App) loadContextPage() tea.Cmd {
	d, windows, items := a.d, a.contextWindows(), a.data.Items
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result, _ := collectContext(ctx, d, windows, items)
		return result
	}
}

func (a *App) openSavedContext(it domain.ContextItem) tea.Cmd {
	src := &ctxSource{mode: "context", client: a.d.Client, project: it.Project, items: a.data.Items}
	item := savedContextItem(it)
	origin := "project " + orNone(it.Project)
	if it.Source != nil {
		origin += " · source " + it.Source.Key()
	}
	if !item.Nested {
		return openContent(src, twopane.OpenMsg{Item: item, Origin: origin})
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		level, err := src.Children(ctx, item)
		if err != nil {
			return statusMsg{fmt.Sprintf("context directory: %v", err)}
		}
		level.Origin = origin
		src.initial = &level
		return twopaneMsg{"context", src}
	}
}
