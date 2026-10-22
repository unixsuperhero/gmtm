package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/unixsuperhero/gmtm/internal/domain"
	_ "modernc.org/sqlite"
)

// migrations is the ordered schema history; index i is version i+1.
// Never edit an applied entry; append a new one.
var migrations = []string{
	// v1: base schema
	`CREATE TABLE projects(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		status TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE project_tags(
		project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		tag TEXT NOT NULL,
		PRIMARY KEY(project_id, tag)
	);
	CREATE TABLE context_items(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		kind TEXT NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		path TEXT NOT NULL DEFAULT '',
		body TEXT NOT NULL DEFAULT '',
		project TEXT NOT NULL DEFAULT '',
		source_server TEXT NOT NULL DEFAULT '',
		source_host TEXT NOT NULL DEFAULT '',
		source_session TEXT NOT NULL DEFAULT '',
		source_window TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE context_tags(
		item_id INTEGER NOT NULL REFERENCES context_items(id) ON DELETE CASCADE,
		tag TEXT NOT NULL,
		PRIMARY KEY(item_id, tag)
	);
	CREATE TABLE field_values(
		field TEXT NOT NULL,
		value TEXT NOT NULL,
		UNIQUE(field, value)
	);
	CREATE TABLE pins(target_key TEXT PRIMARY KEY);
	CREATE TABLE stars(target_key TEXT PRIMARY KEY);
	CREATE TABLE settings(key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
	// v2: lookup indexes
	`CREATE INDEX idx_context_items_project ON context_items(project);
	CREATE INDEX idx_context_tags_item ON context_tags(item_id);`,
}

// DefaultPath is $XDG_DATA_HOME/mtm/mtm.db or ~/.local/share/mtm/mtm.db.
func DefaultPath() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "mtm", "mtm.db")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".local", "share", "mtm", "mtm.db")
}

// Open opens (creating if needed) the database and applies all pending migrations.
// On any failure the error is returned; the database file is never removed or replaced.
func Open(path string) (Store, error) { return openWithMigrations(path, len(migrations)) }

func openWithMigrations(path string, n int) (*sqlStore, error) {
	if n < 0 || n > len(migrations) {
		return nil, fmt.Errorf("store: invalid migration count %d", n)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("store: create dir: %w", err)
	}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	s := &sqlStore{db: db}
	if err := s.migrate(n); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

type sqlStore struct{ db *sql.DB }

func (s *sqlStore) Close() error { return s.db.Close() }

func (s *sqlStore) migrate(n int) error {
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(
		version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("store: schema_migrations: %w", err)
	}
	cur, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if cur > len(migrations) {
		return fmt.Errorf("store: database schema v%d is newer than this build (v%d)", cur, len(migrations))
	}
	for v := cur + 1; v <= n; v++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("store: migration v%d: %w", v, err)
		}
		if _, err := tx.ExecContext(ctx, migrations[v-1]); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: migration v%d: %w", v, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?,?)`, v, now()); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: migration v%d: %w", v, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: migration v%d: %w", v, err)
		}
	}
	return nil
}

func now() int64           { return time.Now().UnixNano() }
func ts(n int64) time.Time { return time.Unix(0, n).UTC() }
func (s *sqlStore) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	return int(v.Int64), nil
}

// ---- reads ----

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func tagsFor(ctx context.Context, q querier, query string, id int64) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

const projectCols = `id, name, status, created_at, updated_at`

func scanProject(sc interface{ Scan(...any) error }) (domain.Project, error) {
	var p domain.Project
	var c, u int64
	if err := sc.Scan(&p.ID, &p.Name, &p.Status, &c, &u); err != nil {
		return p, err
	}
	p.CreatedAt, p.UpdatedAt = ts(c), ts(u)
	return p, nil
}

func (s *sqlStore) Projects(ctx context.Context) ([]domain.Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+projectCols+` FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var out []domain.Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range out {
		if out[i].Tags, err = tagsFor(ctx, s.db, `SELECT tag FROM project_tags WHERE project_id=? ORDER BY tag`, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ProjectByName returns nil, nil when absent.
func (s *sqlStore) ProjectByName(ctx context.Context, name string) (*domain.Project, error) {
	p, err := scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects WHERE name=?`, name))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if p.Tags, err = tagsFor(ctx, s.db, `SELECT tag FROM project_tags WHERE project_id=? ORDER BY tag`, p.ID); err != nil {
		return nil, err
	}
	return &p, nil
}

const itemCols = `id, kind, title, path, body, project, source_server, source_host, source_session, source_window, created_at, updated_at`

func scanItem(sc interface{ Scan(...any) error }) (domain.ContextItem, error) {
	var c domain.ContextItem
	var t domain.Target
	var cr, up int64
	if err := sc.Scan(&c.ID, &c.Kind, &c.Title, &c.Path, &c.Body, &c.Project,
		&t.Server, &t.Host, &t.Session, &t.WindowID, &cr, &up); err != nil {
		return c, err
	}
	if t != (domain.Target{}) {
		c.Source = &t
	}
	c.CreatedAt, c.UpdatedAt = ts(cr), ts(up)
	return c, nil
}

func (s *sqlStore) items(ctx context.Context, where string, args ...any) ([]domain.ContextItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemCols+` FROM context_items `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	var out []domain.ContextItem
	for rows.Next() {
		c, err := scanItem(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range out {
		if out[i].Tags, err = tagsFor(ctx, s.db, `SELECT tag FROM context_tags WHERE item_id=? ORDER BY tag`, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ContextItems lists items of one project; "" = no-project.
func (s *sqlStore) ContextItems(ctx context.Context, project string) ([]domain.ContextItem, error) {
	return s.items(ctx, `WHERE project=?`, project)
}

func (s *sqlStore) AllContextItems(ctx context.Context) ([]domain.ContextItem, error) {
	return s.items(ctx, ``)
}

// ContextItem returns nil, nil when absent.
func (s *sqlStore) ContextItem(ctx context.Context, id int64) (*domain.ContextItem, error) {
	l, err := s.items(ctx, `WHERE id=?`, id)
	if err != nil || len(l) == 0 {
		return nil, err
	}
	return &l[0], nil
}

func (s *sqlStore) FieldValues(ctx context.Context, field string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT value FROM field_values WHERE field=? ORDER BY value`, field)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *sqlStore) keys(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT target_key FROM `+table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

func (s *sqlStore) Pins(ctx context.Context) (map[string]bool, error)  { return s.keys(ctx, "pins") }
func (s *sqlStore) Stars(ctx context.Context) (map[string]bool, error) { return s.keys(ctx, "stars") }

func (s *sqlStore) Settings(ctx context.Context, defaults map[string]string) ([]Setting, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stored := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		stored[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(defaults))
	for k := range defaults {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Setting, 0, len(keys))
	for _, k := range keys {
		st := Setting{Key: k, Default: defaults[k], Value: defaults[k]}
		if v, ok := stored[k]; ok {
			st.Value, st.Stored = v, true
		}
		out = append(out, st)
	}
	return out, nil
}

// ---- writes ----

func (s *sqlStore) Begin(ctx context.Context) (Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &sqlTx{tx: tx, ctx: ctx}, nil
}

type sqlTx struct {
	tx  *sql.Tx
	ctx context.Context
}

func (t *sqlTx) Commit() error   { return t.tx.Commit() }
func (t *sqlTx) Rollback() error { return t.tx.Rollback() }

func (t *sqlTx) exec(q string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(t.ctx, q, args...)
}

func (t *sqlTx) AddFieldValue(field, value string) error {
	_, err := t.exec(`INSERT OR IGNORE INTO field_values(field, value) VALUES(?,?)`, field, value)
	return err
}

func (t *sqlTx) replaceTags(table, col string, id int64, tags []string) error {
	if _, err := t.exec(`DELETE FROM `+table+` WHERE `+col+`=?`, id); err != nil {
		return err
	}
	for _, tag := range tags {
		if _, err := t.exec(`INSERT OR IGNORE INTO `+table+`(`+col+`, tag) VALUES(?,?)`, id, tag); err != nil {
			return err
		}
		if err := t.AddFieldValue("tag", tag); err != nil {
			return err
		}
	}
	return nil
}

func (t *sqlTx) CreateProject(p *domain.Project) error {
	n := now()
	res, err := t.exec(`INSERT INTO projects(name, status, created_at, updated_at) VALUES(?,?,?,?)`, p.Name, p.Status, n, n)
	if err != nil {
		return err
	}
	if p.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	p.CreatedAt, p.UpdatedAt = ts(n), ts(n)
	if p.Status != "" {
		if err := t.AddFieldValue("status", p.Status); err != nil {
			return err
		}
	}
	return t.replaceTags("project_tags", "project_id", p.ID, p.Tags)
}

func (t *sqlTx) UpdateProject(p *domain.Project) error {
	n := now()
	res, err := t.exec(`UPDATE projects SET name=?, status=?, updated_at=? WHERE id=?`, p.Name, p.Status, n, p.ID)
	if err != nil {
		return err
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return fmt.Errorf("store: project %d not found", p.ID)
	}
	p.UpdatedAt = ts(n)
	if p.Status != "" {
		if err := t.AddFieldValue("status", p.Status); err != nil {
			return err
		}
	}
	return t.replaceTags("project_tags", "project_id", p.ID, p.Tags)
}

// DeleteProject removes the project; context items keep their project name text untouched.
func (t *sqlTx) DeleteProject(id int64) error {
	_, err := t.exec(`DELETE FROM projects WHERE id=?`, id)
	return err
}

func deriveTitle(body string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(body), "\n", 2)[0])
	if r := []rune(line); len(r) > 60 {
		line = string(r[:60]) + "…"
	}
	return line
}

func (t *sqlTx) SaveContextItem(c *domain.ContextItem) error {
	if c.Kind == "note" && c.Title == "" {
		c.Title = deriveTitle(c.Body)
	}
	var src domain.Target
	if c.Source != nil {
		src = *c.Source
	}
	n := now()
	if c.ID == 0 {
		res, err := t.exec(`INSERT INTO context_items(kind,title,path,body,project,source_server,source_host,source_session,source_window,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`, c.Kind, c.Title, c.Path, c.Body, c.Project,
			src.Server, src.Host, src.Session, src.WindowID, n, n)
		if err != nil {
			return err
		}
		if c.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		c.CreatedAt = ts(n)
	} else {
		res, err := t.exec(`UPDATE context_items SET kind=?,title=?,path=?,body=?,project=?,source_server=?,source_host=?,source_session=?,source_window=?,updated_at=? WHERE id=?`,
			c.Kind, c.Title, c.Path, c.Body, c.Project, src.Server, src.Host, src.Session, src.WindowID, n, c.ID)
		if err != nil {
			return err
		}
		if k, _ := res.RowsAffected(); k == 0 {
			return fmt.Errorf("store: context item %d not found", c.ID)
		}
	}
	c.UpdatedAt = ts(n)
	return t.replaceTags("context_tags", "item_id", c.ID, c.Tags)
}

// SetContextProject reassociates an item; "" clears the association.
func (t *sqlTx) SetContextProject(id int64, project string) error {
	res, err := t.exec(`UPDATE context_items SET project=?, updated_at=? WHERE id=?`, project, now(), id)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return fmt.Errorf("store: context item %d not found", id)
	}
	return nil
}

func (t *sqlTx) DeleteContextItem(id int64) error {
	_, err := t.exec(`DELETE FROM context_items WHERE id=?`, id)
	return err
}

func (t *sqlTx) setKey(table, key string, on bool) error {
	var err error
	if on {
		_, err = t.exec(`INSERT OR IGNORE INTO `+table+`(target_key) VALUES(?)`, key)
	} else {
		_, err = t.exec(`DELETE FROM `+table+` WHERE target_key=?`, key)
	}
	return err
}

func (t *sqlTx) SetPinned(k string, on bool) error  { return t.setKey("pins", k, on) }
func (t *sqlTx) SetStarred(k string, on bool) error { return t.setKey("stars", k, on) }

func (t *sqlTx) SetSetting(key, value string) error {
	_, err := t.exec(`INSERT INTO settings(key, value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (t *sqlTx) ResetSetting(key string) error {
	_, err := t.exec(`DELETE FROM settings WHERE key=?`, key)
	return err
}
