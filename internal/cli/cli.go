// Package cli parses arguments, prints human-readable help, dispatches
// built-in subcommands first and external `<bin>-<sub>` executables last.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/unixsuperhero/gmtm/internal/actions"
	"github.com/unixsuperhero/gmtm/internal/ui/jsonexplorer"
	"github.com/unixsuperhero/gmtm/internal/ui/pathpicker"
)

// Version is overridden at link time.
var Version = "dev"

// DefaultServer is the isolated TUIOS server mtm owns.
const DefaultServer = "clean-mtm"

// Options are the resolved global options handed to the TUI.
type Options struct {
	Server  string
	DB      string
	Project string // initial active project ("" = no-project)
}

// Hooks set by the orchestrator / tests.
var (
	// RunTUI starts the TUI. Nil means not wired.
	RunTUI func(opts Options) error
	// Actions supplies the registry listed by `mtm actions`.
	Actions func() *actions.Registry = actions.New
	// runExplorer is the JSON explorer runner.
	runExplorer = jsonexplorer.Run
	// pickFile runs the built-in fuzzy path picker; swapped in tests.
	pickFile = fuzzyPick
	// isTTY reports whether a reader is an interactive terminal.
	isTTY = func(r io.Reader) bool {
		f, ok := r.(*os.File)
		if !ok {
			return false
		}
		st, err := f.Stat()
		return err == nil && st.Mode()&os.ModeCharDevice != 0
	}
)

var serverRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,14}$`)

// ScrubbedEnv removes pane-scoped TUIOS variables and pins the selected server.
func ScrubbedEnv(env []string, server string) []string {
	drop := map[string]bool{"TUIOS_SOCKET": true, "TUIOS_SESSION": true, "TUIOS_PANE_ID": true,
		"TUIOS_WINDOW_ID": true, "TUIOS_PANE_TOKEN": true, "TUIOS_PANE_GRANTS": true,
		"TUIOS_SERVER": true, "MTM_SERVER": true}
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[k] {
			out = append(out, kv)
		}
	}
	return append(out, "TUIOS_SERVER="+server, "MTM_SERVER="+server)
}

type global struct {
	server, db, project string
	json                bool
}

// Main is the process entry.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	bin := "mtm"
	if len(args) > 0 && args[0] != "" {
		bin = filepath.Base(args[0])
		args = args[1:]
	}
	g := global{}
	fs := flag.NewFlagSet(bin, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&g.server, "server", DefaultServer, "")
	fs.StringVar(&g.db, "db", "", "")
	fs.StringVar(&g.project, "project", "", "")
	fs.BoolVar(&g.json, "json", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, helpText(bin, ""))
			return 0
		}
		return fail(stderr, bin, "%v (try '%s help')", err, bin)
	}
	if !serverRe.MatchString(g.server) {
		return fail(stderr, bin, "invalid --server %q: use 1-14 characters from A-Z a-z 0-9 _ -", g.server)
	}
	rest := fs.Args()
	sub := "tui"
	if len(rest) > 0 {
		sub, rest = rest[0], rest[1:]
	}
	env := &runEnv{bin: bin, g: g, in: stdin, out: stdout, err: stderr}
	switch sub {
	case "tui":
		return env.tui(rest)
	case "json":
		return env.jsonCmd(rest)
	case "help":
		return env.help(rest)
	case "version":
		return env.version(rest)
	case "server":
		return env.server(rest)
	case "actions":
		return env.actions(rest)
	case "setup":
		return env.setup(rest)
	case "capture":
		return env.capture(rest)
	}
	return env.external(sub, rest)
}

func fail(w io.Writer, bin, f string, a ...any) int {
	fmt.Fprintf(w, "%s: %s\n", bin, fmt.Sprintf(f, a...))
	return 2
}

type runEnv struct {
	bin      string
	g        global
	in       io.Reader
	out, err io.Writer
}

// sub builds a flag set for a subcommand that takes no flags.
func (e *runEnv) noFlags(name string, rest []string, maxPos int, usage string) (pos []string, code int, ok bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(e.out, helpText(e.bin, name))
			return nil, 0, false
		}
		return nil, fail(e.err, e.bin, "%s: %v (try '%s help %s')", name, err, e.bin, name), false
	}
	if fs.NArg() > maxPos {
		return nil, fail(e.err, e.bin, "%s: unexpected argument %q; usage: %s", name, fs.Arg(maxPos), usage), false
	}
	return fs.Args(), 0, true
}

func (e *runEnv) needNoJSON(name string) bool {
	if e.g.json {
		fail(e.err, e.bin, "%s: --json is not supported for this command", name)
		return false
	}
	return true
}

func (e *runEnv) tui(rest []string) int {
	if !e.needNoJSON("tui") {
		return 2
	}
	if _, code, ok := e.noFlags("tui", rest, 0, e.bin+" tui"); !ok {
		return code
	}
	if RunTUI == nil {
		fmt.Fprintln(e.err, "TUI not wired")
		return 1
	}
	if err := RunTUI(Options{Server: e.g.server, DB: e.g.db, Project: e.g.project}); err != nil {
		fmt.Fprintf(e.err, "%s: %v\n", e.bin, err)
		return 1
	}
	return 0
}

func (e *runEnv) version(rest []string) int {
	if !e.needNoJSON("version") {
		return 2
	}
	if _, code, ok := e.noFlags("version", rest, 0, e.bin+" version"); !ok {
		return code
	}
	fmt.Fprintf(e.out, "%s %s (%s/%s)\n", e.bin, Version, runtime.GOOS, runtime.GOARCH)
	return 0
}

func (e *runEnv) help(rest []string) int {
	if !e.needNoJSON("help") {
		return 2
	}
	pos, code, ok := e.noFlags("help", rest, 1, e.bin+" help [command]")
	if !ok {
		return code
	}
	topic := ""
	if len(pos) == 1 {
		topic = pos[0]
		if _, known := topics[topic]; !known {
			return fail(e.err, e.bin, "help: no help for %q (commands: %s)", topic, strings.Join(commandNames(), ", "))
		}
	}
	fmt.Fprint(e.out, helpText(e.bin, topic))
	return 0
}

func (e *runEnv) actions(rest []string) int {
	if _, code, ok := e.noFlags("actions", rest, 0, e.bin+" actions"); !ok {
		return code
	}
	all := Actions().Effective()
	if e.g.json {
		type row struct {
			ID, Label, Description string
			Keys, Modes            []string
			Page, CLI              string
		}
		rows := []row{}
		for _, b := range all {
			a := b.Action
			rows = append(rows, row{a.ID, a.Label(), a.Description, b.Keys, a.Modes, a.Page, a.CLI})
		}
		enc := json.NewEncoder(e.out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rows)
		return 0
	}
	if len(all) == 0 {
		fmt.Fprintln(e.out, "no actions registered")
		return 0
	}
	idW, keyW := 0, 0
	for _, b := range all {
		idW = max(idW, len(b.Action.ID))
		keyW = max(keyW, len(strings.Join(b.Keys, ", ")))
	}
	for _, b := range all {
		fmt.Fprintf(e.out, "%-*s  %-*s  %s\n", idW, b.Action.ID, keyW, strings.Join(b.Keys, ", "), b.Action.Description)
	}
	return 0
}

// SocketPath resolves the TUIOS socket for a server per docs/tuios-protocol-notes.md.
func SocketPath(server string) string {
	uid := os.Getuid()
	var base string
	switch {
	case os.Getenv("XDG_RUNTIME_DIR") != "":
		base = filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "tuios")
	default:
		linux := fmt.Sprintf("/run/user/%d/tuios", uid)
		if st, err := os.Stat(filepath.Dir(linux)); err == nil && st.IsDir() && runtime.GOOS == "linux" {
			base = linux
		} else {
			base = fmt.Sprintf("/tmp/tuios-%d", uid)
		}
	}
	return filepath.Join(base, "servers", server, "tuios.sock")
}

func (e *runEnv) server(rest []string) int {
	if _, code, ok := e.noFlags("server", rest, 0, e.bin+" server"); !ok {
		return code
	}
	path := SocketPath(e.g.server)
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	up := err == nil
	if up {
		conn.Close()
	}
	if e.g.json {
		_ = json.NewEncoder(e.out).Encode(map[string]any{"server": e.g.server, "socket": path, "answering": up})
		return 0
	}
	state := "not answering"
	if up {
		state = "answering"
	}
	fmt.Fprintf(e.out, "server  %s\nsocket  %s\nstatus  %s\n", e.g.server, path, state)
	return 0
}

func (e *runEnv) jsonCmd(rest []string) int {
	if !e.needNoJSON("json") {
		return 2
	}
	pos, code, ok := e.noFlags("json", rest, 1, e.bin+" json [FILE]")
	if !ok {
		return code
	}
	var data []byte
	var err error
	title := "stdin"
	switch {
	case len(pos) == 1:
		title = pos[0]
		data, err = os.ReadFile(pos[0])
	case !isTTY(e.in):
		data, err = io.ReadAll(e.in)
	default:
		var p string
		if p, err = pickFile(); errors.Is(err, pathpicker.ErrCanceled) {
			fmt.Fprintf(e.err, "%s: json: no file chosen (pass FILE or pipe JSON on stdin)\n", e.bin)
			return 1
		}
		if err == nil {
			title = p
			data, err = os.ReadFile(p)
		}
	}
	if err != nil {
		return fail(e.err, e.bin, "json: %v", err)
	}
	if err := runExplorer(data, title); err != nil {
		var pe *jsonexplorer.ParseError
		if errors.As(err, &pe) {
			return fail(e.err, e.bin, "json: %s: %v", title, pe)
		}
		return fail(e.err, e.bin, "json: %v", err)
	}
	return 0
}

// fuzzyPick asks for one local file with the built-in fuzzy path picker.
func fuzzyPick() (string, error) {
	paths, err := pathpicker.Run(pathpicker.Options{Mode: pathpicker.Files, Title: "json: choose a file"})
	if err != nil {
		return "", err
	}
	return paths[0], nil
}

func (e *runEnv) external(sub string, rest []string) int {
	if strings.ContainsAny(sub, "/\\") || strings.HasPrefix(sub, "-") {
		return fail(e.err, e.bin, "unknown command %q (try '%s help')", sub, e.bin)
	}
	path, cands := resolveExternal(e.bin, sub, os.Getenv("PATH"))
	switch {
	case len(cands) > 1:
		return fail(e.err, e.bin, "ambiguous command %q: matches %s (type more of the name)", sub, strings.Join(cands, ", "))
	case path == "":
		return fail(e.err, e.bin, "unknown command %q (no built-in, and no %s-%s* on PATH; try '%s help')", sub, e.bin, sub, e.bin)
	}
	c := exec.Command(path, rest...)
	c.Stdin, c.Stdout, c.Stderr = e.in, e.out, e.err
	c.Env = ScrubbedEnv(os.Environ(), e.g.server)
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		return fail(e.err, e.bin, "%s: %v", filepath.Base(path), err)
	}
	return 0
}

// resolveExternal finds the executable for `bin sub`: an exact <bin>-<sub>
// on PATH wins (first PATH entry first); otherwise a unique <bin>-<sub>*
// prefix match. With several prefix matches it returns their sorted names
// and no path. Built-in names are never abbreviated (Main checks them first).
func resolveExternal(bin, sub, pathEnv string) (path string, ambiguous []string) {
	exact := bin + "-" + sub
	byName := map[string]string{}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range ents {
			n := ent.Name()
			if !strings.HasPrefix(n, exact) {
				continue
			}
			if _, seen := byName[n]; seen {
				continue
			}
			full := filepath.Join(dir, n)
			if st, err := os.Stat(full); err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
				continue
			}
			byName[n] = full
		}
	}
	if p, ok := byName[exact]; ok {
		return p, nil
	}
	if len(byName) == 1 {
		for _, p := range byName {
			return p, nil
		}
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) < 2 {
		names = nil
	}
	return "", names
}
