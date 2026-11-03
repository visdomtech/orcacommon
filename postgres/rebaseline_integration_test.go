//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"ariga.io/atlas/sql/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

//go:embed testdata/rebaseline
var rebaselineFS embed.FS

const rebaselineVersion = "20260101000000"

// rebaselineTestEnv holds the shared test infrastructure for rebaseline tests.
type rebaselineTestEnv struct {
	dsn    string
	cleanup func()
}

// startRebaselinePostgres provisions a PostgreSQL TestContainer for rebaseline tests.
func startRebaselinePostgres(t *testing.T) rebaselineTestEnv {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "postgres:17.5",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "rebaseline_user",
			"POSTGRES_PASSWORD": "rebaseline_pass",
			"POSTGRES_DB":       "rebaseline_db",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start testcontainer: %v", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		t.Fatalf("container host: %v", err)
	}
	port, err := c.MappedPort(ctx, "5432")
	if err != nil {
		_ = c.Terminate(ctx)
		t.Fatalf("container port: %v", err)
	}
	dsn := fmt.Sprintf("postgres://rebaseline_user:rebaseline_pass@%s:%s/rebaseline_db?sslmode=disable", host, port.Port())
	return rebaselineTestEnv{
		dsn:     dsn,
		cleanup: func() { _ = c.Terminate(ctx) },
	}
}

// openSQLDB opens a *sql.DB connection to the test container.
func (e rebaselineTestEnv) openSQLDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", e.dsn)
	if err != nil {
		t.Fatalf("open sql db: %v", err)
	}
	return db
}

// loadRebaselineDir loads the rebaseline migration files from the embedded FS.
func loadRebaselineDir(t *testing.T) fs.FS {
	t.Helper()
	sub, err := fs.Sub(rebaselineFS, "testdata/rebaseline")
	if err != nil {
		t.Fatalf("sub rebaseline fs: %v", err)
	}
	return sub
}

// seedHistory inserts n revision rows into atlas_schema_revisions.
// All rows are marked as fully applied with the given version.
func seedHistory(t *testing.T, db *sql.DB, versions ...string) {
	t.Helper()
	ctx := context.Background()
	// Ensure the revisions table exists.
	if _, err := db.ExecContext(ctx, createRevTable); err != nil {
		t.Fatalf("create revisions table: %v", err)
	}
	for i, v := range versions {
		desc := fmt.Sprintf("migration_%s", v)
		_, err := db.ExecContext(ctx, `
			INSERT INTO atlas_schema_revisions
				(version, description, type, applied, total, executed_at, execution_time,
				 error, error_stmt, hash, partial_hashes, operator_version)
			VALUES ($1, $2, 2, $3, $3, now(), 0, '', '', 'hash_' || $1, '{}', 'test')
			ON CONFLICT (version) DO UPDATE SET
				description=EXCLUDED.description, applied=EXCLUDED.applied, total=EXCLUDED.total`,
			v, desc, i+1)
		if err != nil {
			t.Fatalf("seed revision %s: %v", v, err)
		}
	}
}

// countRevisions returns the number of rows in atlas_schema_revisions.
func countRevisions(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	err := db.QueryRow("SELECT count(*) FROM atlas_schema_revisions").Scan(&n)
	if err != nil {
		t.Fatalf("count revisions: %v", err)
	}
	return n
}

// tableExists reports whether a table with the given name exists in the current schema.
func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var exists bool
	err := db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)`, name).Scan(&exists)
	if err != nil {
		t.Fatalf("table exists %q: %v", name, err)
	}
	return exists
}

// listBackupTables returns all tables matching the rebaseline backup prefix.
func listBackupTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name LIKE 'atlas_schema_revisions_backup_%'
		ORDER BY table_name`)
	if err != nil {
		t.Fatalf("list backup tables: %v", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan backup table: %v", err)
		}
		tables = append(tables, name)
	}
	return tables
}

// openTestPool opens a pgxpool.Pool from a DSN for testing.
func openTestPool(t *testing.T, dsn string) (*pgxpool.Pool, error) {
	t.Helper()
	ctx := context.Background()
	pool, err := Connect(ctx, dsn, "rebaseline-"+t.Name())
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool, nil
}

// --- Tests ---

// TestRebaseline_Success verifies the full rebaseline flow: seed history, run
// rebaseline, assert backup table with all old rows, fresh table with one
// baseline row, and no migration SQL execution.
func TestRebaseline_Success(t *testing.T) {
	env := startRebaselinePostgres(t)
	defer env.cleanup()
	ctx := context.Background()
	db := env.openSQLDB(t)
	defer db.Close()
	dirFS := loadRebaselineDir(t)

	// Seed two historical revisions — the latest must match the migration file version.
	seedHistory(t, db, "20251201000000", rebaselineVersion)
	beforeCount := countRevisions(t, db)
	if beforeCount != 2 {
		t.Fatalf("expected 2 seeded revisions, got %d", beforeCount)
	}

	// Run rebaseline.
	pool, err := openTestPool(t, env.dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	migrator := NewMigrator(dirFS, nil)
	if err := runMigrations(ctx, pool, migrator, "rebaseline-success", "public", rebaselineVersion); err != nil {
		t.Fatalf("rebaseline: %v", err)
	}

	// Assert: exactly one backup table was created.
	backups := listBackupTables(t, db)
	if len(backups) != 1 {
		t.Fatalf("expected 1 backup table, got %d: %v", len(backups), backups)
	}

	// Assert: backup table has all original rows.
	var backupCount int
	err = db.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s", quoteIdent(backups[0]))).Scan(&backupCount)
	if err != nil {
		t.Fatalf("count backup rows: %v", err)
	}
	if backupCount != 2 {
		t.Fatalf("expected 2 rows in backup, got %d", backupCount)
	}

	// Assert: active table has exactly one row (the baseline).
	activeCount := countRevisions(t, db)
	if activeCount != 1 {
		t.Fatalf("expected 1 active revision, got %d", activeCount)
	}

	// Assert: the active row is a baseline with the correct version.
	var version, description string
	var revType int
	err = db.QueryRow(`SELECT version, description, type FROM atlas_schema_revisions LIMIT 1`).
		Scan(&version, &description, &revType)
	if err != nil {
		t.Fatalf("query active revision: %v", err)
	}
	if version != rebaselineVersion {
		t.Errorf("expected version %q, got %q", rebaselineVersion, version)
	}
	if description == "" {
		t.Error("expected non-empty description")
	}
	if revType != 1 { // RevisionTypeBaseline = 1
		t.Errorf("expected revision type 1 (baseline), got %d", revType)
	}

	// Assert: migration SQL was NOT executed (sentinel table does not exist).
	if tableExists(t, db, "migration_sentinel") {
		t.Fatal("expected migration_sentinel table to NOT exist (rebaseline must not execute SQL)")
	}
}

// TestRebaseline_NoSchemaExecution is covered by TestRebaseline_Success's
// sentinel table assertion. This test adds explicit verification that the
// migration file's DDL did not run.
func TestRebaseline_NoSchemaExecution(t *testing.T) {
	env := startRebaselinePostgres(t)
	defer env.cleanup()
	ctx := context.Background()
	db := env.openSQLDB(t)
	defer db.Close()
	dirFS := loadRebaselineDir(t)

	seedHistory(t, db, rebaselineVersion)

	pool, err := openTestPool(t, env.dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	migrator := NewMigrator(dirFS, nil)
	if err := runMigrations(ctx, pool, migrator, "rebaseline-noexec", "public", rebaselineVersion); err != nil {
		t.Fatalf("rebaseline: %v", err)
	}

	if tableExists(t, db, "migration_sentinel") {
		t.Fatal("migration SQL was executed during rebaseline — it must not run")
	}
}

// TestRebaseline_MissingHistory rejects rebaseline when the history table
// does not exist (empty value preserves current behavior).
func TestRebaseline_MissingHistory(t *testing.T) {
	env := startRebaselinePostgres(t)
	defer env.cleanup()
	ctx := context.Background()
	db := env.openSQLDB(t)
	defer db.Close()
	dirFS := loadRebaselineDir(t)

	// Do NOT seed history or create the revisions table.
	pool, err := openTestPool(t, env.dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	migrator := NewMigrator(dirFS, nil)
	err = runMigrations(ctx, pool, migrator, "rebaseline-missing", "public", rebaselineVersion)
	if err == nil {
		t.Fatal("expected error for missing history, got nil")
	}
	if !strings.Contains(err.Error(), "rebaseline") {
		t.Errorf("expected rebaseline error, got: %v", err)
	}

	// Assert: no backup table was created.
	backups := listBackupTables(t, db)
	if len(backups) != 0 {
		t.Errorf("expected 0 backup tables, got %d", len(backups))
	}
}

// TestRebaseline_VersionMismatch_HistoryLatest rejects when the latest history
// revision version does not match the configured version.
func TestRebaseline_VersionMismatch_HistoryLatest(t *testing.T) {
	env := startRebaselinePostgres(t)
	defer env.cleanup()
	ctx := context.Background()
	db := env.openSQLDB(t)
	defer db.Close()
	dirFS := loadRebaselineDir(t)

	// Seed history with a different latest version.
	seedHistory(t, db, "20251201000000", "20251202000000")

	pool, err := openTestPool(t, env.dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	migrator := NewMigrator(dirFS, nil)
	err = runMigrations(ctx, pool, migrator, "rebaseline-vermismatch", "public", rebaselineVersion)
	if err == nil {
		t.Fatal("expected error for version mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "rebaseline") || !strings.Contains(err.Error(), "version") {
		t.Errorf("expected rebaseline version mismatch error, got: %v", err)
	}

	// Assert: no history changes.
	if countRevisions(t, db) != 2 {
		t.Errorf("expected 2 revisions unchanged, got %d", countRevisions(t, db))
	}
	if len(listBackupTables(t, db)) != 0 {
		t.Error("expected no backup tables after version mismatch")
	}
}

// TestRebaseline_VersionMismatch_FileVersion rejects when the configured
// version does not match the migration file version.
func TestRebaseline_VersionMismatch_FileVersion(t *testing.T) {
	env := startRebaselinePostgres(t)
	defer env.cleanup()
	ctx := context.Background()
	db := env.openSQLDB(t)
	defer db.Close()
	dirFS := loadRebaselineDir(t)

	// Seed history matching a wrong version.
	wrongVersion := "99999999999999"
	seedHistory(t, db, wrongVersion)

	pool, err := openTestPool(t, env.dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	migrator := NewMigrator(dirFS, nil)
	// Configure a version that matches history but not the file.
	err = runMigrations(ctx, pool, migrator, "rebaseline-filemismatch", "public", wrongVersion)
	if err == nil {
		t.Fatal("expected error for file version mismatch, got nil")
	}

	// Assert: no history changes.
	if len(listBackupTables(t, db)) != 0 {
		t.Error("expected no backup tables after file version mismatch")
	}
}

// TestRebaseline_MultipleFiles rejects when the migration directory contains
// more than one .sql file.
func TestRebaseline_MultipleFiles(t *testing.T) {
	env := startRebaselinePostgres(t)
	defer env.cleanup()
	ctx := context.Background()
	db := env.openSQLDB(t)
	defer db.Close()

	// Seed valid history.
	seedHistory(t, db, rebaselineVersion)

	// Build a MemDir with two migration files and a valid atlas.sum.
	mem := migrate.OpenMemDir("rebaseline_multi_test")
	defer func() { _ = mem.Close() }()
	_ = mem.WriteFile(rebaselineVersion+"_first.sql", []byte("CREATE TABLE t1(id int);"))
	_ = mem.WriteFile("20260102000000_second.sql", []byte("CREATE TABLE t2(id int);"))
	files, _ := mem.Files()
	hf, err := migrate.NewHashFile(files)
	if err != nil {
		t.Fatalf("compute hash: %v", err)
	}
	sumBytes, err := hf.MarshalText()
	if err != nil {
		t.Fatalf("marshal hash: %v", err)
	}
	_ = mem.WriteFile("atlas.sum", sumBytes)

	// Get a pinned connection for doRebaseline.
	pool, err := openTestPool(t, env.dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Close()
	_, _ = conn.ExecContext(ctx, `SET search_path TO "public"`)

	// Call doRebaseline directly — must reject with "exactly 1".
	err = doRebaseline(ctx, conn, mem, rebaselineVersion, "public")
	if err == nil {
		t.Fatal("expected error for multiple files, got nil")
	}
	if !strings.Contains(err.Error(), "exactly 1") {
		t.Errorf("expected 'exactly 1' error, got: %v", err)
	}

	// Assert: no history changes.
	if len(listBackupTables(t, db)) != 0 {
		t.Error("expected no backup tables after multiple files rejection")
	}
}

// TestRebaseline_IncompleteHistory_PartialRevision rejects when the latest
// revision has partial application (applied < total).
func TestRebaseline_IncompleteHistory_PartialRevision(t *testing.T) {
	env := startRebaselinePostgres(t)
	defer env.cleanup()
	ctx := context.Background()
	db := env.openSQLDB(t)
	defer db.Close()
	dirFS := loadRebaselineDir(t)

	// Seed a partially applied revision (applied=1, total=3).
	if _, err := db.ExecContext(ctx, createRevTable); err != nil {
		t.Fatalf("create revisions table: %v", err)
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO atlas_schema_revisions
			(version, description, type, applied, total, executed_at, execution_time,
			 error, error_stmt, hash, partial_hashes, operator_version)
		VALUES ($1, 'partial_migration', 2, 1, 3, now(), 0, '', '', 'hash_partial', '{}', 'test')`,
		rebaselineVersion)
	if err != nil {
		t.Fatalf("seed partial revision: %v", err)
	}

	pool, err := openTestPool(t, env.dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	migrator := NewMigrator(dirFS, nil)
	err = runMigrations(ctx, pool, migrator, "rebaseline-partial", "public", rebaselineVersion)
	if err == nil {
		t.Fatal("expected error for partial revision, got nil")
	}

	// Assert: no history changes.
	if len(listBackupTables(t, db)) != 0 {
		t.Error("expected no backup tables after partial revision rejection")
	}
}

// TestRebaseline_IncompleteHistory_ErrorRevision rejects when the latest
// revision has a non-empty error field.
func TestRebaseline_IncompleteHistory_ErrorRevision(t *testing.T) {
	env := startRebaselinePostgres(t)
	defer env.cleanup()
	ctx := context.Background()
	db := env.openSQLDB(t)
	defer db.Close()
	dirFS := loadRebaselineDir(t)

	// Seed a revision with an error.
	if _, err := db.ExecContext(ctx, createRevTable); err != nil {
		t.Fatalf("create revisions table: %v", err)
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO atlas_schema_revisions
			(version, description, type, applied, total, executed_at, execution_time,
			 error, error_stmt, hash, partial_hashes, operator_version)
		VALUES ($1, 'error_migration', 2, 1, 1, now(), 0, 'some error occurred', 'BAD SQL', 'hash_error', '{}', 'test')`,
		rebaselineVersion)
	if err != nil {
		t.Fatalf("seed error revision: %v", err)
	}

	pool, err := openTestPool(t, env.dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	migrator := NewMigrator(dirFS, nil)
	err = runMigrations(ctx, pool, migrator, "rebaseline-errorrev", "public", rebaselineVersion)
	if err == nil {
		t.Fatal("expected error for error revision, got nil")
	}

	if len(listBackupTables(t, db)) != 0 {
		t.Error("expected no backup tables after error revision rejection")
	}
}

// TestRebaseline_ChecksumFailure rejects when the migration directory checksum
// does not match atlas.sum.
func TestRebaseline_ChecksumFailure(t *testing.T) {
	env := startRebaselinePostgres(t)
	defer env.cleanup()
	ctx := context.Background()
	db := env.openSQLDB(t)
	defer db.Close()

	// Seed valid history.
	seedHistory(t, db, rebaselineVersion)

	// Build a MemDir with one SQL file but a CORRUPTED atlas.sum.
	mem := migrate.OpenMemDir("rebaseline_cksum_test")
	defer func() { _ = mem.Close() }()
	_ = mem.WriteFile(rebaselineVersion+"_test.sql", []byte("CREATE TABLE t(id int);"))
	// Write an atlas.sum with intentionally wrong hash values.
	_ = mem.WriteFile("atlas.sum", []byte("h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n"+
		rebaselineVersion+"_test.sql h1:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=\n"))

	// Get a pinned connection for doRebaseline.
	pool, err := openTestPool(t, env.dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Close()
	_, _ = conn.ExecContext(ctx, `SET search_path TO "public"`)

	// Call doRebaseline directly — must reject with checksum error.
	err = doRebaseline(ctx, conn, mem, rebaselineVersion, "public")
	if err == nil {
		t.Fatal("expected error for checksum mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "validate migration dir") {
		t.Errorf("expected checksum validation error, got: %v", err)
	}

	// Assert: no history changes.
	if len(listBackupTables(t, db)) != 0 {
		t.Error("expected no backup tables after checksum rejection")
	}
}

// TestRebaseline_DisabledNormalMigration proves that migration SQL executes
// when the rebaseline setting is empty (normal migration path).
func TestRebaseline_DisabledNormalMigration(t *testing.T) {
	env := startRebaselinePostgres(t)
	defer env.cleanup()
	ctx := context.Background()
	db := env.openSQLDB(t)
	defer db.Close()
	dirFS := loadRebaselineDir(t)

	// Run normal migration (empty rebaseline).
	pool, err := openTestPool(t, env.dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	migrator := NewMigrator(dirFS, nil)
	if err := runMigrations(ctx, pool, migrator, "rebaseline-normal", "public", ""); err != nil {
		t.Fatalf("normal migration: %v", err)
	}

	// Assert: migration SQL was executed (sentinel table exists).
	if !tableExists(t, db, "migration_sentinel") {
		t.Fatal("expected migration_sentinel table to exist after normal migration")
	}

	// Assert: revision table has one row (the applied migration).
	if countRevisions(t, db) != 1 {
		t.Fatalf("expected 1 revision after normal migration, got %d", countRevisions(t, db))
	}
}

// TestRebaseline_DisabledInitialBaseline proves that IsBaseline retains its
// current behavior when the rebaseline setting is empty.
func TestRebaseline_DisabledInitialBaseline(t *testing.T) {
	env := startRebaselinePostgres(t)
	defer env.cleanup()
	ctx := context.Background()
	db := env.openSQLDB(t)
	defer db.Close()
	dirFS := loadRebaselineDir(t)

	// Run with IsBaseline returning true and empty rebaseline.
	pool, err := openTestPool(t, env.dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	isBaseline := func(ctx context.Context, pool *pgxpool.Pool) bool { return true }
	migrator := NewMigrator(dirFS, isBaseline)
	if err := runMigrations(ctx, pool, migrator, "rebaseline-baseline", "public", ""); err != nil {
		t.Fatalf("initial baseline: %v", err)
	}

	// Assert: sentinel table does NOT exist (baseline doesn't execute SQL).
	if tableExists(t, db, "migration_sentinel") {
		t.Fatal("sentinel table should not exist after baseline")
	}

	// Assert: revision table has one baseline row.
	if countRevisions(t, db) != 1 {
		t.Fatalf("expected 1 revision after baseline, got %d", countRevisions(t, db))
	}
	var revType int
	err = db.QueryRow("SELECT type FROM atlas_schema_revisions LIMIT 1").Scan(&revType)
	if err != nil {
		t.Fatalf("query revision type: %v", err)
	}
	if revType != 1 { // RevisionTypeBaseline
		t.Errorf("expected revision type 1 (baseline), got %d", revType)
	}
}

// TestRebaseline_RepeatedRun verifies that two valid enabled calls create
// two distinct backup tables and one active baseline row.
func TestRebaseline_RepeatedRun(t *testing.T) {
	env := startRebaselinePostgres(t)
	defer env.cleanup()
	ctx := context.Background()
	db := env.openSQLDB(t)
	defer db.Close()
	dirFS := loadRebaselineDir(t)

	seedHistory(t, db, "20251201000000", rebaselineVersion)

	// First rebaseline.
	pool1, err := openTestPool(t, env.dsn)
	if err != nil {
		t.Fatalf("open pool 1: %v", err)
	}
	migrator := NewMigrator(dirFS, nil)
	if err := runMigrations(ctx, pool1, migrator, "rebaseline-repeat1", "public", rebaselineVersion); err != nil {
		t.Fatalf("first rebaseline: %v", err)
	}

	// Brief sleep to ensure distinct timestamps.
	time.Sleep(10 * time.Millisecond)

	// Second rebaseline (the active table now has one baseline row matching the version).
	pool2, err := openTestPool(t, env.dsn)
	if err != nil {
		t.Fatalf("open pool 2: %v", err)
	}
	migrator2 := NewMigrator(dirFS, nil)
	if err := runMigrations(ctx, pool2, migrator2, "rebaseline-repeat2", "public", rebaselineVersion); err != nil {
		t.Fatalf("second rebaseline: %v", err)
	}

	// Assert: exactly two backup tables.
	backups := listBackupTables(t, db)
	if len(backups) != 2 {
		t.Fatalf("expected 2 backup tables, got %d: %v", len(backups), backups)
	}
	// Backups should be distinct (different timestamps).
	sort.Strings(backups)
	if backups[0] == backups[1] {
		t.Error("backup tables should have distinct names")
	}

	// Assert: active table has exactly one baseline row.
	if countRevisions(t, db) != 1 {
		t.Fatalf("expected 1 active revision, got %d", countRevisions(t, db))
	}
}

// TestRebaseline_ConcurrentCalls verifies that two concurrent rebaseline
// callers serialize through the advisory lock without deadlock or data loss.
func TestRebaseline_ConcurrentCalls(t *testing.T) {
	env := startRebaselinePostgres(t)
	defer env.cleanup()
	ctx := context.Background()
	db := env.openSQLDB(t)
	defer db.Close()
	dirFS := loadRebaselineDir(t)

	seedHistory(t, db, rebaselineVersion)

	var wg sync.WaitGroup
	errs := make([]error, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			pool, err := openTestPool(t, env.dsn)
			if err != nil {
				errs[idx] = err
				return
			}
			migrator := NewMigrator(dirFS, nil)
			errs[idx] = runMigrations(ctx, pool, migrator, fmt.Sprintf("rebaseline-concurrent-%d", idx), "public", rebaselineVersion)
		}(i)
	}
	wg.Wait()

	// At least one must succeed. The second may succeed or fail
	// (depending on timing), but neither should deadlock.
	successCount := 0
	for _, err := range errs {
		if err == nil {
			successCount++
		}
	}
	if successCount == 0 {
		t.Fatalf("expected at least one concurrent rebaseline to succeed, both failed: %v, %v", errs[0], errs[1])
	}

	// Assert: at least one backup table exists.
	backups := listBackupTables(t, db)
	if len(backups) < 1 {
		t.Fatalf("expected at least 1 backup table, got %d", len(backups))
	}

	// Assert: no deadlock occurred (test completing proves this).
}

// TestRebaseline_FailureInventory verifies that after a rejection, the
// active table and backup inventory are unchanged.
func TestRebaseline_FailureInventory(t *testing.T) {
	env := startRebaselinePostgres(t)
	defer env.cleanup()
	ctx := context.Background()
	db := env.openSQLDB(t)
	defer db.Close()
	dirFS := loadRebaselineDir(t)

	// Seed valid history.
	seedHistory(t, db, rebaselineVersion)

	// Record state before.
	revCountBefore := countRevisions(t, db)
	backupsBefore := listBackupTables(t, db)

	// Attempt rebaseline with a mismatched version to trigger rejection.
	pool, err := openTestPool(t, env.dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	migrator := NewMigrator(dirFS, nil)
	err = runMigrations(ctx, pool, migrator, "rebaseline-inventory", "public", "wrong_version")
	if err == nil {
		t.Fatal("expected rejection error, got nil")
	}

	// Assert: state unchanged.
	revCountAfter := countRevisions(t, db)
	if revCountBefore != revCountAfter {
		t.Errorf("revision count changed: before=%d after=%d", revCountBefore, revCountAfter)
	}
	backupsAfter := listBackupTables(t, db)
	if len(backupsBefore) != len(backupsAfter) {
		t.Errorf("backup count changed: before=%d after=%d", len(backupsBefore), len(backupsAfter))
	}
}
