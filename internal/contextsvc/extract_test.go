package contextsvc

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestExtractURLs(t *testing.T) {
	text := `See (https://example.test/search?q=a/b&lang=en#result),
https://example.test/wiki/Function_(mathematics).
<https://example.test/?next=/docs/README.md&ok=true#section>
[remote docs](https://example.test/guide_(advanced)?a=1&b=2#top "Guide")
https://example.test/search?q=why?&finish=yes!#part?
https://example.test/search?q=a/b&lang=en#result`
	want := []Reference{
		{Kind: "url", Path: "https://example.test/search?q=a/b&lang=en#result"},
		{Kind: "url", Path: "https://example.test/wiki/Function_(mathematics)"},
		{Kind: "url", Path: "https://example.test/?next=/docs/README.md&ok=true#section"},
		{Kind: "url", Path: "https://example.test/guide_(advanced)?a=1&b=2#top"},
		{Kind: "url", Path: "https://example.test/search?q=why?&finish=yes!#part?"},
	}
	if got := Extract(text, "/remote/project", false); !reflect.DeepEqual(got, want) {
		t.Fatalf("Extract() = %#v, want %#v", got, want)
	}
}

func TestExtractLocalPaths(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"src", "docs", "directory.md"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"README.md", "Makefile", "src/main.go", "docs/设计 notes.MARKDOWN", "report (final).txt"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	text := "README.md, src/main.go:12:3; src/main.go(12,3) docs directory.md Makefile " +
		"`docs/设计 notes.MARKDOWN` \"report (final).txt\" " +
		"./generated/new.go ../planned.md ./future/ " +
		"\x1b[32msrc/main.go\x1b[0m"
	want := []Reference{
		{Kind: "doc", Path: filepath.Join(root, "README.md")},
		{Kind: "file", Path: filepath.Join(root, "src/main.go")},
		{Kind: "dir", Path: filepath.Join(root, "docs")},
		{Kind: "dir", Path: filepath.Join(root, "directory.md")},
		{Kind: "file", Path: filepath.Join(root, "Makefile")},
		{Kind: "doc", Path: filepath.Join(root, "docs/设计 notes.MARKDOWN")},
		{Kind: "file", Path: filepath.Join(root, "report (final).txt")},
		{Kind: "file", Path: filepath.Join(root, "generated/new.go")},
		{Kind: "doc", Path: filepath.Join(root, "../planned.md")},
		{Kind: "dir", Path: filepath.Join(root, "future")},
	}
	if got := Extract(text, root, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("Extract() = %#v, want %#v", got, want)
	}
}

func TestExtractMarkdownPaths(t *testing.T) {
	text := "[guide](docs/User Guide (draft).md) " +
		"[angle](<docs/设计 notes.MARKDOWN> \"Title\") " +
		"[source](src/main.go:42 \"Source file\") " +
		"[escaped](docs/Guide\\(draft\\).md) " +
		"[directory](./docs/) " +
		"[fragment](README.md#setup) [encoded](docs/User%20Guide%20(draft).md) " +
		"'docs/User Guide (draft).md'"
	want := []Reference{
		{Kind: "doc", Path: "/workspace/docs/User Guide (draft).md"},
		{Kind: "doc", Path: "/workspace/docs/设计 notes.MARKDOWN"},
		{Kind: "file", Path: "/workspace/src/main.go"},
		{Kind: "doc", Path: "/workspace/docs/Guide(draft).md"},
		{Kind: "dir", Path: "/workspace/docs"},
		{Kind: "doc", Path: "/workspace/README.md"},
	}
	if got := Extract(text, "/workspace", false); !reflect.DeepEqual(got, want) {
		t.Fatalf("Extract() = %#v, want %#v", got, want)
	}
}

func TestExtractRemoteIsolation(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(root, "actually-directory.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ambiguous"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	text := "actually-directory.md ambiguous ~/private/notes.md ./new.go /remote/logs/"
	want := []Reference{
		{Kind: "doc", Path: filepath.Join(root, "actually-directory.md")},
		{Kind: "doc", Path: "~/private/notes.md"},
		{Kind: "file", Path: filepath.Join(root, "new.go")},
		{Kind: "dir", Path: "/remote/logs"},
	}
	if got := Extract(text, root, false); !reflect.DeepEqual(got, want) {
		t.Fatalf("Extract() = %#v, want %#v", got, want)
	}
	local := Extract("~/private/notes.md", root, true)
	localWant := []Reference{{Kind: "doc", Path: filepath.Join(home, "private/notes.md")}}
	if !reflect.DeepEqual(local, localWant) {
		t.Fatalf("local home = %#v, want %#v", local, localWant)
	}
}

func TestExtractDeduplicatesResolvedPathsInOrder(t *testing.T) {
	root := t.TempDir()
	text := "./README.md README.md " + filepath.Join(root, "README.md") +
		" https://example.test/ ./src/main.go src/../README.md https://example.test/"
	want := []Reference{
		{Kind: "doc", Path: filepath.Join(root, "README.md")},
		{Kind: "url", Path: "https://example.test/"},
		{Kind: "file", Path: filepath.Join(root, "src/main.go")},
	}
	if got := Extract(text, root, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("Extract() = %#v, want %#v", got, want)
	}
}

func TestExtractRejectsProseAndOperators(t *testing.T) {
	text := "Read the output and/or input/output, then compute a/b or a / b. " +
		"Use x *= y; foo(bar) items[0] 1.25 v1.2.3 e.g. i.e. " +
		"'ordinary quoted prose' `some code here` [section](#intro) " +
		"ftp://example.test/path/file.md"
	for _, local := range []bool{false, true} {
		if got := Extract(text, t.TempDir(), local); len(got) != 0 {
			t.Fatalf("Extract(local=%v) = %#v, want no references", local, got)
		}
	}
}

func TestExtractWithoutCWD(t *testing.T) {
	want := []Reference{{Kind: "doc", Path: "README.md"}}
	if got := Extract("README.md", "", true); !reflect.DeepEqual(got, want) {
		t.Fatalf("Extract() = %#v, want %#v", got, want)
	}
}
