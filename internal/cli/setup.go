package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/unixsuperhero/gmtm/internal/capture"
	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/setup"
	"github.com/unixsuperhero/gmtm/internal/tuios"
)

func (e *runEnv) setup(rest []string) int {
	action := "install"
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		action, rest = rest[0], rest[1:]
	}
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var home, binary string
	fs.StringVar(&home, "home", "", "isolated setup home")
	fs.StringVar(&binary, "binary", "", "capture executable")
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(e.out, helpText(e.bin, "setup"))
			return 0
		}
		return fail(e.err, e.bin, "setup: %v", err)
	}
	pos := fs.Args()
	// Also permit setup --home DIR status, matching the default-install shape.
	if len(pos) > 0 && action == "install" {
		switch pos[0] {
		case "install", "status", "uninstall", "edit":
			action, pos = pos[0], pos[1:]
		}
	}
	if action != "install" && action != "status" && action != "uninstall" && action != "edit" {
		return fail(e.err, e.bin, "setup: unknown action %q (choose install, status, uninstall, edit)", action)
	}
	if len(pos) > 1 || (action != "edit" && len(pos) != 0) {
		return fail(e.err, e.bin, "setup: unexpected arguments; try '%s help setup'", e.bin)
	}
	if action == "edit" && !e.needNoJSON("setup edit") {
		return 2
	}
	m, err := setup.New(setup.Options{Server: e.g.server, DB: e.g.db, Binary: binary, Home: home})
	if err != nil {
		return fail(e.err, e.bin, "setup: %v", err)
	}
	if action == "edit" {
		id := "omp"
		if len(pos) > 0 {
			id = pos[0]
		}
		path, err := m.EditPath(id)
		if err != nil {
			return fail(e.err, e.bin, "setup edit: %v", err)
		}
		editor := os.Getenv("VISUAL")
		if editor == "" {
			editor = os.Getenv("EDITOR")
		}
		if editor == "" {
			editor = "vi"
		}
		// The configured editor may contain arguments; the owned path remains a separate argv.
		cmd := exec.Command("sh", "-c", "exec "+editor+` "$@"`, "gmtm-setup-editor", path)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = e.in, e.out, e.err
		if err := cmd.Run(); err != nil {
			return fail(e.err, e.bin, "setup edit: %v", err)
		}
		return 0
	}
	var items []setup.Item
	switch action {
	case "install":
		items, err = m.Install()
	case "uninstall":
		items, err = m.Uninstall()
	default:
		items, err = m.Status()
	}
	if err != nil {
		return fail(e.err, e.bin, "setup %s: %v", action, err)
	}
	if e.g.json {
		if err := json.NewEncoder(e.out).Encode(items); err != nil {
			return fail(e.err, e.bin, "%v", err)
		}
	} else {
		for _, item := range items {
			fmt.Fprintf(e.out, "%s: %s\n  %s\n  %s\n", item.ID, item.Status, item.Path, item.Detail)
		}
	}
	return 0
}

func (e *runEnv) capture(rest []string) int {
	if len(rest) == 0 || rest[0] == "--help" || rest[0] == "-h" {
		fmt.Fprint(e.out, helpText(e.bin, "capture"))
		if len(rest) == 0 {
			return 2
		}
		return 0
	}
	action, rest := rest[0], rest[1:]
	if action != "record" && action != "sync" {
		return fail(e.err, e.bin, "capture: unknown action %q (choose record or sync)", action)
	}
	fs := flag.NewFlagSet("capture "+action, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var session, window, host, command string
	if action == "sync" {
		fs.StringVar(&session, "session", "", "session name")
		fs.StringVar(&window, "window", "", "stable window id")
		fs.StringVar(&host, "host", domain.HostLocal, "host name")
		fs.StringVar(&command, "command", "", "exact command supplied by the shell hook")
	}
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(e.out, helpText(e.bin, "capture"))
			return 0
		}
		return fail(e.err, e.bin, "capture %s: %v", action, err)
	}
	if fs.NArg() != 0 {
		return fail(e.err, e.bin, "capture %s: unexpected argument %q", action, fs.Arg(0))
	}
	var turn capture.Turn
	if action == "record" {
		decoder := json.NewDecoder(e.in)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&turn); err != nil {
			return fail(e.err, e.bin, "capture record: invalid Turn JSON: %v", err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return fail(e.err, e.bin, "capture record: expected exactly one JSON object")
		}
		if turn.Server != e.g.server {
			return fail(e.err, e.bin, "capture record: record server %q does not match selected --server %q", turn.Server, e.g.server)
		}
	} else if session == "" || window == "" || host == "" {
		return fail(e.err, e.bin, "capture sync requires --session and --window, with a nonempty --host")
	}
	db := e.g.db
	if db == "" {
		var err error
		db, err = capture.DefaultDBPath()
		if err != nil {
			return fail(e.err, e.bin, "capture: %v", err)
		}
	}
	repo, err := capture.Open(db)
	if err != nil {
		return fail(e.err, e.bin, "capture: %v", err)
	}
	defer repo.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if action == "record" {
		if err := repo.Upsert(ctx, turn); err != nil {
			return fail(e.err, e.bin, "capture record: %v", err)
		}
		if e.g.json {
			if err := json.NewEncoder(e.out).Encode(turn); err != nil {
				return fail(e.err, e.bin, "capture record: %v", err)
			}
		}
		return 0
	}
	client, err := tuios.New(e.g.server)
	if err != nil {
		return fail(e.err, e.bin, "capture sync: %v", err)
	}
	if closer, ok := client.(io.Closer); ok {
		defer closer.Close()
	}
	service := capture.NewService(client, repo)
	target := domain.Target{Server: e.g.server, Host: host, Session: session, WindowID: window}
	var status capture.Status
	if command != "" {
		status, err = service.SyncAfterCommand(ctx, target, command)
	} else {
		status, err = service.SyncAfterMark(ctx, target)
	}
	if err != nil {
		return fail(e.err, e.bin, "capture sync: %v", err)
	}
	if !status.Supported {
		return fail(e.err, e.bin, "capture sync: %s", status.Detail)
	}
	if e.g.json {
		if err := json.NewEncoder(e.out).Encode(status); err != nil {
			return fail(e.err, e.bin, "capture sync: %v", err)
		}
	}
	return 0
}
