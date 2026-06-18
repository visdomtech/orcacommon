package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	atlasschema "ariga.io/atlas/sql/schema"
	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/postgres"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// advisoryLockKey is a stable application-wide lock key for serialising migrations across replicas.
const advisoryLockKey = int64(773492011)

// runMigrations applies all pending versioned migrations from the provided FS.
// It acquires a PostgreSQL advisory lock to prevent concurrent replicas from racing.
// Returns a non-nil error if any migration fails; callers must not start the server in that case.
func runMigrations(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	driver, err := postgres.Open(sqlDB)
	if err != nil {
		return fmt.Errorf("migrate: open atlas driver: %w", err)
	}

	dir, err := embedDir(fsys)
	if err != nil {
		return fmt.Errorf("migrate: open migration dir: %w", err)
	}

	if err := acquireAdvisoryLock(ctx, sqlDB); err != nil {
		return err
	}
	defer releaseAdvisoryLock(ctx, sqlDB)

	rrw := newPGRevisions(sqlDB)
	if err := rrw.init(ctx); err != nil {
		return fmt.Errorf("migrate: init revisions table: %w", err)
	}

	// WithAllowDirty permits migration on databases that have pre-existing schemas
	// (e.g. the default "public" schema in a fresh PostgreSQL instance).
	executor, err := migrate.NewExecutor(driver, dir, rrw, migrate.WithAllowDirty(true))
	if err != nil {
		return fmt.Errorf("migrate: new executor: %w", err)
	}

	pending, err := executor.Pending(ctx)
	if errors.Is(err, migrate.ErrNoPendingFiles) {
		slog.InfoContext(ctx, "atlas migration: no pending migrations")
		return nil
	}
	if err != nil {
		return fmt.Errorf("migrate: list pending: %w", err)
	}

	slog.InfoContext(ctx, "atlas migration: applying migrations", "count", len(pending))
	for _, f := range pending {
		start := time.Now()
		applyErr := executor.Execute(ctx, f)
		durMs := time.Since(start).Milliseconds()
		if applyErr != nil {
			slog.ErrorContext(ctx, "atlas migration: failed",
				"version", f.Version(),
				"description", f.Desc(),
				"direction", "up",
				"duration_ms", durMs,
				"error", applyErr.Error(),
			)
			return fmt.Errorf("migrate: apply %s: %w", f.Version(), applyErr)
		}
		slog.InfoContext(ctx, "atlas migration: applied",
			"version", f.Version(),
			"description", f.Desc(),
			"direction", "up",
			"duration_ms", durMs,
		)
	}
	return nil
}

// embedDir loads the embedded FS into a MemDir so Atlas can read it.
func embedDir(fsys fs.FS) (migrate.Dir, error) {
	mem := migrate.OpenMemDir("migrations")
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		if err := mem.WriteFile(e.Name(), data); err != nil {
			return nil, err
		}
	}
	return mem, nil
}

func acquireAdvisoryLock(ctx context.Context, db *sql.DB) error {
	const maxWait = 30 * time.Second
	const poll = 500 * time.Millisecond
	deadline := time.Now().Add(maxWait)
	for {
		var locked bool
		if err := db.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&locked); err != nil {
			return fmt.Errorf("migrate: advisory lock query: %w", err)
		}
		if locked {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("migrate: could not acquire advisory lock within %s", maxWait)
		}
		slog.InfoContext(ctx, "atlas migration: waiting for advisory lock")
		time.Sleep(poll)
	}
}

func releaseAdvisoryLock(ctx context.Context, db *sql.DB) {
	if _, err := db.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockKey); err != nil {
		slog.WarnContext(ctx, "atlas migration: failed to release advisory lock", "error", err)
	}
}

// pgRevisions is a PostgreSQL-backed RevisionReadWriter that persists migration state.
type pgRevisions struct {
	db      *sql.DB
	typeMap *pgtype.Map
}

func newPGRevisions(db *sql.DB) *pgRevisions {
	m := pgtype.NewMap()
	m.RegisterDefaultPgType([]string{}, "text[]")
	return &pgRevisions{db: db, typeMap: m}
}

const createRevTable = `
CREATE TABLE IF NOT EXISTS atlas_schema_revisions (
	version         TEXT        NOT NULL PRIMARY KEY,
	description     TEXT        NOT NULL DEFAULT '',
	type            INT         NOT NULL DEFAULT 2,
	applied         INT         NOT NULL DEFAULT 0,
	total           INT         NOT NULL DEFAULT 0,
	executed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
	execution_time  BIGINT      NOT NULL DEFAULT 0,
	error           TEXT        NOT NULL DEFAULT '',
	error_stmt      TEXT        NOT NULL DEFAULT '',
	hash            TEXT        NOT NULL DEFAULT '',
	partial_hashes  TEXT[]      NOT NULL DEFAULT '{}',
	operator_version TEXT       NOT NULL DEFAULT ''
)`

func (r *pgRevisions) init(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, createRevTable)
	return err
}

func (r *pgRevisions) Ident() *migrate.TableIdent {
	return &migrate.TableIdent{Name: "atlas_schema_revisions"}
}

func (r *pgRevisions) ReadRevisions(ctx context.Context) ([]*migrate.Revision, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT version, description, type, applied, total,
		       executed_at, execution_time, error, error_stmt,
		       hash, partial_hashes, operator_version
		FROM atlas_schema_revisions ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var revs []*migrate.Revision
	for rows.Next() {
		rev, err := r.scanRevision(rows)
		if err != nil {
			return nil, err
		}
		revs = append(revs, rev)
	}
	return revs, rows.Err()
}

func (r *pgRevisions) ReadRevision(ctx context.Context, version string) (*migrate.Revision, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT version, description, type, applied, total,
		       executed_at, execution_time, error, error_stmt,
		       hash, partial_hashes, operator_version
		FROM atlas_schema_revisions WHERE version = $1`, version)
	rev, err := r.scanRevision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, migrate.ErrRevisionNotExist
	}
	return rev, err
}

func (r *pgRevisions) WriteRevision(ctx context.Context, rev *migrate.Revision) error {
	partial := rev.PartialHashes
	if partial == nil {
		partial = []string{}
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO atlas_schema_revisions
		  (version, description, type, applied, total, executed_at, execution_time,
		   error, error_stmt, hash, partial_hashes, operator_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (version) DO UPDATE SET
		  description=EXCLUDED.description, type=EXCLUDED.type,
		  applied=EXCLUDED.applied, total=EXCLUDED.total,
		  executed_at=EXCLUDED.executed_at, execution_time=EXCLUDED.execution_time,
		  error=EXCLUDED.error, error_stmt=EXCLUDED.error_stmt,
		  hash=EXCLUDED.hash, partial_hashes=EXCLUDED.partial_hashes,
		  operator_version=EXCLUDED.operator_version`,
		rev.Version, rev.Description, int(rev.Type), rev.Applied, rev.Total,
		rev.ExecutedAt, rev.ExecutionTime.Nanoseconds(),
		rev.Error, rev.ErrorStmt, rev.Hash, partial, rev.OperatorVersion,
	)
	return err
}

func (r *pgRevisions) DeleteRevision(ctx context.Context, version string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM atlas_schema_revisions WHERE version = $1", version)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func (r *pgRevisions) scanRevision(s scanner) (*migrate.Revision, error) {
	var (
		rev           migrate.Revision
		execTimeNanos int64
		partialHashes []string
		revType       int
	)
	err := s.Scan(
		&rev.Version, &rev.Description, &revType, &rev.Applied, &rev.Total,
		&rev.ExecutedAt, &execTimeNanos, &rev.Error, &rev.ErrorStmt,
		&rev.Hash, r.typeMap.SQLScanner(&partialHashes), &rev.OperatorVersion,
	)
	if err != nil {
		return nil, err
	}
	rev.Type = migrate.RevisionType(revType)
	rev.ExecutionTime = time.Duration(execTimeNanos)
	rev.PartialHashes = partialHashes
	return &rev, nil
}

// Ensure pgRevisions implements the interface at compile time.
var _ migrate.RevisionReadWriter = (*pgRevisions)(nil)

// Ensure postgres.Driver satisfies schema.ExecQuerier (checked at compile time via usage in Open).
var _ atlasschema.ExecQuerier = (*sql.DB)(nil)
