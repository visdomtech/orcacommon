package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/postgres"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// advisoryLockKey is a stable application-wide lock key for serialising migrations across replicas.
const advisoryLockKey = int64(773492011)

// runMigrationsHook is an optional callback invoked by runMigrations after the
// nil-migrator guard but before any real work. Tests use it to count or
// intercept migration calls. A non-nil return short-circuits runMigrations.
var runMigrationsHook func(ctx context.Context, pool *pgxpool.Pool, key, schema, rebaseline string) error

// Migrator bundles the migration file source with a caller-supplied baseline
// predicate. It is passed to OpenPool (and internally to runMigrations) so that
// the baseline decision is made by the caller rather than hard-coded.
type Migrator struct {
	migrationFiles fs.FS
	// IsBaseline is invoked by runMigrations to determine whether the current
	// run should only record a baseline (i.e. mark existing migrations as
	// already applied without executing their SQL). A nil func disables
	// baseline handling entirely.
	IsBaseline func(ctx context.Context, pool *pgxpool.Pool) bool
}

// NewMigrator creates a Migrator with the supplied migration file source and
// baseline predicate. Either argument may be nil; a nil IsBaseline disables
// baseline detection.
func NewMigrator(migrationFiles fs.FS, isBaseline func(context.Context, *pgxpool.Pool) bool) *Migrator {
	return &Migrator{
		migrationFiles: migrationFiles,
		IsBaseline:     isBaseline,
	}
}

// runMigrations applies all pending versioned migrations from the Migrator's
// file source. It acquires a PostgreSQL advisory lock to prevent concurrent
// replicas from racing. Returns a non-nil error if any migration fails;
// callers must not start the server in that case.
//
// When rebaseline is non-empty, the function performs an atomic rebaseline
// operation instead of normal migration execution. See doRebaseline for
// the full precondition list and atomic sequence. Rebaseline takes
// precedence over Migrator.IsBaseline: when enabled, the atomic rebaseline
// runs and the function returns without executing any migration SQL or
// invoking the baseline predicate.
func runMigrations(ctx context.Context, pool *pgxpool.Pool, migrator *Migrator, key string, schema string, rebaseline string) error {
	if migrator == nil {
		return nil
	}

	// Test hook: when set, delegate entirely to it and return.
	if runMigrationsHook != nil {
		return runMigrationsHook(ctx, pool, key, schema, rebaseline)
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	dir, err := embedDir(migrator.migrationFiles, key)
	if err != nil {
		return fmt.Errorf("migrate: open migration dir: %w", err)
	}

	// Pin a dedicated connection for advisory lock ownership and (when
	// enabled) the rebaseline transaction. This prevents the lock from
	// drifting between pooled sessions.
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquire connection: %w", err)
	}
	defer conn.Close()

	// Set search_path on the pinned connection so that atlas and
	// rebaseline operations target the specified schema.
	if _, err := conn.ExecContext(ctx, "SET search_path TO "+quoteIdent(schema)); err != nil {
		return fmt.Errorf("migrate: set search_path to %q: %w", schema, err)
	}
	// Reset search_path before returning the connection to the pool so
	// that subsequent borrowers do not inherit the modified session state.
	defer func() {
		if _, err := conn.ExecContext(context.Background(), "RESET search_path"); err != nil {
			slog.WarnContext(ctx, "migrate: failed to reset search_path", "error", err)
		}
	}()

	if err := acquireAdvisoryLock(ctx, conn); err != nil {
		return err
	}
	defer releaseAdvisoryLock(ctx, conn)

	// Rebaseline branch: when enabled, perform the atomic rebaseline
	// on the locked connection and return without executing any migration SQL.
	if rebaseline != "" {
		return doRebaseline(ctx, conn, dir, rebaseline, schema)
	}

	// Normal migration path: open the atlas driver on the pinned
	// connection and execute pending migrations.
	driver, err := postgres.Open(conn)
	if err != nil {
		return fmt.Errorf("migrate: open atlas driver: %w", err)
	}

	rrw := newPGRevisions(conn)
	if err := rrw.init(ctx); err != nil {
		return fmt.Errorf("migrate: init revisions table: %w", err)
	}

	// Ask the caller-supplied predicate whether this run should only set a
	// baseline (mark the first migration as already applied without executing
	// its SQL). A nil IsBaseline disables baseline handling entirely.
	allOpts := []migrate.ExecutorOption{
		migrate.WithExecOrder(migrate.ExecOrderNonLinear),
	}

	if migrator.IsBaseline != nil && migrator.IsBaseline(ctx, pool) {
		files, ferr := dir.Files()
		if ferr != nil {
			return fmt.Errorf("migrate: list migration files: %w", ferr)
		}
		if len(files) > 0 {
			baselineVer := files[0].Version()
			slog.InfoContext(ctx, "atlas migration: setting baseline",
				"version", baselineVer,
				"description", files[0].Desc(),
			)
			allOpts = append(allOpts, migrate.WithBaselineVersion(baselineVer))
		}
	} else {
		allOpts = append(allOpts, migrate.WithAllowDirty(true))
	}

	executor, err := migrate.NewExecutor(driver, dir, rrw, allOpts...)
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
func embedDir(fsys fs.FS, key string) (migrate.Dir, error) {
	mem := migrate.OpenMemDir(fmt.Sprintf("migrations_%s", key))
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	seen := make(map[string]string) // version prefix -> first file seen
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Guard against duplicate migration versions. Two .sql files sharing
		// the same version prefix cause an opaque runtime panic inside the
		// Atlas executor (index out of range on PartialHashes). Catch this
		// early with a clear error instead.
		if strings.HasSuffix(name, ".sql") {
			if ver := versionPrefix(name); ver != "" {
				if prev, ok := seen[ver]; ok {
					return nil, fmt.Errorf(
						"migrate: duplicate migration version %q: files %q and %q share the same version prefix "+
							"(Atlas versions must be unique; rename one file to a different timestamp)",
						ver, prev, name,
					)
				}
				seen[ver] = name
			}
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		slog.Info("write mem sql file", "name", name, "size", len(data))
		if err := mem.WriteFile(name, data); err != nil {
			return nil, err
		}
	}
	return mem, nil
}

// versionPrefix extracts the Atlas migration version (the part before the
// first underscore) from a migration filename, e.g.
// "20260712100000_desc.sql" -> "20260712100000". Returns "" for filenames
// that don't follow the {version}_{description}.sql convention.
func versionPrefix(name string) string {
	idx := strings.Index(name, "_")
	if idx <= 0 {
		return ""
	}
	return name[:idx]
}

func acquireAdvisoryLock(ctx context.Context, conn *sql.Conn) error {
	const maxWait = 30 * time.Second
	const poll = 500 * time.Millisecond
	deadline := time.Now().Add(maxWait)
	for {
		var locked bool
		if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&locked); err != nil {
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

func releaseAdvisoryLock(ctx context.Context, conn *sql.Conn) {
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockKey); err != nil {
		slog.WarnContext(ctx, "atlas migration: failed to release advisory lock", "error", err)
	}
}

// execQuerier abstracts *sql.DB, *sql.Conn, and *sql.Tx for query execution.
// All three types implement this interface.
type execQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// pgRevisions is a PostgreSQL-backed RevisionReadWriter that persists migration state.
type pgRevisions struct {
	db      execQuerier
	typeMap *pgtype.Map
}

func newPGRevisions(db execQuerier) *pgRevisions {
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

// isEmpty reports whether the revisions table has zero rows.
func (r *pgRevisions) isEmpty(ctx context.Context) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM atlas_schema_revisions").Scan(&n)
	if err != nil {
		return false, err
	}
	return n == 0, nil
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

// quoteIdent quotes a PostgreSQL identifier (schema/table name) to prevent SQL injection.
// It doubles any embedded double-quotes and wraps the result in double-quotes.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// doRebaseline replaces the existing migration history with a single baseline
// revision. It validates all preconditions, backs up the old history table,
// creates a fresh one, and writes the baseline revision — all atomically.
//
// Preconditions (all checked before any persistent change):
//  1. Migration directory checksum is valid (atlas.sum matches files).
//  2. Exactly one versioned migration file exists in the directory.
//  3. The migration file version matches the configured rebaseline version.
//  4. The atlas_schema_revisions table exists and is non-empty.
//  5. The latest revision version matches the configured rebaseline version.
//  6. The latest revision is fully applied (applied == total) with no error.
//
// Atomic operation (single transaction):
//  1. Rename atlas_schema_revisions to atlas_schema_revisions_backup_<timestamp>.
//  2. Rename the primary-key index to <backup_table>_pkey.
//  3. Create a fresh atlas_schema_revisions table.
//  4. Write one baseline revision for the migration file.
//  5. Commit (or rollback on any failure).
//
// The migration file SQL is never executed.
func doRebaseline(ctx context.Context, conn *sql.Conn, dir migrate.Dir, version string, schema string) (err error) {
	start := time.Now()
	defer func() {
		if err != nil {
			slog.ErrorContext(ctx, "atlas migration: rebaseline failed", "error", err)
		}
	}()
	// 1. Validate migration directory checksum.
	if err := migrate.Validate(dir); err != nil {
		return fmt.Errorf("migrate: rebaseline: validate migration dir: %w", err)
	}

	// 2. Require exactly one migration file.
	files, err := dir.Files()
	if err != nil {
		return fmt.Errorf("migrate: rebaseline: list migration files: %w", err)
	}
	if len(files) != 1 {
		return fmt.Errorf("migrate: rebaseline: expected exactly 1 migration file, found %d", len(files))
	}

	// 3. Require file version to match configured version.
	fileVersion := files[0].Version()
	if fileVersion != version {
		return fmt.Errorf("migrate: rebaseline: migration file version %q does not match configured version %q", fileVersion, version)
	}

	// 4. Check history table existence without creating it.
	var tableExists bool
	if err := conn.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'atlas_schema_revisions')`,
		schema,
	).Scan(&tableExists); err != nil {
		return fmt.Errorf("migrate: rebaseline: check history table: %w", err)
	}
	if !tableExists {
		return fmt.Errorf("migrate: rebaseline: atlas_schema_revisions table does not exist in schema %q", schema)
	}

	// 5-6. Read the latest revision and validate preconditions.
	var (
		latestVersion string
		applied       int
		total         int
		revError      string
	)
	if err := conn.QueryRowContext(ctx,
		`SELECT version, applied, total, error FROM atlas_schema_revisions ORDER BY version DESC LIMIT 1`,
	).Scan(&latestVersion, &applied, &total, &revError); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("migrate: rebaseline: atlas_schema_revisions table exists but contains no rows")
		}
		return fmt.Errorf("migrate: rebaseline: read latest revision: %w", err)
	}
	if latestVersion != version {
		return fmt.Errorf("migrate: rebaseline: latest history version %q does not match configured version %q", latestVersion, version)
	}
	if applied != total {
		return fmt.Errorf("migrate: rebaseline: latest revision %q is partially applied (applied=%d, total=%d)", latestVersion, applied, total)
	}
	if revError != "" {
		return fmt.Errorf("migrate: rebaseline: latest revision %q has error: %s", latestVersion, revError)
	}

	// --- Begin atomic operation ---
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate: rebaseline: begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Generate backup timestamp.
	now := time.Now().UTC()
	ts := fmt.Sprintf("%04d%02d%02dT%02d%02d%02d%09dZ",
		now.Year(), now.Month(), now.Day(),
		now.Hour(), now.Minute(), now.Second(), now.Nanosecond())
	backupTable := "atlas_schema_revisions_backup_" + ts
	backupIndex := backupTable + "_pkey"

	// 1. Rename the existing history table to the backup name.
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf("ALTER TABLE %s RENAME TO %s", quoteIdent("atlas_schema_revisions"), quoteIdent(backupTable)),
	); err != nil {
		return fmt.Errorf("migrate: rebaseline: rename history table: %w", err)
	}

	// 2. Rename the primary-key index so the fresh table can use the original name.
	// PostgreSQL auto-generates the PK index name as <table>_pkey when the
	// constraint is unnamed (which createRevTable relies on). If a future
	// change names the constraint explicitly, this must be updated.
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf("ALTER INDEX %s RENAME TO %s", quoteIdent("atlas_schema_revisions_pkey"), quoteIdent(backupIndex)),
	); err != nil {
		return fmt.Errorf("migrate: rebaseline: rename primary key index: %w", err)
	}

	// 3. Create the fresh history table.
	rrw := newPGRevisions(tx)
	if err := rrw.init(ctx); err != nil {
		return fmt.Errorf("migrate: rebaseline: create fresh history table: %w", err)
	}

	// 4. Write the baseline revision. Populate the hash from the migration
	// file checksum so that Atlas tooling can verify content integrity.
	var fileHash string
	if hf, hfErr := dir.Checksum(); hfErr == nil {
		for _, entry := range hf {
			if entry.N == files[0].Name() {
				fileHash = entry.H
				break
			}
		}
	}
	rev := &migrate.Revision{
		Version:     fileVersion,
		Description: files[0].Desc(),
		Type:        migrate.RevisionTypeBaseline,
		Applied:     1,
		Total:       1,
		ExecutedAt:  time.Now().UTC(),
		Hash:        fileHash,
	}
	if err := rrw.WriteRevision(ctx, rev); err != nil {
		return fmt.Errorf("migrate: rebaseline: write baseline revision: %w", err)
	}

	// 5. Commit.
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate: rebaseline: commit: %w", err)
	}

	slog.InfoContext(ctx, "atlas migration: rebaseline complete",
		"version", fileVersion,
		"backup_table", backupTable,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return nil
}

// Ensure pgRevisions implements the interface at compile time.
var _ migrate.RevisionReadWriter = (*pgRevisions)(nil)

// Compile-time checks: *sql.DB, *sql.Conn, and *sql.Tx all satisfy execQuerier.
var (
	_ execQuerier = (*sql.DB)(nil)
	_ execQuerier = (*sql.Conn)(nil)
	_ execQuerier = (*sql.Tx)(nil)
)
