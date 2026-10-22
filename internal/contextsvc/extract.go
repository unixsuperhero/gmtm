package contextsvc

import (
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Reference is a context artifact discovered in a pane's captured text.
type Reference struct {
	Kind string
	Path string
}

var referenceLocation = regexp.MustCompile(`(?::[0-9]+(?::[0-9]+)?|\([0-9]+(?:,[0-9]+)?\))$`)
var markdownEscapes = strings.NewReplacer(`\(`, "(", `\)`, ")", `\ `, " ")

// Extract discovers references without reading their contents. Only local paths
// are statted, and relative paths are resolved against the source pane's cwd.
// Missing explicit paths remain useful references (for example, planned files).
func Extract(text, cwd string, local bool) []Reference {
	text = ansi.Strip(text)
	var refs []Reference
	seen := make(map[Reference]bool)
	candidates := make(map[string]bool)
	add := func(value string, explicit bool) {
		key := value
		if explicit {
			key = "\x00" + key
		}
		if candidates[key] {
			return
		}
		candidates[key] = true
		ref, ok := extractReference(value, cwd, local, explicit)
		if ok && !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if unicode.IsSpace(r) {
			i += size
			continue
		}
		if text[i] == '[' {
			if target, end, ok := markdownTarget(text, i); ok {
				add(target, true)
				i = end
				continue
			}
		}
		if strings.ContainsRune("\"'`", r) {
			if end := strings.IndexByte(text[i+size:], byte(r)); end >= 0 {
				end += i + size
				value := text[i+size : end]
				if !strings.ContainsAny(value, "\r\n") {
					add(value, true)
					i = end + size
					continue
				}
			}
			i += size
			continue
		}
		end := i
		for end < len(text) {
			r, size = utf8.DecodeRuneInString(text[end:])
			if unicode.IsSpace(r) || strings.ContainsRune("\"'`<>", r) {
				break
			}
			end += size
		}
		if end == i {
			i += size
			continue
		}
		add(text[i:end], false)
		i = end
	}
	return refs
}

// markdownTarget accepts balanced parentheses and angle-delimited destinations,
// including the space-containing paths commonly printed by coding agents.
func markdownTarget(text string, start int) (string, int, bool) {
	label := strings.Index(text[start:], "](")
	if label < 0 || strings.ContainsAny(text[start:start+label], "\r\n") {
		return "", start, false
	}
	begin := start + label + 2
	depth := 1
	angle := false
	for i := begin; i < len(text); i++ {
		if text[i] == '\n' || text[i] == '\r' {
			break
		}
		if text[i] == '\\' && i+1 < len(text) {
			i++
			continue
		}
		if text[i] == '<' {
			angle = true
		} else if text[i] == '>' {
			angle = false
		} else if !angle {
			switch text[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					target := strings.TrimSpace(text[begin:i])
					if strings.HasPrefix(target, "<") {
						if end := strings.IndexByte(target, '>'); end >= 0 {
							target = target[1:end]
						}
					} else {
						for _, quote := range []string{" \"", " '"} {
							if title := strings.Index(target, quote); title >= 0 {
								target = target[:title]
							}
						}
					}
					target = markdownEscapes.Replace(target)
					if parsed, err := url.Parse(target); err == nil && parsed.Scheme == "" && parsed.Host == "" && parsed.Path != "" {
						target = parsed.Path
					}
					return target, i + 1, target != ""
				}
			}
		}
	}
	return "", start, false
}

func extractReference(value, cwd string, local, explicit bool) (Reference, bool) {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	// Consume URLs as a whole, never interpreting their slash-separated pieces
	// as files. Prefix punctuation can surround a URL in ordinary prose.
	for _, scheme := range []string{"https://", "http://"} {
		if start := strings.Index(lower, scheme); start >= 0 {
			candidate := trimReferenceEnd(value[start:])
			u, err := url.Parse(candidate)
			if err == nil && u.Hostname() != "" && !strings.ContainsAny(candidate, "\r\n\t ") {
				return Reference{Kind: "url", Path: candidate}, true
			}
			return Reference{}, false
		}
	}
	if strings.Contains(value, "://") {
		return Reference{}, false
	}
	if !explicit {
		value = strings.TrimLeft(value, "([{<\"'`")
		value = trimReferenceEnd(value)
	}
	value = referenceLocation.ReplaceAllString(value, "")
	if value == "" || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "\r\n\t:=|&*<>!?;\x00") {
		return Reference{}, false
	}
	if !explicit && (value == "." || value == ".." || value == "/") {
		return Reference{}, false
	}
	if !explicit && strings.ContainsAny(value, "(){}[]") {
		// Balanced parentheses inside a filename are allowed; operators and
		// indexing expressions without a filename extension are not.
		if !filenameExtension(value) {
			return Reference{}, false
		}
	}
	dirHint := strings.HasSuffix(value, "/") || value == "." || value == ".."
	pathHint := strings.HasPrefix(value, "/") || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") || strings.HasPrefix(value, "~/") || dirHint
	fileHint := filenameExtension(value) || strings.HasPrefix(path.Base(value), ".")
	// A slash alone is not evidence that prose such as "and/or" or an
	// expression such as "a/b" names a file. Existing local paths still win.
	lexical := pathHint || fileHint || explicit && strings.Contains(value, "/") || strings.Count(value, "/") > 1
	resolved := value
	if local {
		if strings.HasPrefix(resolved, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return Reference{}, false
			}
			resolved = filepath.Join(home, resolved[2:])
		} else if !filepath.IsAbs(resolved) && cwd != "" {
			resolved = filepath.Join(cwd, resolved)
		}
		resolved = filepath.Clean(resolved)
		// Without an absolute source cwd, do not accidentally inspect the
		// application's own working directory for a pane's relative path.
		if filepath.IsAbs(resolved) {
			if info, err := os.Stat(resolved); err == nil {
				if info.IsDir() {
					return Reference{Kind: "dir", Path: resolved}, true
				}
				lexical = true
			}
		}
	} else {
		if !path.IsAbs(resolved) && !strings.HasPrefix(resolved, "~/") && cwd != "" {
			resolved = path.Join(cwd, resolved)
		} else {
			resolved = path.Clean(resolved)
		}
	}
	if !lexical {
		return Reference{}, false
	}
	kind := "file"
	if dirHint {
		kind = "dir"
	} else if ext := strings.ToLower(path.Ext(resolved)); ext == ".md" || ext == ".markdown" {
		kind = "doc"
	}
	return Reference{Kind: kind, Path: resolved}, true
}

func filenameExtension(value string) bool {
	base := path.Base(value)
	if base == "e.g" || base == "i.e" {
		return false
	}
	ext := strings.TrimPrefix(path.Ext(base), ".")
	if ext == "" || len(ext) > 16 {
		return false
	}
	for i, r := range ext {
		if !unicode.IsLetter(r) && (i == 0 || !unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

func trimReferenceEnd(value string) string {
	value = strings.TrimRight(value, ".,;:\"'`>")
	for len(value) > 0 {
		last := value[len(value)-1]
		open := byte(0)
		switch last {
		case ')':
			open = '('
		case ']':
			open = '['
		case '}':
			open = '{'
		}
		if open == 0 || strings.Count(value, string(last)) <= strings.Count(value, string(open)) {
			break
		}
		value = strings.TrimRight(value[:len(value)-1], ".,;:")
	}
	return value
}
