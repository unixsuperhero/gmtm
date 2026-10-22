package forms

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/unixsuperhero/gmtm/internal/ui/pathpicker"
)

// PathMode selects what the fuzzy path picker lists for a Path field.
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

// PickerMode maps a field mode to the pathpicker mode.
func (m PathMode) PickerMode() pathpicker.Mode {
	switch m {
	case PickDir:
		return pathpicker.Dirs
	case PickSave:
		return pathpicker.SaveAs
	}
	return pathpicker.Files
}

// PickRequestMsg asks the orchestrator to open the built-in fuzzy path
// picker (pathpicker) for one local Path field. The result comes back as a
// PickResultMsg carrying the same FormID, FieldID and Gen.
type PickRequestMsg struct {
	FormID   string
	FieldID  string
	Gen      int
	Mode     PathMode
	Multiple bool
	Base     string // absolute starting directory
	Title    string
}

// PickerOptions builds the pathpicker options for this request; the picker's
// ID is "FormID/FieldID" so replies can be routed.
func (r PickRequestMsg) PickerOptions() pathpicker.Options {
	return pathpicker.Options{ID: r.FormID + "/" + r.FieldID, Mode: r.Mode.PickerMode(), Multiple: r.Multiple, Base: r.Base, Title: r.Title}
}

// Result binds picker output to this request. Use ErrPickerCanceled for a cancel.
func (r PickRequestMsg) Result(paths []string, err error) PickResultMsg {
	return PickResultMsg{FormID: r.FormID, FieldID: r.FieldID, Gen: r.Gen, Paths: paths, Err: err}
}

// ErrPickerCanceled reports a dismissed picker; the field value is unchanged.
var ErrPickerCanceled = errors.New("picker cancelled")

// pickBase is the picker's starting directory for a field value: the value
// itself when it is a directory, else its parent when that exists, else home.
func pickBase(value string) string {
	home, _ := os.UserHomeDir()
	v := strings.TrimSpace(value)
	if v == "~" || strings.HasPrefix(v, "~/") {
		v = home + v[1:]
	}
	if v != "" && filepath.IsAbs(v) {
		for _, c := range []string{v, filepath.Dir(v)} {
			if st, err := os.Stat(c); err == nil && st.IsDir() {
				return filepath.Clean(c)
			}
		}
	}
	return home
}
