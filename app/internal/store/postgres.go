package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Postgres stores everything in PostgreSQL through a connection pool.
type Postgres struct {
	Pool *pgxpool.Pool
}

// NewPostgres connects, waits for the database to answer, and applies migrations.
func NewPostgres(ctx context.Context, url string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	p := &Postgres{Pool: pool}
	if err := p.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return p, nil
}

// migrate applies every migrations/*.sql file once, in name order.
// An advisory lock makes this safe when several replicas start at the same time.
func (p *Postgres) migrate(ctx context.Context) error {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727274)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		var done bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`, f).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, _ := migrations.ReadFile(f)
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, f); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) Ping(ctx context.Context) error { return p.Pool.Ping(ctx) }

func (p *Postgres) Close() { p.Pool.Close() }

func (p *Postgres) ListComponents(ctx context.Context) ([]Component, error) {
	rows, err := p.Pool.Query(ctx, `SELECT id, name, description, status, position, updated_at FROM components ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Component, error) {
		var c Component
		err := r.Scan(&c.ID, &c.Name, &c.Description, &c.Status, &c.Position, &c.UpdatedAt)
		return c, err
	})
}

func (p *Postgres) CreateComponent(ctx context.Context, name, description string) (Component, error) {
	if err := validateComponent(name); err != nil {
		return Component{}, err
	}
	var c Component
	err := p.Pool.QueryRow(ctx, `
		INSERT INTO components (name, description, position)
		VALUES ($1, $2, (SELECT COALESCE(MAX(position), 0) + 1 FROM components))
		RETURNING id, name, description, status, position, updated_at`,
		strings.TrimSpace(name), description).Scan(&c.ID, &c.Name, &c.Description, &c.Status, &c.Position, &c.UpdatedAt)
	return c, err
}

func (p *Postgres) SetComponentStatus(ctx context.Context, id int64, status string) (Component, error) {
	if !ValidComponentStatus(status) {
		return Component{}, ErrInvalid
	}
	var c Component
	err := p.Pool.QueryRow(ctx, `
		UPDATE components SET status = $2, updated_at = now() WHERE id = $1
		RETURNING id, name, description, status, position, updated_at`, id, status).
		Scan(&c.ID, &c.Name, &c.Description, &c.Status, &c.Position, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Component{}, ErrNotFound
	}
	return c, err
}

func (p *Postgres) ListIncidents(ctx context.Context, includeResolved bool, limit int) ([]Incident, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.Pool.Query(ctx, `
		SELECT id, title, impact, status, created_at, resolved_at FROM incidents
		WHERE $1 OR status <> 'resolved'
		ORDER BY created_at DESC, id DESC LIMIT $2`, includeResolved, limit)
	if err != nil {
		return nil, err
	}
	incidents, err := pgx.CollectRows(rows, scanIncident)
	if err != nil {
		return nil, err
	}
	for i := range incidents {
		if incidents[i].Updates, err = p.updates(ctx, incidents[i].ID); err != nil {
			return nil, err
		}
	}
	return incidents, nil
}

func (p *Postgres) GetIncident(ctx context.Context, id int64) (Incident, error) {
	rows, err := p.Pool.Query(ctx, `SELECT id, title, impact, status, created_at, resolved_at FROM incidents WHERE id = $1`, id)
	if err != nil {
		return Incident{}, err
	}
	in, err := pgx.CollectExactlyOneRow(rows, scanIncident)
	if errors.Is(err, pgx.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, err
	}
	in.Updates, err = p.updates(ctx, id)
	return in, err
}

func (p *Postgres) CreateIncident(ctx context.Context, title, impact, message string) (Incident, error) {
	if err := validateIncident(title, impact, message); err != nil {
		return Incident{}, err
	}
	var id int64
	err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO incidents (title, impact) VALUES ($1, $2) RETURNING id`,
			strings.TrimSpace(title), impact).Scan(&id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO incident_updates (incident_id, status, message) VALUES ($1, 'investigating', $2)`, id, message)
		return err
	})
	if err != nil {
		return Incident{}, err
	}
	return p.GetIncident(ctx, id)
}

func (p *Postgres) AddIncidentUpdate(ctx context.Context, id int64, status, message string) (Incident, error) {
	if !ValidIncidentStatus(status) || strings.TrimSpace(message) == "" {
		return Incident{}, ErrInvalid
	}
	err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE incidents SET status = $2,
			       resolved_at = CASE WHEN $2 = 'resolved' THEN now() ELSE NULL END
			WHERE id = $1`, id, status)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `INSERT INTO incident_updates (incident_id, status, message) VALUES ($1, $2, $3)`, id, status, message)
		return err
	})
	if err != nil {
		return Incident{}, err
	}
	return p.GetIncident(ctx, id)
}

func (p *Postgres) updates(ctx context.Context, incidentID int64) ([]IncidentUpdate, error) {
	rows, err := p.Pool.Query(ctx, `
		SELECT id, status, message, created_at FROM incident_updates
		WHERE incident_id = $1 ORDER BY created_at DESC, id DESC`, incidentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (IncidentUpdate, error) {
		var u IncidentUpdate
		err := r.Scan(&u.ID, &u.Status, &u.Message, &u.CreatedAt)
		return u, err
	})
}

func scanIncident(r pgx.CollectableRow) (Incident, error) {
	var in Incident
	err := r.Scan(&in.ID, &in.Title, &in.Impact, &in.Status, &in.CreatedAt, &in.ResolvedAt)
	return in, err
}
