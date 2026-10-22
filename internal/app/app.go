// Package app is the thin orchestrator: a top-level tea.Model that routes
// messages to modules, composes the frame, page switcher, pages and modal
// stack, and holds mode/focus. Domain rules live in inventory/launch/store,
// protocol in tuios, widgets in ui/*.
//
// Key routing order: the reserved global binding (F4 New note) first; then
// the top modal layer (so a note opened over live mode never leaks keys to
// the target); then live mode, which forwards everything else to
// internal/live; then the read-only content view; then the page (registry
// sequences/keys first, list navigation and search otherwise).
package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/actions"
	"github.com/unixsuperhero/gmtm/internal/capture"
	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/inventory"
	"github.com/unixsuperhero/gmtm/internal/launch"
	"github.com/unixsuperhero/gmtm/internal/live"
	"github.com/unixsuperhero/gmtm/internal/preview"
	"github.com/unixsuperhero/gmtm/internal/setup"
	"github.com/unixsuperhero/gmtm/internal/store"
	"github.com/unixsuperhero/gmtm/internal/tuios"
	"github.com/unixsuperhero/gmtm/internal/ui/forms"
	"github.com/unixsuperhero/gmtm/internal/ui/frame"
	"github.com/unixsuperhero/gmtm/internal/ui/keys"
	"github.com/unixsuperhero/gmtm/internal/ui/list"
	"github.com/unixsuperhero/gmtm/internal/ui/modal"
	"github.com/unixsuperhero/gmtm/internal/ui/twopane"
)

// Deps are the modules the orchestrator composes. One client per process.
type Deps struct {
	Client   tuios.Client
	Inv      *inventory.Service
	Store    store.Store
	Launch   *launch.Service
	Registry *actions.Registry
	Capture  *capture.Repository
	History  *capture.Service
	Setup    *setup.Manager
	// Project is the explicit active project ("" = none chosen).
	Project string
	// WaitUpdates subscribes to inventory Updates() (off in unit tests).
	WaitUpdates bool
}

// Setting defaults (stored value vs default shown on the Settings page).
var settingDefaults = map[string]string{
	"live.leader":   live.DefaultLeader,
	"keys.jump":     "J",
	"keys.context":  "C",
	"keys.metamode": "M",
	"keys.note":     "f4",
	"preview.lines": "200",
}

var settingAction = map[string]string{"keys.jump": "jump", "keys.context": "context", "keys.metamode": "metamode", "keys.note": "note.new"}

// App is the root model.
type App struct {
	d      Deps
	reg    *actions.Registry
	router *keys.Router
	leader string

	w, h  int
	pages []*page
	cur   int
	snap  domain.Snapshot
	data  storeData

	modals   modal.Stack
	confirms map[string]func() tea.Cmd

	live         *live.Session
	liveStarting bool

	prev            *preview.Service
	tracker         *preview.Tracker
	cache           *preview.Cache
	shot            *preview.ResultMsg
	inFlight        bool
	lastShot        time.Time
	actGen          uint64
	actTarget       string
	activity        string
	historyGen      uint64
	historyTarget   string
	historyText     string
	historyInFlight bool
	historyRead     time.Time

	twoSrc       twopane.Source
	saveSource   *domain.Target
	assignTarget domain.Target
	pickReq      *forms.PickRequestMsg
	content      *contentView
	status       string

	active     string
	activeNone bool
	retry      *retryInfo
	everRead   bool
}

type contentView struct {
	title, text string
	scroll      int
}

type retryInfo struct {
	target domain.Target
	prompt string
	queue  bool
}

// messages
const screenRefreshInterval = 250 * time.Millisecond

type (
	invMsg         struct{}
	screenTickMsg  struct{}
	refreshDoneMsg struct{ err error }
	storeMsg       struct{ d storeData }
	statusMsg      struct{ text string }
	launchMsg      struct {
		res    launch.Result
		err    error
		spec   domain.LaunchSpec
		formID string
	}
	closedMsg struct {
		err    error
		target domain.Target
	}
	txDoneMsg struct {
		err           error
		ok            string
		layer         string
		activeProject *string
	}
	noteSavedMsg struct{ err error }
	liveStartMsg struct {
		s   *live.Session
		err error
	}
	liveStoppedMsg struct {
		err    error
		reason string
	}
	activityMsg struct {
		gen  uint64
		text string
	}
	serverStartMsg struct{ err error }
	contentMsg     struct{ title, text string }
	confirmMsg     struct {
		id  string
		yes bool
	}
)

// New builds the orchestrator.
func New(d Deps) *App {
	a := &App{d: d, reg: d.Registry, leader: live.DefaultLeader, pages: newPages(), confirms: map[string]func() tea.Cmd{},
		prev: preview.New(d.Client), tracker: &preview.Tracker{}, cache: preview.NewCache(), active: d.Project, w: 100, h: 30}
	if a.reg == nil {
		a.reg = actions.NewDefault()
	}
	a.pages[0].lst.Detail = func(it list.Item) string {
		r, ok := it.(row)
		if !ok || r.win == nil {
			return ""
		}
		w := r.win
		s := fmt.Sprintf("  %s · cwd %s", w.Target.Key(), w.CWD)
		if w.Harness != "" {
			s += " · " + w.Harness
		}
		if w.RawState != "" && string(w.State) != w.RawState {
			s += " · raw state " + w.RawState
		}
		return s
	}
	a.rebuildRouter()
	a.snap = d.Inv.Snapshot()
	a.layout()
	return a
}

func (a *App) rebuildRouter() {
	r := keys.NewRouter()
	r.Globals = map[string]string{}
	for _, b := range a.reg.Effective() {
		for _, k := range b.Keys {
			if containsStr(b.Action.Modes, actions.ModeGlobal) {
				r.SetGlobal(k, b.Action.ID)
			} else if containsStr(b.Action.Modes, actions.ModeNormal) {
				r.AddSequences(k)
			}
		}
	}
	a.router = r
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Init loads the store, reads the inventory and subscribes to updates.
func (a *App) Init() tea.Cmd {
	cmds := []tea.Cmd{a.loadStore(), a.refreshCmd()}
	if a.d.WaitUpdates {
		cmds = append(cmds, a.waitUpdates())
	}
	return tea.Batch(cmds...)
}

func (a *App) waitUpdates() tea.Cmd {
	ch := a.d.Inv.Updates()
	return func() tea.Msg {
		if _, ok := <-ch; !ok {
			return nil
		}
		return invMsg{}
	}
}

func (a *App) refreshCmd() tea.Cmd {
	inv := a.d.Inv
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return refreshDoneMsg{err: inv.Refresh(ctx)}
	}
}

func (a *App) loadStore() tea.Cmd {
	st := a.d.Store
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var d storeData
		var err error
		if d.Projects, err = st.Projects(ctx); err != nil {
			return storeMsg{storeData{Err: err}}
		}
		if d.Items, err = st.AllContextItems(ctx); err != nil {
			return storeMsg{storeData{Err: err}}
		}
		if d.Pins, err = st.Pins(ctx); err != nil {
			return storeMsg{storeData{Err: err}}
		}
		if d.Stars, err = st.Stars(ctx); err != nil {
			return storeMsg{storeData{Err: err}}
		}
		if d.PaneProjects, err = st.PaneProjects(ctx); err != nil {
			return storeMsg{storeData{Err: err}}
		}
		if d.Settings, err = st.Settings(ctx, settingDefaults); err != nil {
			return storeMsg{storeData{Err: err}}
		}
		return storeMsg{d}
	}
}

func (a *App) page() *page { return a.pages[a.cur] }

func (a *App) panes() *page { return a.pages[0] }

// ---- layout ----

func (a *App) frame() frame.Frame {
	c := a.snap.Connection
	conn := string(c.State)
	if conn == "" {
		conn = string(domain.ConnUnknown)
	}
	return frame.Frame{Width: a.w, Height: a.h, Mode: a.modeText(), Project: a.projectText(), Server: a.d.Client.Server(), Conn: conn}
}

// bodyHeights splits the page body into list and detail rows. Metadata
// (list) shrinks before the detail/preview area disappears.
func (a *App) bodyHeights(p *page) (listH, detailH int) {
	_, ih := a.frame().Inner()
	bh := max(1, ih-2) // switcher + status line
	if p.detailRows == 0 {
		return bh, 0
	}
	listH = max(3, bh*2/5)
	if p.id != actions.PagePanes {
		listH = max(3, bh-p.detailRows)
	}
	if listH >= bh {
		return bh, 0
	}
	return listH, bh - listH
}

func (a *App) layout() {
	iw, _ := a.frame().Inner()
	for _, p := range a.pages {
		lh, _ := a.bodyHeights(p)
		p.lst = p.lst.SetSize(iw, lh)
	}
}

// liveSize is the measured detail area minus its tab line: never the whole terminal.
func (a *App) liveSize() (int, int) {
	iw, _ := a.frame().Inner()
	_, dh := a.bodyHeights(a.panes())
	return max(1, iw), max(1, dh-1)
}

// ---- mode / project text ----

func (a *App) modeText() string {
	if l, ok := a.modals.Top(); ok {
		switch {
		case l.ID == "note":
			return "note"
		case l.ID == "picker":
			return "insert"
		case l.ID == "palette", l.ID == "help", l.ID == "jump", l.ID == "context", l.ID == "meta":
			return l.ID
		case strings.HasPrefix(l.ID, "form:"):
			if f, ok := formOf(l); ok && f.Insert() {
				return "insert"
			}
		}
		return "normal"
	}
	if a.live != nil {
		return "live"
	}
	if a.content == nil && a.page().lst.Searching() {
		return "insert"
	}
	return "normal"
}

func (a *App) projectText() string {
	switch {
	case a.active != "":
		return a.active
	case a.activeNone:
		return domain.SessionNoProject
	case !a.everRead && a.snap.Connection.State == domain.ConnDisconnected:
		return domain.ProjectUnknown
	}
	return domain.SessionNoProject
}

func (a *App) routeMode() keys.Mode {
	switch {
	case !a.modals.Empty():
		return keys.Insert // everything but globals goes to the layer
	case a.live != nil || a.liveStarting:
		return keys.Live
	case a.content == nil && a.page().lst.Searching():
		return keys.Insert
	}
	return keys.Normal
}

// ---- update ----

// Update routes one message.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := a.update(msg)
	return a, tea.Batch(cmd, a.syncDetail())
}

func (a *App) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = m.Width, m.Height
		a.layout()
		a.resizeModals()
		if a.live != nil {
			s, c, r := a.live, 0, 0
			c, r = a.liveSize()
			return func() tea.Msg {
				if err := s.Resize(c, r); err != nil {
					return statusMsg{"live resize: " + err.Error()}
				}
				return nil
			}
		}
		return nil
	case tea.KeyMsg:
		return a.key(m)
	case keys.TimeoutMsg:
		a.router.HandleTimeout(m)
		return nil
	case invMsg:
		a.onInventory()
		return a.waitUpdates()
	case refreshDoneMsg:
		if m.err != nil {
			a.status = "inventory read failed: " + m.err.Error()
		}
		a.onInventory()
		return nil
	case storeMsg:
		if m.d.Err != nil {
			a.status = "database error: " + m.d.Err.Error() + " (no fallback storage)"
			return nil
		}
		a.data = m.d
		a.applySettings()
		a.refreshPages()
		return nil
	case contextLoadedMsg:
		a.data.Items = m.items
		a.refreshPages()
		if m.note != "" {
			a.status = m.note
		}
		return nil
	case statusMsg:
		a.status = m.text
		return nil
	case serverStartMsg:
		if m.err != nil {
			a.status = "start server " + a.d.Client.Server() + " failed: " + m.err.Error()
		} else {
			a.status = "server " + a.d.Client.Server() + " started; resynchronized"
		}
		a.onInventory()
		return nil
	case preview.ResultMsg:
		if a.tracker.Accept(m) {
			r := a.cache.Resolve(m)
			a.shot, a.inFlight, a.lastShot = &r, false, time.Now()
			return tea.Tick(screenRefreshInterval, func(time.Time) tea.Msg { return screenTickMsg{} })
		}
		return nil
	case screenTickMsg:
		return nil // Update's syncDetail captures again when the screen is visible.
	case activityMsg:
		if m.gen == a.actGen {
			a.activity = m.text
		}
		return nil
	case capturedHistoryMsg:
		if m.gen != a.historyGen || m.target != a.historyTarget {
			return nil
		}
		a.historyText, a.historyInFlight, a.historyRead = m.text, false, time.Now()
		return tea.Tick(time.Second, func(time.Time) tea.Msg { return historyTickMsg{} })
	case historyTickMsg:
		return nil
	case setupStatusMsg:
		return a.onSetupStatus(m)
	case contentMsg:
		a.content = &contentView{title: m.title, text: m.text}
		return nil
	case confirmMsg:
		a.removeLayer("confirm:" + m.id)
		fn := a.confirms[m.id]
		delete(a.confirms, m.id)
		if m.yes && fn != nil {
			return fn()
		}
		return nil
	}
	if cmd, ok := a.handleModuleMsg(msg); ok {
		return cmd
	}
	if cmd, ok := a.handleLiveMsg(msg); ok {
		return cmd
	}
	// Anything else: async replies for modal bodies (form completions, picker
	// results, twopane previews, note Ctrl+Enter sequences) and live frames.
	var cmds []tea.Cmd
	cmds = append(cmds, a.broadcastModals(msg))
	if a.live != nil {
		cmds = append(cmds, a.live.Update(msg))
	}
	return tea.Batch(cmds...)
}

func (a *App) onInventory() {
	a.snap = a.d.Inv.Snapshot()
	if a.snap.Connection.State == domain.ConnConnected {
		a.everRead = true
	}
	a.refreshPages()
}

func (a *App) refreshPages() {
	// Snapshot owns its windows; overlay application metadata without changing
	// daemon identity or the inventory service's session-derived defaults.
	for i := range a.snap.Windows {
		w := &a.snap.Windows[i]
		if project, ok := a.data.PaneProjects[w.Target.Key()]; ok {
			w.Project = project
		}
	}
	for _, p := range a.pages {
		p.refresh(a.snap, a.data, a.active, a.active != "" || a.activeNone)
	}
}

func (a *App) key(k tea.KeyMsg) tea.Cmd {
	res := a.router.Route(a.routeMode(), k)
	if res.Kind == keys.Global {
		return a.exec(res.Action, "")
	}
	if !a.modals.Empty() {
		var cmd tea.Cmd
		a.modals, cmd = a.modals.Update(k)
		return cmd
	}
	if a.liveStarting {
		a.status = "starting live… (key not sent)"
		return nil
	}
	if a.live != nil {
		_, cmd := a.live.HandleKey(k)
		return cmd
	}
	if a.content != nil {
		return a.contentKey(k)
	}
	p := a.page()
	if p.lst.Searching() {
		return p.update(k)
	}
	switch res.Kind {
	case keys.Pending:
		a.status = "keys: " + res.Key + " …"
		return a.router.TimeoutCmd()
	case keys.Cancelled:
		a.status = ""
		return nil
	case keys.Sequence, keys.Pass, keys.Navigate:
		if act, ok := a.reg.Lookup(actions.ModeNormal, p.id, res.Key); ok && act.Page == "" || ok && act.Page == p.id {
			a.status = ""
			return a.exec(act.ID, "")
		}
		if res.Kind == keys.Sequence {
			a.status = "no action for " + res.Key
			return nil
		}
		return p.update(k)
	}
	return nil
}

func (a *App) contentKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc", "q":
		a.content = nil
	case "j", "down":
		a.content.scroll++
	case "k", "up":
		a.content.scroll = max(0, a.content.scroll-1)
	case "g":
		a.content.scroll = 0
	case "d", "ctrl+d", "pgdown":
		a.content.scroll += 10
	case "u", "ctrl+u", "pgup":
		a.content.scroll = max(0, a.content.scroll-10)
	}
	return nil
}

// ---- detail (preview/activity) sync: only the selected target ----

func (a *App) selectedWindow() (*domain.Window, bool) {
	r, ok := a.panes().selected()
	if !ok || r.win == nil {
		return nil, false
	}
	return r.win, true
}

func (a *App) syncDetail() tea.Cmd {
	if a.cur != 0 || a.live != nil || a.liveStarting || a.content != nil || !a.modals.Empty() {
		a.lastShot = time.Time{}
		return nil
	}
	w, ok := a.selectedWindow()
	if !ok {
		a.lastShot = time.Time{}
		return nil
	}
	p := a.panes()
	if paneTabs[p.tab] != "Screen" {
		a.lastShot = time.Time{}
	}
	switch paneTabs[p.tab] {
	case "Transcript", "Turns":
		return a.syncCapturedDetail(*w)
	case "Screen":
		cur, _ := a.tracker.Current()
		stale := cur.Key() != w.Target.Key() || (!a.inFlight && time.Since(a.lastShot) >= screenRefreshInterval)
		if !stale || (a.inFlight && cur.Key() == w.Target.Key()) {
			return nil
		}
		gen := a.tracker.Begin(w.Target)
		a.inFlight = true
		if a.shot != nil && a.shot.Target.Key() != w.Target.Key() {
			a.shot = nil
		}
		return a.prev.Capture(context.Background(), w.Target, gen, preview.Opts{Lines: preview.MaxLines, Styled: true, Visible: true})
	case "Activity":
		if a.actTarget == w.Target.Key() {
			return nil
		}
		a.actTarget = w.Target.Key()
		a.actGen++
		a.activity = "loading activity…"
		gen, c, t := a.actGen, a.d.Client, w.Target
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			act, err := tuios.AgentActivity(ctx, c, t.Session, t.WindowID, 50, false)
			if err != nil {
				return activityMsg{gen, "activity unavailable: " + err.Error()}
			}
			return activityMsg{gen, formatActivity(act)}
		}
	}
	return nil
}

func formatActivity(act tuios.Activity) string {
	if len(act.Entries) == 0 {
		return "(no activity entries reported)"
	}
	var b strings.Builder
	if act.Untrusted {
		b.WriteString("agent-reported text (untrusted)\n")
	}
	for _, e := range act.Entries {
		b.WriteString(entryTitle(e) + "\n")
	}
	return b.String()
}

func entryTitle(e map[string]any) string {
	kind := firstStr(e, "kind", "type", "event")
	text := firstStr(e, "text", "summary", "tool", "message", "prompt", "command")
	return sanitizeLine(strings.TrimSpace(kind + ": " + text))
}

func firstStr(m map[string]any, ks ...string) string {
	for _, k := range ks {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// sanitizeLine drops control characters (terminal output is untrusted).
func sanitizeLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < 0x20 && r != '\n' || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}

// ---- view ----

// View renders the frame around the switcher, page and modal stack.
func (a *App) View() string {
	f := a.frame()
	if f.TooSmall() {
		return f.Render("")
	}
	iw, ih := f.Inner()
	base := a.body(iw, ih)
	if !a.modals.Empty() {
		base = a.modals.Render(base, iw, ih)
	}
	return f.Render(base)
}

func (a *App) switcher(w int) string {
	var parts []string
	for i, p := range a.pages {
		if i == a.cur {
			parts = append(parts, fmt.Sprintf("[%d %s]", i+1, p.title))
		} else {
			parts = append(parts, fmt.Sprintf(" %d %s ", i+1, p.title))
		}
	}
	att := 0
	for _, w := range a.snap.Windows {
		if w.Flags.NeedsInput || w.Flags.Blocked {
			att++
		}
	}
	s := strings.Join(parts, "")
	if att > 0 {
		s += fmt.Sprintf("  ! attention:%d", att)
	}
	return frame.Truncate(s, w)
}

func (a *App) statusLine(w int) string {
	s := a.status
	if pk := a.router.PendingKeys(); pk != "" {
		s = "keys: " + pk + " … " + s
	}
	if a.live != nil {
		s = "LIVE " + a.live.Target().Key() + " · leader " + a.leader + " then d detach, n/p switch, ? help · " + s
	} else if s == "" {
		s = "? help · : palette · F4 note · n t/n a/n v new · i live · / search"
	}
	return frame.Truncate(s, w)
}

func (a *App) body(w, h int) string {
	lines := []string{a.switcher(w)}
	bh := max(0, h-2)
	var main string
	p := a.page()
	switch {
	case a.content != nil:
		main = a.contentBody(w, bh)
	case len(p.lst.Items()) == 0 && p.empty != nil:
		main = p.empty(a)
	default:
		lh, dh := a.bodyHeights(p)
		main = p.lst.View()
		if p.detail != nil && dh > 0 {
			main = strings.Join(frame.FitLines(main, w, lh), "\n") + "\n" + strings.Join(frame.FitLines(p.detail(a, w, dh), w, dh), "\n")
		}
	}
	lines = append(lines, frame.FitLines(main, w, bh)...)
	lines = append(lines, a.statusLine(w))
	return strings.Join(lines, "\n")
}

func (a *App) contentBody(w, h int) string {
	c := a.content
	all := strings.Split(c.text, "\n")
	c.scroll = min(c.scroll, max(0, len(all)-1))
	head := frame.Truncate(fmt.Sprintf("read-only · %s · %d/%d lines · esc close", c.title, min(len(all), c.scroll+1), len(all)), w)
	body := all[c.scroll:]
	if len(body) > h-1 {
		body = body[:max(0, h-1)]
	}
	for i := range body {
		body[i] = frame.Truncate(sanitizeLine(body[i]), w)
	}
	return head + "\n" + strings.Join(body, "\n")
}

func (a *App) emptyInventory() string {
	c := a.snap.Connection
	switch c.State {
	case domain.ConnDisconnected:
		return fmt.Sprintf("Server %s unavailable: %s\nPress S to start it (mtm never falls back to another server).", a.d.Client.Server(), c.LastError)
	case domain.ConnConnecting, domain.ConnResynchronizing, domain.ConnUnknown, "":
		return "Connecting to " + a.d.Client.Server() + "… (loading)"
	}
	if a.cur == 0 && (a.active != "" || a.activeNone) {
		return "No panes in project " + a.projectText() + ". Create one: n t terminal · n a agent · n v vim (N for forms)."
	}
	return "No windows on " + a.d.Client.Server() + ". Create one: n t terminal · n a agent · n v vim (N for forms)."
}

func (a *App) paneDetail(w, h int) string {
	p := a.panes()
	var tabs []string
	for i, t := range paneTabs {
		if i == p.tab {
			tabs = append(tabs, "["+t+"]")
		} else {
			tabs = append(tabs, " "+t+" ")
		}
	}
	head := strings.Join(tabs, "")
	bh := max(0, h-1)
	if a.live != nil {
		return frame.Truncate("LIVE ▶ "+a.live.Target().Key(), w) + "\n" + a.live.View(w, bh)
	}
	win, ok := a.selectedWindow()
	if !ok {
		return head + "\n(no selection)"
	}
	switch paneTabs[p.tab] {
	case "Screen":
		if preview.TooSmall(w, bh) {
			return head + "\npreview: too small"
		}
		if a.shot == nil || a.shot.Target.Key() != win.Target.Key() {
			return head + "\n" + preview.FitHeader(fmt.Sprintf("%s/%s/%s/%s • loading", win.Target.Server, hostOf(win.Target.Host), win.Target.Session, win.Target.WindowID), w)
		}
		hdr := preview.FitHeader(preview.Labeled(*a.shot, nil), w)
		lines := a.shot.Rendered
		if len(lines) > bh-1 {
			lines = lines[len(lines)-(bh-1):] // bottom of the detail area: most recent
		}
		return head + "\n" + hdr + "\n" + strings.Join(preview.Crop(lines, w, bh-1), "\n")
	case "Activity":
		return head + "\n" + a.activity
	case "Transcript", "Turns":
		if a.historyTarget != win.Target.Key() {
			return head + "\nloading captured history…"
		}
		return head + "\n" + a.historyText
	}
	return head
}

func (a *App) contextDetail(w, h int) string {
	r, ok := a.page().selected()
	if !ok || r.item == nil {
		return ""
	}
	it := r.item
	src := "source: not recorded"
	if it.Source != nil {
		src = "source: " + it.Source.Key()
	}
	return fmt.Sprintf("project: %s · %s · path: %s\n%s", orNone(it.Project), src, it.Path, sanitizeLine(it.Body))
}
