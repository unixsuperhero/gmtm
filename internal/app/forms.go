package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/launch"
	"github.com/unixsuperhero/gmtm/internal/store"
	"github.com/unixsuperhero/gmtm/internal/ui/forms"
	"github.com/unixsuperhero/gmtm/internal/ui/pathpicker"
)

var noProject = &forms.Option{Value: domain.SessionNoProject, Label: domain.SessionNoProject, Hint: "explicitly no project ownership"}

func (a *App) projectField(def string) forms.Field {
	var opts []forms.Option
	for _, n := range projectNames(a.snap, a.data) {
		opts = append(opts, forms.Option{Value: n})
	}
	if def == "" {
		def = domain.SessionNoProject
	}
	return forms.Field{ID: "project", Label: "Project", Kind: forms.Extensible, Required: true, NoValue: noProject, Options: opts, Default: def,
		Hint: "type to filter; unmatched text stages a new project created on submit"}
}

func (a *App) pushForm(f *forms.Form) {
	w, _ := a.modalSize()
	f.Width = w
	// Path fields emit forms.PickRequestMsg (ctrl+o); see openPicker.
	a.push("form:"+f.ID, f.Title, ptr[*forms.Form]{f}, false)
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case forms.None:
		return ""
	}
	return ""
}

func projectValue(v any) string {
	if _, none := v.(forms.None); none {
		return ""
	}
	s := str(v)
	if s == domain.SessionNoProject {
		return ""
	}
	return s
}

func (a *App) openCreateForm(kind domain.Kind, r row, hasRow bool) tea.Cmd {
	def := a.active
	cwd := ""
	if hasRow {
		if r.win != nil && hostOf(r.win.Target.Host) == domain.HostLocal {
			cwd = r.win.CWD
		}
	}
	hosts := []forms.Option{{Value: domain.HostLocal}}
	for _, h := range a.snap.Hosts {
		if h.Name != "" && h.Name != domain.HostLocal {
			hosts = append(hosts, forms.Option{Value: h.Name, Hint: h.Status})
		}
	}
	fields := []forms.Field{
		{ID: "host", Label: "Host", Kind: forms.Choice, Required: true, Options: hosts, Default: domain.HostLocal},
		a.projectField(def),
		{ID: "session", Label: "Session", Kind: forms.Text, Hint: "blank = placement session (the project's name, or no-project)"},
		{ID: "directory", Label: "Directory", Kind: forms.Path, Path: forms.PathSpec{Mode: forms.PickDir, Host: domain.HostLocal}, Default: cwd,
			Hint: "local directory; not applied to remote hosts"},
		{ID: "name", Label: "Name", Kind: forms.Text},
	}
	if kind == domain.KindAgent {
		var hs, models, efforts []forms.Option
		for _, c := range a.d.Launch.Capabilities() {
			hs = append(hs, forms.Option{Value: c.ID, Hint: c.Reason})
		}
		for _, o := range launch.Catalog.Models {
			models = append(models, forms.Option{Value: o.Value, Parent: o.Harness, Hint: "recognized; access not verified"})
		}
		for _, o := range launch.Catalog.Efforts {
			efforts = append(efforts, forms.Option{Value: o.Value, Parent: o.Harness, Hint: "recognized"})
		}
		fields = append(fields,
			forms.Field{ID: "harness", Label: "Harness", Kind: forms.Choice, Required: true, Options: hs, Default: a.d.Launch.DefaultHarness()},
			forms.Field{ID: "model", Label: "Model", Kind: forms.Linked, ParentID: "harness", Options: models, Hint: "blank = harness default"},
			forms.Field{ID: "effort", Label: "Effort", Kind: forms.Linked, ParentID: "harness", Options: efforts, Hint: "only codex exposes effort"},
			forms.Field{ID: "speed", Label: "Speed", Kind: forms.Linked, ParentID: "harness", Hint: "no recognized harness exposes a speed option"},
			forms.Field{ID: "prompt", Label: "Prompt", Kind: forms.Multiline, Hint: `use \n for line breaks or paste multiline text; ctrl+s submits`},
			forms.Field{ID: "delivery", Label: "Delivery", Kind: forms.Choice, Options: []forms.Option{{Value: "send", Hint: "type once ready"}, {Value: "queue", Hint: "queue-prompt: typed when at rest"}}, Default: "send"},
		)
	}
	f, err := forms.New("create-"+string(kind), "New "+string(kind), fields...)
	if err != nil {
		a.status = "form: " + err.Error()
		return nil
	}
	a.pushForm(f)
	return nil
}

func (a *App) openContextSaveForm(w domain.Window) tea.Cmd {
	kinds := []forms.Option{{Value: "doc"}, {Value: "file"}, {Value: "note"}}
	f, err := forms.New("context-save", "Save context",
		forms.Field{ID: "title", Label: "Title", Kind: forms.Text, Required: true, Default: windowName(w)},
		forms.Field{ID: "kind", Label: "Kind", Kind: forms.Extensible, Required: true, Options: kinds, Default: "doc"},
		forms.Field{ID: "path", Label: "Path", Kind: forms.Path, Path: forms.PathSpec{Mode: forms.PickFile, Host: hostOf(w.Target.Host)}},
		forms.Field{ID: "body", Label: "Body", Kind: forms.Multiline},
		a.projectField(w.Project),
	)
	if err != nil {
		a.status = "form: " + err.Error()
		return nil
	}
	src := w.Target
	a.saveSource = &src
	a.pushForm(f)
	return nil
}

func (a *App) openReassociateForm(it domain.ContextItem) tea.Cmd {
	f, err := forms.New(fmt.Sprintf("reassoc-%d", it.ID), "Project of "+it.Title, a.projectField(it.Project))
	if err != nil {
		a.status = "form: " + err.Error()
		return nil
	}
	a.pushForm(f)
	return nil
}

func (a *App) openProjectChooser() tea.Cmd {
	field := a.projectField("")
	field.Default = nil
	f, err := forms.New("project-choose", "Active project", field)
	if err != nil {
		a.status = "form: " + err.Error()
		return nil
	}
	a.pushForm(f)
	return nil
}

func (a *App) openPaneProjectForm(w domain.Window) tea.Cmd {
	field := a.projectField("")
	field.Default = nil
	field.Hint = "current: " + orNone(w.Project) + "; choose or type a project; only metadata changes"
	f, err := forms.New("pane-project", "Assign project · "+windowName(w), field)
	if err != nil {
		a.status = "form: " + err.Error()
		return nil
	}
	a.assignTarget = w.Target
	a.pushForm(f)
	return nil
}

func (a *App) openSettingForm(s store.Setting) tea.Cmd {
	f, err := forms.New("setting-"+s.Key, "Setting "+s.Key,
		forms.Field{ID: "value", Label: s.Key, Kind: forms.Text, Required: true, Default: s.Value, Hint: "default: " + s.Default})
	if err != nil {
		a.status = "form: " + err.Error()
		return nil
	}
	a.pushForm(f)
	return nil
}

// stagedProjects creates staged project names inside tx.
func stagedProjects(tx store.Tx, staged map[string][]string) error {
	for _, n := range staged["project"] {
		if err := tx.CreateProject(&domain.Project{Name: n}); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) submit(m forms.SubmitMsg) tea.Cmd {
	v := m.Values
	layer := "form:" + m.FormID
	switch {
	case m.FormID == "pane-project":
		target, project := a.assignTarget, projectValue(v["project"])
		return a.tx("pane assigned to "+orNone(project)+" (session unchanged)", layer, func(tx store.Tx) error {
			if err := stagedProjects(tx, m.Staged); err != nil {
				return err
			}
			return tx.SetPaneProject(target.Key(), project)
		})
	case strings.HasPrefix(m.FormID, "create-"):
		kind := domain.Kind(strings.TrimPrefix(m.FormID, "create-"))
		spec := domain.LaunchSpec{Kind: kind, Host: str(v["host"]), Session: strings.TrimSpace(str(v["session"])), Project: projectValue(v["project"]),
			Name: str(v["name"]), CWD: str(v["directory"]), Harness: str(v["harness"]), Model: str(v["model"]), Effort: str(v["effort"]),
			Speed: str(v["speed"]), Prompt: str(v["prompt"]), Queue: str(v["delivery"]) == "queue"}
		if spec.Host != domain.HostLocal && spec.CWD != "" {
			a.status = "directory is a local path and cannot be used on host " + spec.Host + " (draft kept)"
			return nil
		}
		// A staged project is created together with the launch (launch.Service
		// creates the record once the window exists).
		return a.launchCmd(spec, m.FormID)
	case m.FormID == "context-save":
		item := &domain.ContextItem{Kind: str(v["kind"]), Title: str(v["title"]), Path: str(v["path"]), Body: str(v["body"]), Project: projectValue(v["project"]), Source: a.saveSource}
		return a.tx("context saved to "+orNone(item.Project), layer, func(tx store.Tx) error {
			if err := stagedProjects(tx, m.Staged); err != nil {
				return err
			}
			for _, k := range m.Staged["kind"] {
				if err := tx.AddFieldValue("kind", k); err != nil {
					return err
				}
			}
			return tx.SaveContextItem(item)
		})
	case strings.HasPrefix(m.FormID, "reassoc-"):
		var id int64
		fmt.Sscan(strings.TrimPrefix(m.FormID, "reassoc-"), &id)
		p := projectValue(v["project"])
		return a.tx("context project set to "+orNone(p), layer, func(tx store.Tx) error {
			if err := stagedProjects(tx, m.Staged); err != nil {
				return err
			}
			return tx.SetContextProject(id, p)
		})
	case m.FormID == "project-choose":
		project := projectValue(v["project"])
		if len(m.Staged["project"]) == 0 {
			a.removeLayer(layer)
			a.setActive(project)
			return nil
		}
		save := a.tx("", layer, func(tx store.Tx) error {
			return stagedProjects(tx, m.Staged)
		})
		return func() tea.Msg {
			result := save().(txDoneMsg)
			result.activeProject = &project
			return result
		}
	case strings.HasPrefix(m.FormID, "setting-"):
		key := strings.TrimPrefix(m.FormID, "setting-")
		val := strings.TrimSpace(str(v["value"]))
		for _, s := range a.data.Settings {
			if s.Key == key && s.Value == val {
				a.removeLayer(layer)
				a.status = key + " unchanged (not saved)"
				return nil
			}
		}
		return a.tx(key+" saved", layer, func(tx store.Tx) error { return tx.SetSetting(key, val) })
	}
	return nil
}

// openPicker pushes the built-in fuzzy path picker for a form's Path field
// (local only; no native OS dialog). Its reply returns as forms.PickResultMsg.
func (a *App) openPicker(r forms.PickRequestMsg) tea.Cmd {
	m := pathpicker.New(r.PickerOptions())
	w, h := a.modalSize()
	m.SetSize(w, h)
	a.pickReq = &r
	a.push("picker", "Pick "+r.Title, ptr[*pathpicker.Model]{m}, true)
	return m.Init()
}

func (a *App) pickDone(paths []string, err error) tea.Cmd {
	a.removeLayer("picker")
	r := a.pickReq
	a.pickReq = nil
	if r == nil {
		return nil
	}
	return a.broadcastModals(r.Result(paths, err))
}
