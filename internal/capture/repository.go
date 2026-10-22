// Package capture persists attributed terminal commands and harness turns.
package capture

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/unixsuperhero/gmtm/internal/domain"
	_ "modernc.org/sqlite"
)

const (
	MaxTextBytes = 1024 * 1024
	MaxListTurns = 500
	MaxListBytes = 8 * 1024 * 1024
)

// Turn identity includes its entire target, harness and conversation, not just ID.
// Source and OutputStatus describe provenance, never inferred screen content.
type Turn struct {
	ID             string `json:"id"`
	Server         string `json:"server"`
	Host           string `json:"host"`
	Session        string `json:"session"`
	Window         string `json:"window"`
	Kind           string `json:"kind"`
	Harness        string `json:"harness"`
	ConversationID string `json:"conversation_id"`
	Prompt         string `json:"prompt"`
	Response       string `json:"response"`
	StartedAt      int64  `json:"started_at"`
	CompletedAt    int64  `json:"completed_at"`
	State          string `json:"state"`
	ExitCode       *int   `json:"exit_code,omitempty"`
	Source         string `json:"source,omitempty"`
	OutputStatus   string `json:"output_status,omitempty"`
	CommandSeq     uint64 `json:"command_seq,omitempty"`
	// RawCommand is authoritative shell-hook input. Prompt remains the daemon's
	// possibly wrapped/redacted reconstruction for command identity correlation.
	RawCommand string `json:"raw_command,omitempty"`
}

func (t Turn) Target() domain.Target {
	return domain.Target{Server: t.Server, Host: t.Host, Session: t.Session, WindowID: t.Window}
}

// DefaultDBPath follows store.DefaultPath, but never falls back to the cwd when
// the home directory cannot be resolved.
func DefaultDBPath() (string, error) {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Abs(filepath.Join(x, "mtm", "mtm.db"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "mtm", "mtm.db"), nil
}

type Repository struct{ db *sql.DB }

func Open(path string) (*Repository, error) {
	var err error
	if path == "" {
		path, err = DefaultDBPath()
		if err != nil {
			return nil, err
		}
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Full commands and prompts may contain secrets; keep the database private.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Chmod(path+suffix, 0600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Set("_txlock", "immediate")
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS capture_turns (
		key TEXT PRIMARY KEY, server TEXT NOT NULL, host TEXT NOT NULL,
		session TEXT NOT NULL, window TEXT NOT NULL, started_at INTEGER NOT NULL,
		data TEXT NOT NULL);
		CREATE INDEX IF NOT EXISTS capture_turns_target ON capture_turns(server,host,session,window,started_at DESC);`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("capture schema: %w", err)
	}
	return &Repository{db: db}, nil
}

func (r *Repository) Close() error { return r.db.Close() }

func validateTarget(t domain.Target) error {
	for _, value := range []string{t.Server, t.Host, t.Session, t.WindowID} {
		if strings.TrimSpace(value) == "" || len(value) > 1024 || strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("capture requires explicit server, host, session and stable window ID")
		}
	}
	return nil
}

func (t Turn) validate() error {
	if err := validateTarget(t.Target()); err != nil {
		return err
	}
	if strings.TrimSpace(t.ID) == "" || len(t.ID) > 1024 {
		return errors.New("capture requires a bounded nonempty ID")
	}
	if t.Kind != "terminal" && t.Kind != "agent" {
		return errors.New("capture kind must be terminal or agent")
	}
	if t.Kind == "agent" && (strings.TrimSpace(t.Harness) == "" || strings.TrimSpace(t.ConversationID) == "") {
		return errors.New("agent capture requires harness and conversation_id")
	}
	if len(t.Harness) > 1024 || len(t.ConversationID) > 4096 || len(t.Prompt) > MaxTextBytes || len(t.Response) > MaxTextBytes || len(t.RawCommand) > MaxTextBytes {
		return errors.New("capture text exceeds size limit")
	}
	if t.Kind != "terminal" && t.RawCommand != "" {
		return errors.New("raw_command is only valid for terminal capture")
	}
	if t.StartedAt <= 0 || (t.CompletedAt != 0 && t.CompletedAt < t.StartedAt) {
		return errors.New("capture timestamps must be Unix milliseconds with completion at or after start")
	}
	switch t.State {
	case "working":
		if t.CompletedAt != 0 {
			return errors.New("working capture cannot have completed_at")
		}
	case "done", "errored", "interrupted":
		if t.CompletedAt == 0 {
			return errors.New("finished capture requires completed_at")
		}
	default:
		return errors.New("capture state must be working, done, errored or interrupted")
	}
	if t.OutputStatus != "" && t.OutputStatus != "captured" && t.OutputStatus != "unavailable" && t.OutputStatus != "truncated" {
		return errors.New("invalid capture output_status")
	}
	if len(t.Source) > 1024 {
		return errors.New("capture source exceeds size limit")
	}
	return nil
}

func turnKey(t Turn) string {
	b, _ := json.Marshal([]string{t.Server, t.Host, t.Session, t.Window, t.Kind, t.Harness, t.ConversationID, t.ID})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Upsert is idempotent. A late start cannot reopen a completed turn. Finished
// harness content is immutable; terminal output may be enriched when it becomes
// available, but a later history-only read cannot erase captured output.
func (r *Repository) Upsert(ctx context.Context, t Turn) error {
	if err := t.validate(); err != nil {
		return err
	}
	if t.Source == "" {
		if t.Kind == "agent" {
			t.Source = "harness-hook"
		} else {
			t.Source = "tuios-osc133"
		}
	}
	key := turnKey(t)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT data FROM capture_turns WHERE key=?`, key).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var old Turn
		if err := json.Unmarshal([]byte(raw), &old); err != nil {
			return err
		}
		if old.StartedAt != t.StartedAt {
			return errors.New("capture identity conflicts with existing start")
		}
		if old.Prompt != t.Prompt {
			switch {
			case old.Kind == "agent" && old.State == "working" && strings.HasPrefix(t.Prompt, old.Prompt):
				// Delivered continuation input may append to an in-flight turn,
				// including on its final save. Never replace earlier input.
			case old.Kind == "agent" && t.State == "working" && strings.HasPrefix(old.Prompt, t.Prompt):
				// An out-of-order start/update must not erase appended input.
				return nil
			default:
				return errors.New("capture identity conflicts with existing prompt")
			}
		}
		if old.State != "working" && t.State == "working" {
			return nil
		}
		if old.RawCommand != "" {
			if t.RawCommand != "" && old.RawCommand != t.RawCommand {
				return errors.New("capture raw command conflicts with existing shell input")
			}
			t.RawCommand = old.RawCommand
		}
		if old.Kind == "terminal" && (old.OutputStatus == "captured" || old.OutputStatus == "truncated") && t.OutputStatus == "unavailable" {
			t.Response, t.OutputStatus = old.Response, old.OutputStatus
		}
		if old.State != "working" {
			if old.State != t.State || old.CompletedAt != t.CompletedAt || !sameExit(old.ExitCode, t.ExitCode) {
				return errors.New("capture completion conflicts with existing turn")
			}
			if old.Kind == "agent" && old.Response != t.Response {
				return errors.New("capture response conflicts with existing turn")
			}
		}
	}
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO capture_turns(key,server,host,session,window,started_at,data) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET data=excluded.data`, key, t.Server, t.Host, t.Session, t.Window, t.StartedAt, string(b))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func sameExit(a, b *int) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

// Page makes bounded history reads explicit to presentation callers.
type Page struct {
	Turns   []Turn `json:"turns"`
	Limited bool   `json:"limited"`
}

// List returns newest first, at most MaxListTurns and MaxListBytes of text.
// Presentation callers should use ListPage to display its Limited flag.
func (r *Repository) List(ctx context.Context, target domain.Target) ([]Turn, error) {
	page, err := r.ListPage(ctx, target)
	return page.Turns, err
}

func (r *Repository) ListPage(ctx context.Context, target domain.Target) (Page, error) {
	page := Page{Turns: make([]Turn, 0)}
	if err := validateTarget(target); err != nil {
		return page, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT data FROM capture_turns WHERE server=? AND host=? AND session=? AND window=? ORDER BY started_at DESC,key DESC LIMIT ?`, target.Server, target.Host, target.Session, target.WindowID, MaxListTurns+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	size := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return page, err
		}
		if len(page.Turns) == MaxListTurns || size+len(raw) > MaxListBytes {
			page.Limited = true
			break
		}
		size += len(raw)
		var t Turn
		if err := json.Unmarshal([]byte(raw), &t); err != nil {
			return page, err
		}
		page.Turns = append(page.Turns, t)
	}
	return page, rows.Err()
}
