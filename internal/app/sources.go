package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/tuios"
	"github.com/unixsuperhero/gmtm/internal/ui/twopane"
)

// ctxSource is the Jump / Context / meta-mode source for one originating
// window (possibly none). It never invents associations: saved items come
// from SQLite, files from the daemon's file-list verb, the outline from
// agent-activity or a read-only capture.
type ctxSource struct {
	mode    string // jump | context | meta
	client  tuios.Client
	win     *domain.Window
	project string // the window's project, else the active project
	outline []twopane.Item
	outNote string
	items   []domain.ContextItem
	names   []string
}

type (
	modeRef     struct{ mode string }
	projectsRef struct{}
	projRef     struct{ name string }
	filesRef    struct{ dir string }
	fileRef     struct{ path string }
	itemRef     struct{ it domain.ContextItem }
)

type twopaneMsg struct {
	mode string
	src  *ctxSource
}

func (s *ctxSource) origin() string {
	if s.win == nil {
		return "no window selected · active project " + orNone(s.project)
	}
	t := s.win.Target
	return fmt.Sprintf("%s · %s · %s · %s (%s)", t.Server, hostOf(t.Host), t.Session, t.WindowID, windowName(*s.win))
}

func (s *ctxSource) Root() twopane.Level { return s.level(s.mode) }

func (s *ctxSource) level(mode string) twopane.Level {
	switch mode {
	case "jump":
		lv := twopane.Level{Title: "Jump", Origin: s.origin(), Items: s.outline, Empty: "no outline entries reported"}
		if s.win == nil {
			lv.Unsupported = "Jump needs a selected agent or terminal on the Panes page"
		} else if s.outNote != "" {
			lv.Empty = s.outNote
		}
		return lv
	case "context":
		lv := twopane.Level{Title: "Context", Origin: s.origin(), Empty: "no saved context for " + orNone(s.project)}
		for _, it := range s.items {
			if it.Project == s.project {
				lv.Items = append(lv.Items, twopane.Item{ID: fmt.Sprintf("item-%d", it.ID), Title: itemTitle(it), Kind: it.Kind, Data: itemRef{it}})
			}
		}
		files := twopane.Item{ID: "files", Title: "Files in working directory", Kind: "files", Nested: true}
		switch {
		case s.win == nil || s.win.CWD == "":
			files.Unavailable = "the window reports no working directory"
		case hostOf(s.win.Target.Host) != domain.HostLocal:
			files.Unavailable = "file listing on remote host " + s.win.Target.Host + " is not supported here"
		default:
			files.Title += " (" + s.win.CWD + ")"
			files.Data = filesRef{s.win.CWD}
		}
		lv.Items = append(lv.Items, files, twopane.Item{ID: "projects", Title: "Project context…", Kind: "projects", Nested: true, Data: projectsRef{}})
		return lv
	}
	return twopane.Level{Title: "Modes", Origin: s.origin(), Items: []twopane.Item{
		{ID: "m-jump", Title: "Jump", Kind: "mode", Nested: true, Data: modeRef{"jump"}},
		{ID: "m-context", Title: "Context", Kind: "mode", Nested: true, Data: modeRef{"context"}},
		{ID: "m-projects", Title: "Project context", Kind: "mode", Nested: true, Data: projectsRef{}},
	}}
}

func itemTitle(it domain.ContextItem) string {
	if it.Title != "" {
		return it.Title
	}
	return fmt.Sprintf("%s #%d", it.Kind, it.ID)
}

func (s *ctxSource) Children(ctx context.Context, it twopane.Item) (twopane.Level, error) {
	switch d := it.Data.(type) {
	case modeRef:
		return s.level(d.mode), nil
	case projectsRef:
		lv := twopane.Level{Title: "Projects", Items: []twopane.Item{{ID: "p-", Title: domain.SessionNoProject, Kind: "project", Nested: true, Data: projRef{""}}}}
		for _, n := range s.names {
			lv.Items = append(lv.Items, twopane.Item{ID: "p-" + n, Title: n, Kind: "project", Nested: true, Data: projRef{n}})
		}
		return lv, nil
	case projRef:
		lv := twopane.Level{Title: orNone(d.name), Empty: "no saved context for " + orNone(d.name)}
		for _, x := range s.items {
			if x.Project == d.name {
				lv.Items = append(lv.Items, twopane.Item{ID: fmt.Sprintf("item-%d", x.ID), Title: itemTitle(x), Kind: x.Kind, Data: itemRef{x}})
			}
		}
		return lv, nil
	case filesRef:
		return s.files(ctx, d.dir)
	}
	return twopane.Level{}, errors.New("not a nested item")
}

// files lists a local directory through the daemon's file-list verb.
func (s *ctxSource) files(ctx context.Context, dir string) (twopane.Level, error) {
	var res map[string]any
	if err := s.client.Call(ctx, "file-list", map[string]any{"dir": dir, "limit": 500}, &res); err != nil {
		return twopane.Level{}, err
	}
	lv := twopane.Level{Title: filepath.Base(dir), Empty: "empty directory"}
	for _, e := range firstList(res) {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		name := firstStr(m, "name", "path")
		if name == "" {
			continue
		}
		full := name
		if !filepath.IsAbs(full) {
			full = filepath.Join(dir, name)
		}
		isDir, _ := m["is_dir"].(bool)
		if d, ok := m["dir"].(bool); ok {
			isDir = d
		}
		if t := firstStr(m, "type", "kind"); t == "dir" || t == "directory" {
			isDir = true
		}
		if isDir {
			lv.Items = append(lv.Items, twopane.Item{ID: full, Title: filepath.Base(name) + "/", Kind: "dir", Nested: true, Data: filesRef{full}})
		} else {
			lv.Items = append(lv.Items, twopane.Item{ID: full, Title: filepath.Base(name), Kind: "file", Data: fileRef{full}})
		}
	}
	return lv, nil
}

func firstList(m map[string]any) []any {
	for _, k := range []string{"entries", "files", "items"} {
		if l, ok := m[k].([]any); ok {
			return l
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if l, ok := m[k].([]any); ok {
			return l
		}
	}
	return nil
}

func (s *ctxSource) Preview(_ context.Context, it twopane.Item) (string, error) {
	switch d := it.Data.(type) {
	case string:
		return d, nil
	case itemRef:
		return itemText(d.it), nil
	case fileRef:
		return readHead(d.path)
	case filesRef:
		return "directory " + d.dir + " — enter to browse", nil
	case projRef:
		return "saved context of " + orNone(d.name) + " — enter to browse", nil
	case projectsRef:
		return "browse saved context by project (also after the source window closed)", nil
	case modeRef:
		return d.mode + " mode", nil
	}
	return "", nil
}

func itemText(it domain.ContextItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · project %s", it.Kind, orNone(it.Project))
	if it.Source != nil {
		fmt.Fprintf(&b, " · source %s", it.Source.Key())
	}
	if it.Path != "" {
		fmt.Fprintf(&b, "\npath: %s", it.Path)
	}
	if it.Body != "" {
		b.WriteString("\n\n" + it.Body)
	}
	return b.String()
}

func readHead(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, 32<<10))
	if err != nil {
		return "", err
	}
	if strings.IndexByte(string(buf), 0) >= 0 {
		return "(binary file)", nil
	}
	return sanitizeLine(string(buf)), nil
}

// openTwopane prefetches the outline (bounded) and then opens the modal.
func (a *App) openTwopane(mode string) tea.Cmd {
	src := &ctxSource{mode: mode, client: a.d.Client, items: a.data.Items, names: projectNames(a.snap, a.data), project: a.active}
	if w, ok := a.selectedWindow(); ok && a.cur == 0 {
		cp := *w
		src.win, src.project = &cp, w.Project
	}
	c := a.d.Client
	return func() tea.Msg {
		if src.win == nil {
			return twopaneMsg{mode, src}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		t := src.win.Target
		if src.win.Kind == domain.KindAgent {
			act, err := tuios.AgentActivity(ctx, c, t.Session, t.WindowID, 100, false)
			if err == nil && len(act.Entries) > 0 {
				for i, e := range act.Entries {
					full := []string{}
					for k, v := range e {
						full = append(full, fmt.Sprintf("%s: %v", k, v))
					}
					sort.Strings(full)
					src.outline = append(src.outline, twopane.Item{ID: fmt.Sprint(i), Title: entryTitle(e), Kind: firstStr(e, "kind", "type", "event"), Data: sanitizeLine(strings.Join(full, "\n"))})
				}
				return twopaneMsg{mode, src}
			}
			if err != nil {
				src.outNote = "agent activity unavailable: " + err.Error() + "; showing captured lines"
			}
		}
		text, err := tuios.CapturePane(ctx, c, t.Session, t.WindowID, "recent", 200, false)
		if err != nil {
			src.outNote = "capture unavailable: " + err.Error()
			return twopaneMsg{mode, src}
		}
		lines := strings.Split(sanitizeLine(text), "\n")
		for i, l := range lines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			lo, hi := max(0, i-10), min(len(lines), i+11)
			src.outline = append(src.outline, twopane.Item{ID: fmt.Sprint(i), Title: strings.TrimSpace(l), Kind: "line", Data: strings.Join(lines[lo:hi], "\n")})
		}
		return twopaneMsg{mode, src}
	}
}

func (a *App) onTwopane(m twopaneMsg) tea.Cmd {
	w, h := a.modalSize()
	tp := twopane.New(m.src, w, h)
	a.twoSrc = m.src
	a.push(m.mode, map[string]string{"jump": "Jump", "context": "Context", "meta": "Modes"}[m.mode], ptr[*twopane.Model]{tp}, true)
	return tp.Init()
}

// openContent replaces the main area with the item's read-only content
// (never live mode).
func openContent(src twopane.Source, m twopane.OpenMsg) tea.Cmd {
	return func() tea.Msg {
		text := "(no content)"
		if src != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			t, err := src.Preview(ctx, m.Item)
			if err != nil {
				t = "unavailable: " + err.Error()
			}
			text = t
		}
		return contentMsg{title: strings.Join(append(m.Path, m.Item.Title), " › "), text: "origin: " + m.Origin + "\n" + text}
	}
}
