package pathpicker

import (
	"bytes"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"
)

// PreviewBytes bounds how much of a file the preview reads.
const PreviewBytes = 64 * 1024

// PreviewLines bounds how many lines a preview keeps.
const PreviewLines = 40

// Preview is the rendered-free content of the preview pane.
type Preview struct {
	Path  string
	Lines []string
	Note  string // "binary file", "directory", errors, "new file"
}

// LoadPreview reads a bounded preview of path: a file's head or a
// directory's listing. It never follows more than PreviewBytes.
func LoadPreview(path string) Preview {
	pv := Preview{Path: path}
	st, err := os.Stat(path)
	if err != nil {
		pv.Note = err.Error()
		return pv
	}
	if st.IsDir() {
		ents, err := os.ReadDir(path)
		if err != nil {
			pv.Note = err.Error()
			return pv
		}
		names := make([]string, 0, len(ents))
		for _, e := range ents {
			n := e.Name()
			if e.IsDir() {
				n += "/"
			}
			names = append(names, Sanitize(n))
		}
		sort.Strings(names)
		pv.Note = "directory"
		if len(names) == 0 {
			pv.Note = "empty directory"
		}
		if len(names) > PreviewLines*5 {
			names = names[:PreviewLines*5]
		}
		pv.Lines = names
		return pv
	}
	if !st.Mode().IsRegular() {
		pv.Note = "special file (" + st.Mode().Type().String() + ")"
		return pv
	}
	f, err := os.Open(path)
	if err != nil {
		pv.Note = err.Error()
		return pv
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, PreviewBytes))
	if err != nil {
		pv.Note = err.Error()
		return pv
	}
	if IsBinary(buf) {
		pv.Note = "binary file"
		return pv
	}
	if len(buf) == 0 {
		pv.Note = "empty file"
		return pv
	}
	for _, l := range strings.Split(string(buf), "\n") {
		if len(pv.Lines) >= PreviewLines {
			break
		}
		pv.Lines = append(pv.Lines, Sanitize(l))
	}
	return pv
}

// IsBinary reports whether a sample looks binary (contains NUL).
func IsBinary(b []byte) bool { return bytes.IndexByte(b, 0) >= 0 }

// Sanitize replaces invalid UTF-8, expands tabs and drops control characters.
func Sanitize(s string) string {
	s = strings.ToValidUTF8(s, "�")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString("    ")
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
