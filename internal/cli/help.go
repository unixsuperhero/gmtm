package cli

import (
	"fmt"
	"sort"
	"strings"
)

type topic struct{ usage, summary, detail string }

var topics = map[string]topic{
	"tui":     {"tui", "Open the terminal UI (default when no command is given)", "Connects to the isolated TUIOS server selected with --server."},
	"json":    {"json [FILE]", "Explore, search and filter a JSON document", "Reads FILE, or stdin when piped. With neither on a terminal, opens the built-in\nfuzzy file picker (fd/fdfind, else find; base ~): type to filter, enter choose,\ntab into a directory, backspace on empty query goes up, ~ home, /abs re-bases,\nesc then q cancels (exit 1).\nKeys: j/k move, h/l fold, d/u half page, ctrl+f/ctrl+b page, g/G ends,\n/ search (n/N next/prev), t cycle type filter, J/K scroll detail, q quit."},
	"help":    {"help [command]", "Show this help, or help for one command", ""},
	"version": {"version", "Print the version", ""},
	"server":  {"server", "Show the selected server, its socket and whether it answers", "Use --json for machine-readable output."},
	"actions": {"actions", "List every registered action and its keys", "Use --json for machine-readable output."},
}

func commandNames() []string {
	n := make([]string, 0, len(topics))
	for k := range topics {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

func helpText(bin, name string) string {
	var sb strings.Builder
	if t, ok := topics[name]; ok {
		fmt.Fprintf(&sb, "Usage: %s [global options] %s\n\n%s\n", bin, t.usage, t.summary)
		if t.detail != "" {
			fmt.Fprintf(&sb, "\n%s\n", t.detail)
		}
		return sb.String()
	}
	fmt.Fprintf(&sb, "%s - terminal workspace manager\n\nUsage: %s [global options] [command] [args]\n\nCommands:\n", bin, bin)
	order := []string{"tui", "json", "server", "actions", "help", "version"}
	w := 0
	for _, k := range order {
		w = max(w, len(topics[k].usage))
	}
	for _, k := range order {
		fmt.Fprintf(&sb, "  %-*s  %s\n", w, topics[k].usage, topics[k].summary)
	}
	fmt.Fprintf(&sb, "\nGlobal options:\n  %-18s  %s\n  %-18s  %s\n  %-18s  %s\n  %-18s  %s\n", "--server NAME", "TUIOS server to use (default clean-mtm, 1-14 of A-Za-z0-9_-)", "--db PATH", "database file", "--project NAME", "initial active project for the TUI (default no-project)", "--json", "machine-readable output (only where noted)")
	fmt.Fprintf(&sb, "\nAny other first word runs '%s-<word>' from PATH, passing the remaining arguments\nthrough. An exact '%s-<word>' wins; otherwise a unique prefix runs (e.g. '%s dep' runs\n'%s-deploy'); several matches are an error that lists them. Built-in commands always take\nprecedence and are never abbreviated.\n", bin, bin, bin, bin)
	return sb.String()
}
