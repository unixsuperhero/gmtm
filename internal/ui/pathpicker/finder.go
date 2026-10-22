package pathpicker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Mode selects what the picker lists and how Enter chooses.
type Mode int

const (
	Files  Mode = iota // existing files
	Dirs               // existing directories
	SaveAs             // a new or existing file name under the base
)

func (m Mode) String() string {
	switch m {
	case Dirs:
		return "directory"
	case SaveAs:
		return "save as"
	}
	return "file"
}

// MaxEntries caps how many candidates one listing keeps.
const MaxEntries = 50000

// ListTimeout bounds one listing.
const ListTimeout = 5 * time.Second

// Finder produces candidate paths relative to base.
type Finder interface {
	List(ctx context.Context, base string, mode Mode) ([]string, error)
}

// NewFinder returns an FdFinder when fd or fdfind is on PATH, else a FindFinder.
func NewFinder() Finder {
	if f, ok := LookupFd(); ok {
		return f
	}
	return FindFinder{}
}

// FdFinder runs fd (or fdfind) unrestricted, excluding node_modules and .git.
type FdFinder struct{ Bin string }

// LookupFd resolves fd, then fdfind, on PATH.
func LookupFd() (FdFinder, bool) {
	for _, n := range []string{"fd", "fdfind"} {
		if p, err := exec.LookPath(n); err == nil {
			return FdFinder{Bin: p}, true
		}
	}
	return FdFinder{}, false
}

// Args are the fd arguments for mode.
func (FdFinder) Args(mode Mode) []string {
	t := "f"
	if mode == Dirs {
		t = "d"
	}
	return []string{"-u", "-E", "node_modules", "-E", ".git", "--color", "never", "--type", t}
}

func (f FdFinder) List(ctx context.Context, base string, mode Mode) ([]string, error) {
	return runLister(ctx, base, f.Bin, f.Args(mode))
}

// FindFinder is the POSIX find fallback with the same exclusions.
type FindFinder struct{}

// Args are the find arguments for mode.
func (FindFinder) Args(mode Mode) []string {
	t := "f"
	if mode == Dirs {
		t = "d"
	}
	return []string{".", "-mindepth", "1", "(", "-name", "node_modules", "-o", "-name", ".git", ")", "-prune", "-o", "-type", t, "-print"}
}

func (f FindFinder) List(ctx context.Context, base string, mode Mode) ([]string, error) {
	return runLister(ctx, base, "find", f.Args(mode))
}

// runLister streams stdout lines, normalised to clean relative paths, up to
// MaxEntries. Partial results are returned together with a timeout error.
func runLister(ctx context.Context, base, bin string, args []string) ([]string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = base
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var items []string
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if p := normalise(sc.Text()); p != "" {
			items = append(items, p)
			if len(items) >= MaxEntries {
				cancel()
				break
			}
		}
	}
	werr := cmd.Wait()
	switch {
	case len(items) >= MaxEntries:
		return items, nil
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return items, fmt.Errorf("listing timed out: %w", context.DeadlineExceeded)
	case werr != nil && len(items) == 0:
		// find exits 1 on unreadable subdirectories but still lists the rest
		return nil, fmt.Errorf("%s: %v: %s", bin, werr, strings.TrimSpace(stderr.String()))
	}
	return items, nil
}

func normalise(l string) string {
	l = strings.TrimRight(l, "\r")
	l = strings.TrimPrefix(l, "./")
	if l != "/" {
		l = strings.TrimSuffix(l, "/")
	}
	if l == "." {
		return ""
	}
	return l
}

// FakeFinder serves canned listings per base and records calls; for tests.
type FakeFinder struct {
	Items map[string][]string
	Err   error
	Calls []string
}

func (f *FakeFinder) List(_ context.Context, base string, _ Mode) ([]string, error) {
	f.Calls = append(f.Calls, base)
	return append([]string(nil), f.Items[base]...), f.Err
}
