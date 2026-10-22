// Package setup installs server-scoped capture integrations without replacing user settings.
package setup

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/unixsuperhero/gmtm/internal/capture"
	"github.com/unixsuperhero/gmtm/internal/tuios"
)

//go:embed omp.ts
var ompSource string

//go:embed shell.bash
var bashSource string

//go:embed shell.zsh
var zshSource string

type Options struct {
	Server string
	DB     string
	Binary string
	Home   string
}

type Item struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type managedFile struct {
	id, path, source, marker, rc, detail string
}

type Manager struct{ files []managedFile }

func New(opts Options) (*Manager, error) {
	if opts.Server != tuios.DefaultDaemon && !regexp.MustCompile(`^[A-Za-z0-9_-]{1,14}$`).MatchString(opts.Server) {
		return nil, fmt.Errorf("setup requires @default or a named TUIOS server (1-14 of A-Za-z0-9_-)")
	}
	if opts.Home == "" {
		opts.Home = os.Getenv("GMTM_SETUP_HOME")
	}
	isolated := opts.Home != ""
	var err error
	if opts.Home == "" {
		opts.Home, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	opts.Home, err = filepath.Abs(opts.Home)
	if err != nil {
		return nil, err
	}
	if opts.Binary == "" {
		opts.Binary, err = os.Executable()
		if err != nil {
			return nil, err
		}
	}
	opts.Binary, err = filepath.Abs(opts.Binary)
	if err != nil {
		return nil, err
	}
	if opts.DB == "" {
		opts.DB, err = capture.DefaultDBPath()
		if err != nil {
			return nil, err
		}
	}
	opts.DB, err = filepath.Abs(opts.DB)
	if err != nil {
		return nil, err
	}
	tuiosPath, err := exec.LookPath("tuios")
	if err != nil {
		// Inspection/uninstall remain usable without TUIOS installed. Install checks it.
		tuiosPath = "tuios"
	} else {
		tuiosPath, err = filepath.Abs(tuiosPath)
		if err != nil {
			return nil, err
		}
	}
	agent := filepath.Join(opts.Home, ".omp", "agent")
	zshHome := opts.Home
	if !isolated {
		if config := os.Getenv("PI_CONFIG_DIR"); config != "" {
			if !filepath.IsAbs(config) {
				config = filepath.Join(opts.Home, config)
			}
			agent = filepath.Join(config, "agent")
		}
		if configured := os.Getenv("PI_CODING_AGENT_DIR"); configured != "" {
			agent = configured
		}
		if configured := os.Getenv("ZDOTDIR"); configured != "" {
			zshHome = configured
		}
	}
	agent, err = filepath.Abs(agent)
	if err != nil {
		return nil, err
	}
	zshHome, err = filepath.Abs(zshHome)
	if err != nil {
		return nil, err
	}
	daemonServer := opts.Server
	if daemonServer == tuios.DefaultDaemon {
		daemonServer = ""
	}
	config, err := json.Marshal(map[string]string{"bin": opts.Binary, "db": opts.DB, "server": opts.Server, "tuios": tuiosPath})
	if err != nil {
		return nil, err
	}
	omp := strings.Replace(ompSource, `const capture = { bin: "", db: "", server: "", tuios: "" };`, "const capture = "+string(config)+";", 1)
	replacer := strings.NewReplacer("__SERVER__", shellQuote(opts.Server), "__DAEMON_SERVER__", shellQuote(daemonServer), "__BINARY__", shellQuote(opts.Binary), "__DB__", shellQuote(opts.DB))
	root := filepath.Join(opts.Home, ".config", "gmtm", "setup", opts.Server)
	m := &Manager{files: []managedFile{
		{id: "omp", path: filepath.Join(agent, "extensions", "gmtm-omp-"+opts.Server+".ts"), source: omp, marker: "// GMTM_SETUP_ID=omp\n", detail: "OMP full prompts/responses, errors and interruptions; restart OMP. Other agent harnesses are not supported."},
		{id: "bash", path: filepath.Join(root, "shell.bash"), source: replacer.Replace(bashSource), marker: "# GMTM_SETUP_ID=bash\n", rc: filepath.Join(opts.Home, ".bashrc"), detail: "Bash 4.4+ OSC 133 and persistent command/output capture; open a new shell. Bash 3.2 is unsupported."},
		{id: "zsh", path: filepath.Join(root, "shell.zsh"), source: replacer.Replace(zshSource), marker: "# GMTM_SETUP_ID=zsh\n", rc: filepath.Join(zshHome, ".zshrc"), detail: "Zsh OSC 133 and persistent command/output capture; open a new shell. Fish/other shells have no installed persistence hook."},
	}}
	return m, nil
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func (f managedFile) sourceLine() string {
	return "[ ! -r " + shellQuote(f.path) + " ] || . " + shellQuote(f.path) + " # GMTM_SETUP_SOURCE=" + f.id
}

// readRegular refuses symlinks so setup cannot overwrite or remove an unrelated target.
func readRegular(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, true, fmt.Errorf("refusing non-regular setup path %s", path)
	}
	data, err := os.ReadFile(path)
	return data, true, err
}

func (m *Manager) Status() ([]Item, error) {
	items := make([]Item, 0, len(m.files))
	for _, f := range m.files {
		item := Item{ID: f.id, Path: f.path, Status: "missing", Detail: f.detail}
		data, exists, err := readRegular(f.path)
		if err != nil {
			item.Status = "conflict"
			item.Detail += " " + err.Error()
		} else if exists {
			switch {
			case bytes.Equal(data, []byte(f.source)):
				item.Status = "installed"
			case bytes.HasPrefix(data, []byte(f.marker)):
				item.Status = "modified"
			default:
				item.Status = "conflict"
			}
		}
		if f.rc != "" {
			rc, _, err := readRegular(f.rc)
			if err != nil {
				item.Status = "conflict"
				item.Detail += " " + err.Error()
			} else if item.Status == "installed" && !hasLine(rc, f.sourceLine()) {
				item.Status = "unlinked"
			}
			item.Detail += " Source: " + f.rc
		}
		items = append(items, item)
	}
	return items, nil
}

func hasLine(data []byte, line string) bool {
	for _, current := range strings.Split(string(data), "\n") {
		if current == line {
			return true
		}
	}
	return false
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	mode := os.FileMode(0600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".gmtm-setup-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (m *Manager) Install() ([]Item, error) {
	if _, err := exec.LookPath("tuios"); err != nil {
		return nil, fmt.Errorf("setup requires tuios on PATH: %w", err)
	}
	// Preflight every path before writing any component.
	for _, f := range m.files {
		data, exists, err := readRegular(f.path)
		if err != nil {
			return nil, err
		}
		if exists && !bytes.Equal(data, []byte(f.source)) {
			return nil, fmt.Errorf("refusing to overwrite existing or modified setup file %s; inspect with setup edit, then uninstall before reinstalling", f.path)
		}
		if f.rc != "" {
			if _, _, err := readRegular(f.rc); err != nil {
				return nil, err
			}
		}
	}
	for _, f := range m.files {
		data, exists, err := readRegular(f.path)
		if err != nil {
			return nil, err
		}
		if !exists || !bytes.Equal(data, []byte(f.source)) {
			if err := writeFile(f.path, []byte(f.source)); err != nil {
				return nil, err
			}
		}
		if f.rc != "" {
			rc, _, err := readRegular(f.rc)
			if err != nil {
				return nil, err
			}
			if !hasLine(rc, f.sourceLine()) {
				// The source line's leading newline is owned too, allowing byte-exact reversal.
				rc = append(rc, []byte("\n"+f.sourceLine()+"\n")...)
				if err := writeFile(f.rc, rc); err != nil {
					return nil, err
				}
			}
		}
	}
	return m.Status()
}

func (m *Manager) Uninstall() ([]Item, error) {
	for _, f := range m.files {
		data, exists, err := readRegular(f.path)
		if err != nil {
			return nil, err
		}
		if exists && !bytes.HasPrefix(data, []byte(f.marker)) {
			return nil, fmt.Errorf("refusing to remove unowned file %s", f.path)
		}
		if f.rc != "" {
			if _, _, err := readRegular(f.rc); err != nil {
				return nil, err
			}
		}
	}
	for _, f := range m.files {
		if f.rc != "" {
			rc, exists, err := readRegular(f.rc)
			if err != nil {
				return nil, err
			}
			clean := bytes.ReplaceAll(rc, []byte("\n"+f.sourceLine()+"\n"), nil)
			if bytes.HasPrefix(clean, []byte(f.sourceLine()+"\n")) {
				clean = clean[len(f.sourceLine())+1:]
			}
			if exists && !bytes.Equal(clean, rc) {
				if err := writeFile(f.rc, clean); err != nil {
					return nil, err
				}
			}
		}
		if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return m.Status()
}

// EditPath returns only an installed owned file, never a user's rc or another extension.
func (m *Manager) EditPath(id string) (string, error) {
	for _, f := range m.files {
		if f.id != id {
			continue
		}
		data, exists, err := readRegular(f.path)
		if err != nil {
			return "", err
		}
		if !exists {
			return "", fmt.Errorf("%s is not installed; run setup install first", id)
		}
		if !bytes.HasPrefix(data, []byte(f.marker)) {
			return "", fmt.Errorf("refusing to edit unowned file %s", f.path)
		}
		return f.path, nil
	}
	return "", fmt.Errorf("unknown setup component %q (choose omp, bash, zsh)", id)
}
