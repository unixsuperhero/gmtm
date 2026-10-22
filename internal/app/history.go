package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/capture"
	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/ui/twopane"
)

type capturedHistoryMsg struct {
	gen    uint64
	target string
	text   string
}
type historyTickMsg struct{}

func loadCapturedTurns(ctx context.Context, repo *capture.Repository, service *capture.Service, w domain.Window) ([]capture.Turn, string) {
	if repo == nil {
		return nil, "capture history is unavailable; configure capture in Settings or run setup"
	}
	var note string
	if service != nil && w.Kind != domain.KindAgent {
		status, err := service.Sync(ctx, w.Target)
		if err != nil {
			note = "TUIOS capture: " + err.Error() + " (retained history follows)"
		} else {
			note = status.Detail
		}
	}
	page, err := repo.ListPage(ctx, w.Target)
	if err != nil {
		return nil, "capture history: " + err.Error()
	}
	if page.Limited {
		if note != "" {
			note += "\n"
		}
		note += "Showing newest captured turns (500 rows / 8 MiB display limit); older records remain stored."
	}
	if service != nil {
		status := service.RunStatus()
		if !status.Supported && status.Detail != "" {
			if note != "" {
				note += "\n"
			}
			note += status.Detail
		}
	}
	if len(page.Turns) == 0 {
		if note != "" {
			note += "\n"
		}
		note += "No captured turns for this target. Run setup or use Settings → Capture setup, then reload the agent/open a new shell."
	}
	return page.Turns, note
}

func turnInput(t capture.Turn) string {
	if t.RawCommand != "" {
		return t.RawCommand
	}
	return t.Prompt
}

func turnText(t capture.Turn) string {
	input, output := "Prompt", "Response"
	if t.Kind == "terminal" {
		input, output = "Command", "Output"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s · %s/%s/%s/%s\n", t.Kind, t.State, t.Server, t.Host, t.Session, t.Window)
	fmt.Fprintf(&b, "turn: %s · started: %s\n", t.ID, time.UnixMilli(t.StartedAt).Format(time.RFC3339))
	if t.CompletedAt != 0 {
		fmt.Fprintf(&b, "completed: %s\n", time.UnixMilli(t.CompletedAt).Format(time.RFC3339))
	}
	if t.ExitCode != nil {
		fmt.Fprintf(&b, "exit: %d\n", *t.ExitCode)
	}
	if t.OutputStatus != "" {
		fmt.Fprintf(&b, "output: %s\n", t.OutputStatus)
	}
	if t.Kind == "terminal" && t.RawCommand == "" {
		b.WriteString("command source: daemon reconstruction; exact shell text was not captured\n")
	}
	fmt.Fprintf(&b, "\n%s\n%s\n\n%s\n%s", input, turnInput(t), output, t.Response)
	return sanitizeLine(b.String())
}

func capturedOutline(turns []capture.Turn) []twopane.Item {
	items := make([]twopane.Item, 0, len(turns))
	for _, turn := range turns {
		title := strings.Join(strings.Fields(sanitizeLine(turnInput(turn))), " ")
		if title == "" {
			title = "(input unavailable)"
		}
		items = append(items, twopane.Item{ID: turn.ID, Title: title, Kind: turn.Kind + "/" + turn.State, SearchText: turnInput(turn) + "\n" + turn.Response, Data: turnText(turn)})
	}
	return items
}

func (a *App) syncCapturedDetail(w domain.Window) tea.Cmd {
	key := w.Target.Key()
	if a.historyTarget == key && (a.historyInFlight || time.Since(a.historyRead) < time.Second) {
		return nil
	}
	a.historyGen++
	gen := a.historyGen
	if a.historyTarget != key {
		a.historyText = "loading captured history…"
	}
	a.historyTarget, a.historyInFlight = key, true
	repo, service := a.d.Capture, a.d.History
	jumpKey := strings.Join(a.reg.Keys("jump"), " / ")
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		turns, note := loadCapturedTurns(ctx, repo, service, w)
		var b strings.Builder
		b.WriteString(jumpKey + ": browse/search turns and open exact content\n")
		if note != "" {
			b.WriteString(note + "\n\n")
		}
		for _, turn := range turns {
			b.WriteString(turnText(turn))
			b.WriteString("\n\n")
		}
		return capturedHistoryMsg{gen: gen, target: key, text: b.String()}
	}
}
