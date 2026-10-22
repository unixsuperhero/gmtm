package forms

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// PathMode selects the native dialog kind.
type PathMode int

const (
	PickFile PathMode = iota // open an existing file
	PickDir                  // choose a directory
	PickSave                 // choose a new output file name
)

func (m PathMode) String() string {
	switch m {
	case PickDir:
		return "directory"
	case PickSave:
		return "save file"
	}
	return "file"
}

// PickRequest describes one native picker invocation.
type PickRequest struct {
	Mode     PathMode
	Multiple bool
	Title    string
	Start    string
}

// Picker opens a native OS file/directory/save dialog.
type Picker interface {
	Pick(ctx context.Context, req PickRequest) ([]string, error)
}

var (
	ErrPickerCanceled    = errors.New("picker cancelled")
	ErrPickerUnavailable = errors.New("no native picker available")
)

// OSPicker uses osascript on macOS and zenity (then kdialog) on Linux.
type OSPicker struct{}

// NewOSPicker returns the native picker for this platform.
func NewOSPicker() Picker { return OSPicker{} }

// Pick runs the platform dialog; the context deadline bounds it.
func (OSPicker) Pick(ctx context.Context, req PickRequest) ([]string, error) {
	switch runtime.GOOS {
	case "darwin":
		return runDialog(ctx, "osascript", []string{"-e", appleScript(req)}, isAppleCancel)
	case "linux":
		if p, err := exec.LookPath("zenity"); err == nil {
			return runDialog(ctx, p, zenityArgs(req), exitOne)
		}
		if p, err := exec.LookPath("kdialog"); err == nil {
			return runDialog(ctx, p, kdialogArgs(req), exitOne)
		}
		return nil, fmt.Errorf("%w: install zenity or kdialog", ErrPickerUnavailable)
	}
	return nil, fmt.Errorf("%w on %s", ErrPickerUnavailable, runtime.GOOS)
}

func runDialog(ctx context.Context, name string, args []string, canceled func(error, string) bool) ([]string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("picker: %w", ctx.Err())
		}
		if canceled(err, stderr.String()) {
			return nil, ErrPickerCanceled
		}
		return nil, fmt.Errorf("picker %s: %v: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	var paths []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimRight(l, "\r"); strings.TrimSpace(l) != "" {
			paths = append(paths, l)
		}
	}
	if len(paths) == 0 {
		return nil, ErrPickerCanceled
	}
	return paths, nil
}

func isAppleCancel(_ error, stderr string) bool { return strings.Contains(stderr, "-128") }

func exitOne(err error, _ string) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == 1
}

func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func appleScript(req PickRequest) string {
	prompt := ""
	if req.Title != "" {
		prompt = " with prompt " + appleQuote(req.Title)
	}
	switch req.Mode {
	case PickSave:
		return "POSIX path of (choose file name" + prompt + ")"
	case PickDir, PickFile:
		verb := "choose file"
		if req.Mode == PickDir {
			verb = "choose folder"
		}
		if !req.Multiple {
			return "POSIX path of (" + verb + prompt + ")"
		}
		return "set l to (" + verb + prompt + " with multiple selections allowed)\n" +
			"set out to \"\"\nrepeat with f in l\nset out to out & POSIX path of f & linefeed\nend repeat\nreturn out"
	}
	return ""
}

func zenityArgs(req PickRequest) []string {
	a := []string{"--file-selection", "--separator=\n"}
	if req.Title != "" {
		a = append(a, "--title="+req.Title)
	}
	switch req.Mode {
	case PickDir:
		a = append(a, "--directory")
	case PickSave:
		a = append(a, "--save", "--confirm-overwrite")
	}
	if req.Multiple && req.Mode != PickSave {
		a = append(a, "--multiple")
	}
	if req.Start != "" {
		a = append(a, "--filename="+req.Start)
	}
	return a
}

func kdialogArgs(req PickRequest) []string {
	start := req.Start
	if start == "" {
		start = "."
	}
	switch req.Mode {
	case PickDir:
		return []string{"--getexistingdirectory", start}
	case PickSave:
		return []string{"--getsavefilename", start}
	}
	if req.Multiple {
		return []string{"--getopenfilename", start, "--multiple", "--separate-output"}
	}
	return []string{"--getopenfilename", start}
}

// FakePicker returns canned results and records requests; for tests.
type FakePicker struct {
	Paths    []string
	Err      error
	Requests []PickRequest
}

func (p *FakePicker) Pick(_ context.Context, req PickRequest) ([]string, error) {
	p.Requests = append(p.Requests, req)
	return p.Paths, p.Err
}
