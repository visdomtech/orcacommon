//go:build integration

package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"testing"

	"github.com/caarlos0/env/v11"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

//go:embed testdata/migrations
var testMigrationsFS embed.FS

var testMigrations fs.FS

func init() {
	sub, err := fs.Sub(testMigrationsFS, "testdata/migrations")
	if err != nil {
		panic(err)
	}
	testMigrations = sub
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
	pool, err := Connect(ctx, dsn)
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

	pool, err := Connect(ctx, "postgres:tc:")
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

	pool, err := Connect(ctx, "postgres:tc:17.5")
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

	pool, err := Connect(ctx, "postgres:tc:")
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

// loadTestDBConfig parses DB_-prefixed env vars into a DBConfig for tests.
func loadTestDBConfig(t *testing.T) DBConfig {
	t.Helper()
	var dbcfg DBConfig
	if err := env.Parse(&dbcfg, env.Options{Prefix: "DB_"}); err != nil {
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

// Compile-time check: Connect must be callable with a *pgxpool.Pool return.
var _ func(context.Context, string) (*pgxpool.Pool, error) = Connect
