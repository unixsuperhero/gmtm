package forms

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Kind is a field's interaction/value behaviour.
type Kind int

const (
	Text        Kind = iota // single-line string
	Multiline               // multi-line string
	Number                  // float64
	Bool                    // bool
	Choice                  // authoritative closed enum: unmatched input is invalid
	MultiSelect             // several values from a closed set
	Extensible              // user-extensible: unmatched nonempty text is staged as new
	Path                    // filesystem path (local: fuzzy picker + completion; remote: host-explicit)
	Linked                  // closed child choice whose options carry Parent (see Field.ParentID)
)

func (k Kind) String() string {
	return [...]string{"text", "multiline", "number", "bool", "choice", "multi", "extensible", "path", "linked"}[k]
}

func (k Kind) choiceLike() bool {
	return k == Choice || k == MultiSelect || k == Extensible || k == Linked
}

// PathSpec configures a Path field. Host "" or "local" means the local
// filesystem; any other host is remote: no local picker, completion or cwd.
type PathSpec struct {
	Mode     PathMode
	Multiple bool
	Host     string
}

// Local reports whether the path lives on this machine.
func (p PathSpec) Local() bool { return p.Host == "" || p.Host == "local" }

// Completer supplies asynchronous options for a field. It runs inside a
// tea.Cmd; its reply is bound to the field ID and edit generation.
type Completer func(ctx context.Context, query string) ([]Option, error)

// Field declares one form field. IDs are stable; the form assigns short unique
// lowercase selection codes displayed beside each field.
type Field struct {
	ID       string
	Label    string
	Hint     string
	Kind     Kind
	Required bool
	// Default: string (text-like, choice value), []string (multi), bool, float64.
	Default any
	Options []Option
	// NoValue is an explicit "no value" choice (e.g. no-project). Choosing it
	// satisfies Required and is reported as None, distinct from unanswered.
	NoValue *Option
	// Multi makes an Extensible field hold several values (tags).
	Multi bool
	// ParentID names the parent field of a Linked child.
	ParentID string
	Path     PathSpec
	Complete Completer
	Validate func(v any) error
}

func (f Field) multi() bool {
	return f.Kind == MultiSelect || (f.Kind == Extensible && f.Multi) || (f.Kind == Path && f.Path.Multiple)
}

// None is reported in SubmitMsg.Values for a field set to its explicit
// no-value choice; Value is that choice's value (e.g. "no-project").
type None struct{ Value string }

// localPathOptions lists filesystem entries completing text. Local only.
func localPathOptions(text string, mode PathMode) []Option {
	if text == "" {
		return nil
	}
	exp := text
	if strings.HasPrefix(exp, "~") {
		if h, err := os.UserHomeDir(); err == nil {
			exp = h + exp[1:]
		}
	}
	dir, base := filepath.Dir(exp), filepath.Base(exp)
	if strings.HasSuffix(exp, "/") {
		dir, base = exp, ""
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	prefix := text[:len(text)-len(base)]
	if strings.HasSuffix(exp, "/") {
		prefix = text
	}
	var out []Option
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), base) || (base == "" && strings.HasPrefix(e.Name(), ".")) {
			continue
		}
		if mode == PickDir && !e.IsDir() {
			continue
		}
		v := prefix + e.Name()
		if e.IsDir() {
			v += "/"
		}
		out = append(out, Option{Value: v})
		if len(out) >= 50 {
			break
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Value < out[b].Value })
	return out
}
