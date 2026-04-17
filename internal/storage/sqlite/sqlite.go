// Package sqlite is a Storage backed by a file-based SQLite database.
// It survives restarts — which is what makes the memory investigator's
// "learning from past incidents" meaningful in production.
//
// Uses modernc.org/sqlite (pure Go, no CGO) so the distroless-static
// runtime image keeps working without libsqlite3.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/trialanderror-eng/lolo/internal/storage"
)

type Storage struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS investigations (
	id          TEXT PRIMARY KEY,
	started_at  INTEGER NOT NULL,
	duration_ns INTEGER NOT NULL,
	data        TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_investigations_started_at
	ON investigations(started_at DESC);
`

// New opens (or creates) the database at path and applies the schema.
// Also enables WAL mode for better concurrent read/write — important
// because the dashboard reads while the webhook handler writes.
func New(path string) (*Storage, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetConnMaxLifetime(time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	return &Storage{db: db}, nil
}

func (s *Storage) Close() error { return s.db.Close() }

func (s *Storage) Save(ctx context.Context, inv storage.Investigation) error {
	data, err := json.Marshal(inv)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO investigations (id, started_at, duration_ns, data)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   started_at  = excluded.started_at,
		   duration_ns = excluded.duration_ns,
		   data        = excluded.data`,
		inv.Incident.ID, inv.StartedAt.Unix(), int64(inv.Duration), string(data),
	)
	return err
}

func (s *Storage) Get(ctx context.Context, id string) (storage.Investigation, bool, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT data FROM investigations WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.Investigation{}, false, nil
	}
	if err != nil {
		return storage.Investigation{}, false, err
	}
	return decode(data)
}

func (s *Storage) List(ctx context.Context, limit int) ([]storage.Investigation, error) {
	q := `SELECT data FROM investigations ORDER BY started_at DESC`
	args := []any{}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []storage.Investigation
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		inv, _, err := decode(data)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func decode(data string) (storage.Investigation, bool, error) {
	var inv storage.Investigation
	if err := json.Unmarshal([]byte(data), &inv); err != nil {
		return storage.Investigation{}, false, fmt.Errorf("unmarshal: %w", err)
	}
	return inv, true, nil
}
