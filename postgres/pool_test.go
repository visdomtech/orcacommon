//go:build integration

package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

//go:embed testdata/migrations
var testMigrationsFS embed.FS

var testMigrations *Migrator

func init() {
	sub, err := fs.Sub(testMigrationsFS, "testdata/migrations")
	if err != nil {
		panic(err)
	}
	testMigrations = NewMigrator(sub, nil)
}

func TestMain(m *testing.M) {
	// Ryuk reaper requires Docker Hub access; disable it for air-gapped envs.
	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	os.Exit(m.Run())
}

// startPlainPostgres spins up a raw postgres container and returns a direct DSN.
// Used to test that Connect works with a plain postgres:// URL.
func startPlainPostgres(t *testing.T) (dsn string, terminate func()) {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "postgres:17.5",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "plain_user",
			"POSTGRES_PASSWORD": "plain_pass",
			"POSTGRES_DB":       "plain_db",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp"),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start plain postgres container: %v", err)
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
	dsn = fmt.Sprintf("postgres://plain_user:plain_pass@%s:%s/plain_db?sslmode=disable", host, port.Port())
	return dsn, func() { _ = c.Terminate(ctx) }
}

func TestConnect_NormalURL(t *testing.T) {
	dsn, terminate := startPlainPostgres(t)
	defer terminate()

	ctx := context.Background()
	pool, err := Connect(ctx, dsn, "test-normal-url")
	if err != nil {
		t.Fatalf("Connect with plain URL: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestConnect_TCPrefix_NoTag(t *testing.T) {
	ctx := context.Background()

	pool, err := Connect(ctx, "postgres:tc:", "test-tc-no-tag")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestConnect_TCPrefix_WithTag(t *testing.T) {
	ctx := context.Background()

	pool, err := Connect(ctx, "postgres:tc:17.5", "test-tc-with-tag")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestConnect_TCPrefix_CleanupTerminates(t *testing.T) {
	ctx := context.Background()

	pool, err := Connect(ctx, "postgres:tc:", "test-tc-cleanup")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping before cleanup: %v", err)
	}

	pool.Close()

	if err := pool.Ping(ctx); err == nil {
		t.Fatal("expected ping to fail after pool.Close(), but it succeeded")
	}
}

func TestConnect_Embedded_WithTempDataPath(t *testing.T) {
	ctx := context.Background()
	dataPath := t.TempDir()

	pool, err := Connect(ctx, "postgres:embedded:?datapath="+url.QueryEscape(dataPath), "test-embed-datapath")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// Verify the data path was actually used by embedded postgres.
	entries, err := os.ReadDir(dataPath)
	if err != nil {
		t.Fatalf("read dataPath: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected dataPath to contain postgres data files, but it was empty")
	}
}

func TestConnect_Embedded_PoolCloseTerminates(t *testing.T) {
	ctx := context.Background()
	dataPath := t.TempDir()

	pool, err := Connect(ctx, "postgres:embedded:?datapath="+url.QueryEscape(dataPath), "test-embed-close")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping before close: %v", err)
	}

	pool.Close()

	// After Close(), ping must fail.
	if err := pool.Ping(ctx); err == nil {
		t.Fatal("expected ping to fail after pool.Close(), but it succeeded")
	}
}

func TestConnect_Embedded_AutoDataPath(t *testing.T) {
	// When no datapath is provided, Connect should create a temp directory
	// automatically. The pool must still work and the instance must be
	// tracked for graceful shutdown.
	ctx := context.Background()

	pool, err := Connect(ctx, "postgres:embedded:", "test-embed-autopath")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

// loadTestDBConfig parses DB_-prefixed env vars into a DBConfig for tests.
func loadTestDBConfig(t *testing.T) DBConfig {
	t.Helper()
	var dbcfg DBConfig
	if err := env.ParseWithOptions(&dbcfg, env.Options{Prefix: "DB_"}); err != nil {
		t.Fatalf("parse DB config: %v", err)
	}
	return dbcfg
}

func TestOpenPool_RunsMigrations(t *testing.T) {
	ctx := context.Background()
	dbcfg := loadTestDBConfig(t)

	pool, err := OpenPool(ctx, dbcfg, testMigrations)
	if err != nil {
		t.Fatalf("OpenPool: %v", err)
	}

	var exists bool
	err = pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_name = 'atlas_schema_revisions'
		)`).Scan(&exists)
	if err != nil {
		t.Fatalf("query atlas_schema_revisions existence: %v", err)
	}
	if !exists {
		t.Fatal("expected atlas_schema_revisions table to exist after OpenPool, but it does not")
	}
}

func TestOpenPool_Singleton(t *testing.T) {
	ctx := context.Background()
	dbcfg := loadTestDBConfig(t)

	p1, err := OpenPool(ctx, dbcfg, testMigrations)
	if err != nil {
		t.Fatalf("first OpenPool: %v", err)
	}
	p2, err := OpenPool(ctx, dbcfg, testMigrations)
	if err != nil {
		t.Fatalf("second OpenPool: %v", err)
	}
	if p1 != p2 {
		t.Fatal("expected OpenPool to return the same *pgxpool.Pool on repeated calls")
	}
	if err := p1.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

// TestOpenPoolWithKey_RunsMigrationsOnCachedPool verifies that runMigrations
// is invoked on every OpenPoolWithKey call, including the fast path that
// returns a cached pool. It uses runMigrationsHook to count invocations.
func TestOpenPoolWithKey_RunsMigrationsOnCachedPool(t *testing.T) {
	ctx := context.Background()
	dbcfg := loadTestDBConfig(t)

	// Install a counting hook; defer cleanup.
	var callCount int
	origHook := runMigrationsHook
	runMigrationsHook = func(_ context.Context, _ *pgxpool.Pool, _, _ string) error {
		callCount++
		return nil
	}
	defer func() { runMigrationsHook = origHook }()

	const testKey = "test-migrations-on-cached"

	// First call: creates pool, runs migrations via hook.
	pool1, err := OpenPoolWithKey(ctx, dbcfg, testMigrations, testKey)
	if err != nil {
		t.Fatalf("first OpenPoolWithKey: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 migration call after creation, got %d", callCount)
	}

	// Second call: hits the fast path (cached pool).
	// With the fix, runMigrations must be called again.
	pool2, err := OpenPoolWithKey(ctx, dbcfg, testMigrations, testKey)
	if err != nil {
		t.Fatalf("second OpenPoolWithKey: %v", err)
	}
	if pool1 != pool2 {
		t.Fatal("expected same pool on second call")
	}
	if callCount != 2 {
		t.Fatalf("expected 2 migration calls (creation + cached), got %d", callCount)
	}
}

// TestOpenPoolWithKey_SequentialKeyReuseSameDataPath verifies that two
// different pool keys can sequentially open and close embedded Postgres
// pools using the same data path. The sequence is:
//
//  1. OpenPoolWithKey(k1) with data path p1
//  2. ClosePoolWithKey(k1) — stops pool and embedded PG
//  3. OpenPoolWithKey(k2) with the same data path p1
//  4. ClosePoolWithKey(k2)
//
// All four operations must succeed without error.
func TestOpenPoolWithKey_SequentialKeyReuseSameDataPath(t *testing.T) {
	// Extend start timeout for slow environments (initdb on temp dirs).
	orig := startTimeout
	startTimeout = 120 * time.Second
	defer func() { startTimeout = orig }()

	ctx := context.Background()
	dataPath := t.TempDir()

	dbcfg := DBConfig{
		DatabaseURLTemplate: "postgres:embedded:?datapath=" + url.QueryEscape(dataPath),
	}

	// Step 1: Open pool with key k1.
	pool1, err := OpenPoolWithKey(ctx, dbcfg, nil, "k1")
	if err != nil {
		t.Fatalf("OpenPoolWithKey k1: %v", err)
	}
	if err := pool1.Ping(ctx); err != nil {
		t.Fatalf("ping k1: %v", err)
	}

	// Step 2: Close pool with key k1.
	if err := ClosePoolWithKey(ctx, "k1"); err != nil {
		t.Fatalf("ClosePoolWithKey k1: %v", err)
	}

	// Step 3: Open pool with key k2 using the same data path.
	pool2, err := OpenPoolWithKey(ctx, dbcfg, nil, "k2")
	if err != nil {
		t.Fatalf("OpenPoolWithKey k2 (same data path): %v", err)
	}
	if err := pool2.Ping(ctx); err != nil {
		t.Fatalf("ping k2: %v", err)
	}

	// Step 4: Close pool with key k2.
	if err := ClosePoolWithKey(ctx, "k2"); err != nil {
		t.Fatalf("ClosePoolWithKey k2: %v", err)
	}
}

// Compile-time check: Connect must be callable with a *pgxpool.Pool return.
var _ func(context.Context, string, string) (*pgxpool.Pool, error) = Connect
