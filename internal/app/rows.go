package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/inventory"
	"github.com/unixsuperhero/gmtm/internal/store"
	"github.com/unixsuperhero/gmtm/internal/ui/list"
)

// row is one list item of any page. Exactly one payload pointer is set.
type row struct {
	id     string
	label  string
	fields map[string]string
	search string

	win     *domain.Window
	sess    *domain.Session
	host    *domain.Host
	project string // projects page: "" = the no-project row
	isProj  bool
	item    *domain.ContextItem
	setting *store.Setting
}

func (r row) ID() string                { return r.id }
func (r row) Fields() map[string]string { return r.fields }
func (r row) Label() string             { return r.label }
func (r row) SearchText() string        { return r.search }

// rowProject is the project ownership a row carries ("" = none/unknown).
func (r row) rowProject() string {
	switch {
	case r.win != nil:
		return r.win.Project
	case r.sess != nil:
		return r.sess.Project
	case r.isProj:
		return r.project
	case r.item != nil:
		return r.item.Project
	}
	return ""
}

// storeData is the application-owned state read from SQLite.
type storeData struct {
	Projects []domain.Project
	Items    []domain.ContextItem
	Pins     map[string]bool
	Stars    map[string]bool
	Settings []store.Setting
	Err      error
}

func orNone(s string) string {
	if s == "" {
		return domain.SessionNoProject
	}
	return s
}

func age(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// attention composes every flag as text; none hides another.
func attention(w domain.Window) string {
	var a []string
	f := w.Flags
	if f.NeedsInput {
		a = append(a, "needs-input")
	}
	if f.Blocked {
		a = append(a, "blocked")
	}
	if f.Working {
		a = append(a, "working")
	}
	if f.Unread > 0 {
		a = append(a, fmt.Sprintf("unread:%d", f.Unread))
	}
	if f.Queued > 0 {
		a = append(a, fmt.Sprintf("queued:%d", f.Queued))
	}
	if f.Completed {
		a = append(a, "completed")
	}
	if f.Subagents > 0 {
		a = append(a, fmt.Sprintf("subagents:%d", f.Subagents))
	}
	if w.Closed {
		a = append(a, "closed")
	} else if w.Unavailable {
		a = append(a, "unavailable")
	}
	if w.Stale {
		a = append(a, "stale")
	}
	return strings.Join(a, ",")
}

func windowName(w domain.Window) string {
	for _, s := range []string{w.Name, w.Title, w.Target.WindowID} {
		if s != "" {
			return s
		}
	}
	return "?"
}

func hostOf(h string) string {
	if h == "" {
		return domain.HostLocal
	}
	return h
}

func windowRows(snap domain.Snapshot, project string, scoped bool) []list.Item {
	out := make([]list.Item, 0, len(snap.Windows))
	for i := range snap.Windows {
		w := snap.Windows[i]
		if scoped && w.Project != project {
			continue
		}
		state := string(w.State)
		if w.State == domain.StateUnknown && w.RawState != "" {
			state = "unknown(" + w.RawState + ")"
		}
		att := attention(w)
		name := windowName(w)
		label := fmt.Sprintf("%-18s %-8s %-12s %-12s %-14s %4s %s", name, w.Kind, hostOf(w.Target.Host)+"/"+w.Target.Session, orNone(w.Project), state, age(w.LastActivity), att)
		out = append(out, row{id: w.Target.Key(), label: label, win: &w,
			search: strings.Join([]string{name, w.Title, w.Target.WindowID, w.Target.Session, w.Project, string(w.Kind), w.Harness, w.CWD}, " "),
			fields: map[string]string{"host": hostOf(w.Target.Host), "session": w.Target.Session, "project": orNone(w.Project),
				"kind": string(w.Kind), "state": state, "attention": att, "harness": w.Harness,
				"lastActivity": w.LastActivity.UTC().Format(time.RFC3339), "name": name}})
	}
	return out
}

func sessionRows(snap domain.Snapshot) []list.Item {
	var out []list.Item
	for i := range snap.Sessions {
		s := snap.Sessions[i]
		stale := ""
		if s.Stale {
			stale = "stale"
		}
		out = append(out, row{id: "session|" + s.Server + "|" + hostOf(s.Host) + "|" + s.Name, sess: &s,
			label:  fmt.Sprintf("%-20s %-10s %-14s windows:%-3d %s", s.Name, hostOf(s.Host), orNone(s.Project), s.Windows, stale),
			search: s.Name + " " + s.Project + " " + s.ID,
			fields: map[string]string{"host": hostOf(s.Host), "project": orNone(s.Project), "name": s.Name, "windows": fmt.Sprint(s.Windows)}})
	}
	return out
}

func hostRows(snap domain.Snapshot) []list.Item {
	hosts := snap.Hosts
	if len(hosts) == 0 {
		hosts = []domain.Host{{Server: snap.Connection.Server, Name: domain.HostLocal, Status: "local (no remote hosts reported)"}}
	}
	var out []list.Item
	for i := range hosts {
		h := hosts[i]
		out = append(out, row{id: "host|" + h.Server + "|" + h.Name, host: &h,
			label:  fmt.Sprintf("%-20s %s", h.Name, h.Status),
			search: h.Name + " " + h.Status, fields: map[string]string{"status": h.Status, "name": h.Name}})
	}
	return out
}

// projectNames unions store projects and session-derived projects.
func projectNames(snap domain.Snapshot, d storeData) []string {
	set := map[string]bool{}
	for _, p := range d.Projects {
		set[p.Name] = true
	}
	for _, s := range snap.Sessions {
		if p := inventory.ProjectForSession(s.Name); p != "" {
			set[p] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func projectRows(snap domain.Snapshot, d storeData) []list.Item {
	count := map[string]int{}
	for _, w := range snap.Windows {
		count[w.Project]++
	}
	items := map[string]int{}
	for _, it := range d.Items {
		items[it.Project]++
	}
	stored := map[string]bool{}
	for _, p := range d.Projects {
		stored[p.Name] = true
	}
	out := []list.Item{row{id: "project|", isProj: true, label: fmt.Sprintf("%-20s windows:%-3d context:%-3d reserved (no ownership)", domain.SessionNoProject, count[""], items[""]),
		search: domain.SessionNoProject, fields: map[string]string{"source": "reserved"}}}
	for _, n := range projectNames(snap, d) {
		src := "session"
		if stored[n] {
			src = "store"
		}
		out = append(out, row{id: "project|" + n, isProj: true, project: n,
			label:  fmt.Sprintf("%-20s windows:%-3d context:%-3d %s", n, count[n], items[n], src),
			search: n, fields: map[string]string{"source": src, "name": n}})
	}
	return out
}

func contextRows(d storeData) []list.Item {
	var out []list.Item
	for i := range d.Items {
		it := d.Items[i]
		src := ""
		if it.Source != nil {
			src = it.Source.Session + "/" + it.Source.WindowID
		}
		out = append(out, row{id: fmt.Sprintf("ctx|%d", it.ID), item: &it,
			label:  fmt.Sprintf("%-6s %-30s %-14s %s", it.Kind, it.Title, orNone(it.Project), src),
			search: it.Title + " " + it.Path + " " + it.Body + " " + it.Project + " " + strings.Join(it.Tags, " "),
			fields: map[string]string{"kind": it.Kind, "project": orNone(it.Project), "tags": strings.Join(it.Tags, ","), "title": it.Title}})
	}
	return out
}

func settingRows(d storeData) []list.Item {
	out := []list.Item{
		row{id: "capture.setup", label: "Capture setup · Enter to install terminal and agent integrations",
			search: "capture setup install terminal command output agent prompt response",
			fields: map[string]string{"state": "setup"}},
		row{id: "capture.status", label: "Capture status · Enter to inspect integration files",
			search: "capture setup status integration files",
			fields: map[string]string{"state": "setup"}},
	}
	for i := range d.Settings {
		s := d.Settings[i]
		state := "default"
		if s.Stored {
			state = "stored"
		}
		out = append(out, row{id: "setting|" + s.Key, setting: &s,
			label:  fmt.Sprintf("%-16s %-12s default:%-10s %s", s.Key, s.Value, s.Default, state),
			search: s.Key + " " + s.Value, fields: map[string]string{"state": state}})
	}
	return out
}
