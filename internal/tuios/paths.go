package tuios

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// MaxServerNameLen is the daemon's cap on server names.
const MaxServerNameLen = 14

// ValidateServerName enforces the daemon's rule: 1..14 chars of [A-Za-z0-9_-].
// An empty name is refused: there is no default-daemon path.
func ValidateServerName(name string) error {
	if name == "" {
		return errors.New("tuios: server name is required (the default daemon is never used)")
	}
	if len(name) > MaxServerNameLen {
		return fmt.Errorf("tuios: server name %q is longer than %d characters", name, MaxServerNameLen)
	}
	for _, r := range name {
		ok := r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("tuios: server name %q may hold only [A-Za-z0-9_-]", name)
		}
	}
	return nil
}

// SocketBase returns the directory under which named servers keep sockets:
// $XDG_RUNTIME_DIR/tuios, else /run/user/<uid>/tuios when /run/user/<uid> exists,
// else /tmp/tuios-<uid>. getenv and dirExists are injectable for tests.
func SocketBase(getenv func(string) string, uid int, dirExists func(string) bool) string {
	if x := getenv("XDG_RUNTIME_DIR"); x != "" {
		return filepath.Join(x, "tuios")
	}
	run := filepath.Join("/run/user", strconv.Itoa(uid))
	if dirExists(run) {
		return filepath.Join(run, "tuios")
	}
	return filepath.Join("/tmp", "tuios-"+strconv.Itoa(uid))
}

// ServerSocketPath is <socketbase>/servers/<name>/tuios.sock.
func ServerSocketPath(base, name string) string {
	return filepath.Join(base, "servers", name, "tuios.sock")
}

// ResolveSocketPath resolves the socket of a named server from the real environment.
// It reads only XDG_RUNTIME_DIR; TUIOS_SOCKET and pane identity are never consulted.
func ResolveSocketPath(name string) (string, error) {
	if err := ValidateServerName(name); err != nil {
		return "", err
	}
	return ServerSocketPath(SocketBase(os.Getenv, os.Getuid(), isDir), name), nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
