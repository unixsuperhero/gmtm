// Package store owns durable application state in SQLite (modernc, cgo-free).
// TUIOS remains authoritative for live hosts, sessions, windows and processes.
package store

import (
	"context"

	"github.com/unixsuperhero/gmtm/internal/domain"
)

// Setting is a stored value next to its default.
type Setting struct {
	Key     string
	Value   string
	Default string
	Stored  bool
}

// FieldValue is one observed/extensible value of a user-extensible field (tags, statuses...).
type FieldValue struct {
	Field string
	Value string
}

// Tx groups writes; all methods inside are atomic on Commit.
type Tx interface {
	CreateProject(p *domain.Project) error
	UpdateProject(p *domain.Project) error
	DeleteProject(id int64) error
	SaveContextItem(c *domain.ContextItem) error
	SetContextProject(id int64, project string) error
	DeleteContextItem(id int64) error
	AddFieldValue(field, value string) error
	SetPinned(targetKey string, pinned bool) error
	SetStarred(targetKey string, starred bool) error
	// SetPaneProject stores an override by Target.Key(); "" explicitly means no project.
	SetPaneProject(targetKey, project string) error
	SetSetting(key, value string) error
	ResetSetting(key string) error
	Commit() error
	Rollback() error
}

type Store interface {
	Begin(ctx context.Context) (Tx, error)
	Projects(ctx context.Context) ([]domain.Project, error)
	ProjectByName(ctx context.Context, name string) (*domain.Project, error)
	ContextItems(ctx context.Context, project string) ([]domain.ContextItem, error) // project "" = no-project
	AllContextItems(ctx context.Context) ([]domain.ContextItem, error)
	ContextItem(ctx context.Context, id int64) (*domain.ContextItem, error)
	FieldValues(ctx context.Context, field string) ([]string, error)
	Pins(ctx context.Context) (map[string]bool, error)
	Stars(ctx context.Context) (map[string]bool, error)
	// PaneProjects returns overrides; an absent key inherits inventory metadata.
	PaneProjects(ctx context.Context) (map[string]string, error)
	Settings(ctx context.Context, defaults map[string]string) ([]Setting, error)
	SchemaVersion(ctx context.Context) (int, error)
	Close() error
}
