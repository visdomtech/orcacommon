package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"cloud.google.com/go/cloudsqlconn"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/visdomtech/orcacommon/utils"
)

var (
	keyedPools    = make(map[string]*pgxpool.Pool)
	keyedPoolLock sync.RWMutex

	embeddedPGs    = make(map[string]*embeddedpostgres.EmbeddedPostgres)
	embeddedPGLock sync.Mutex
)

func init() {
	go gracefulShutdown()
}

// OpenPool returns the process-wide singleton pgxpool connection.
// The caller supplies the DBConfig (typically from AppConfig.DBConfig).
// The pool is created on the first call and reused on subsequent calls.
// A SIGTERM/SIGINT handler is registered to gracefully close the pool on shutdown.
func OpenPool(ctx context.Context, dbcfg DBConfig, migrator *Migrator) (*pgxpool.Pool, error) {
	return OpenPoolWithKey(ctx, dbcfg, migrator, "__shared__")
}

// OpenPoolWithKey returns a keyed pgxpool connection. If the pool is not found, it is created with the given key and save in the pools.
// the cached pool will be returned directly on the second time it is called with given key.
func OpenPoolWithKey(ctx context.Context, dbcfg DBConfig, migrator *Migrator, key string) (*pgxpool.Pool, error) {
	if key == "" {
		return nil, errors.New("non-empty key is required")
	}
	keyedPoolLock.RLock()
	pool, found := keyedPools[key]
	keyedPoolLock.RUnlock()

	if found {
		return pool, nil
	}
	keyedPoolLock.Lock()
	defer keyedPoolLock.Unlock()
	if pool, found = keyedPools[key]; found {
		return pool, nil
	}

	var err error
	if pool, err = createPool(ctx, dbcfg, migrator, key); err == nil {
		keyedPools[key] = pool
	}
	return pool, err
}

func createPool(ctx context.Context, dbcfg DBConfig, migrator *Migrator, key string) (*pgxpool.Pool, error) {
	var pool *pgxpool.Pool
	var err error
	if dbcfg.CloudSQLInstance != "" {
		pool, err = openCloudSQL(ctx, dbcfg)
	} else {
		pool, err = Connect(ctx, dbcfg.ResolveURL(), key)
	}
	if err != nil {
		return nil, err
	}
	if err = runMigrations(ctx, pool, migrator, key); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// gracefulShutdown blocks until SIGTERM or SIGINT is received, then closes
// all connection pools and stops any embedded Postgres instances. It is
// intended to be launched as a goroutine from init() and should not be
// called directly.
func gracefulShutdown() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	sig := <-ch
	slog.Info("received shutdown signal, closing database pools", "signal", sig)

	// Close connection pools first so they can cleanly drain before the DB servers stop.
	keyedPoolLock.Lock()
	slog.Info("closing the keyed pools", "count", len(keyedPools))
	for _, pool := range keyedPools {
		pool.Close()
	}
	clear(keyedPools)
	keyedPoolLock.Unlock()

	// Stop embedded Postgres instances after pools are closed.
	embeddedPGLock.Lock()
	slog.Info("stopping embedded Postgres instances", "count", len(embeddedPGs))
	for k, pg := range embeddedPGs {
		if err := pg.Stop(); err != nil {
			slog.Error("stop embedded Postgres", "key", k, "error", err)
		}
	}
	clear(embeddedPGs)
	embeddedPGLock.Unlock()
}

func openCloudSQL(ctx context.Context, dbcfg DBConfig) (*pgxpool.Pool, error) {
	d, err := cloudsqlconn.NewDialer(ctx, cloudsqlconn.WithLazyRefresh())
	if err != nil {
		return nil, fmt.Errorf("new Cloud SQL dialer: %w", err)
	}

	dsn := fmt.Sprintf("user=%s password=%s dbname=%s sslmode=disable",
		dbcfg.User, dbcfg.Password, dbcfg.Name)
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	poolCfg.ConnConfig.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return d.Dial(ctx, dbcfg.CloudSQLInstance)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("new pool: %w", err)
	}
	return pool, nil
}

// Connect returns a pgxpool.Pool for the given database URL.
//
// The key parameter identifies this connection for tracking embedded Postgres
// instances used during graceful shutdown. When called via OpenPoolWithKey the
// key is the pool key; direct callers should supply a unique non-empty string.
//
// If dbURL starts with "postgres:embedded:", it spins up an embedded Postgres instance automatically.
// If dbURL starts with "postgres:tc:", it spins up a Testcontainer automatically.
// The testcontainer process lifetime is managed by the Docker daemon; callers
// should invoke pool.Close() when done with the connection.
func Connect(ctx context.Context, dbURL string, key string) (*pgxpool.Pool, error) {
	var embeddedPG *embeddedpostgres.EmbeddedPostgres
	var tcContainer testcontainers.Container

	if strings.Contains(dbURL, "postgres:embedded:") {
		slog.Info("'postgres:embedded:' detected — provisioning an embedded Postgres")

		const (
			dbUser     = "test"
			dbPassword = "test"
			dbName     = "test"
		)

		// Parse optional query parameters appended after the prefix.
		// e.g. "postgres:embedded:?datapath=/tmp/pgdata"
		embeddedOpts := parseEmbeddedOptions(dbURL)

		port, err := utils.GetFreePort()
		if err != nil {
			return nil, fmt.Errorf("get free port: %w", err)
		}

		cfg := embeddedpostgres.DefaultConfig().
			Username(dbUser).
			Password(dbPassword).
			Database(dbName).
			Port(uint32(port)).
			Version(embeddedpostgres.V18)
		if embeddedOpts.dataPath != "" {
			cfg = cfg.DataPath(embeddedOpts.dataPath)
		}

		postgres := embeddedpostgres.NewDatabase(cfg)

		if err := postgres.Start(); err != nil {
			return nil, fmt.Errorf("start embedded postgres: %w", err)
		}
		embeddedPG = postgres

		embeddedPGLock.Lock()
		if old, ok := embeddedPGs[key]; ok {
			slog.Warn("replacing existing embedded Postgres entry", "key", key)
			_ = old.Stop()
		}
		embeddedPGs[key] = postgres
		embeddedPGLock.Unlock()

		dbURL = fmt.Sprintf("postgres://%s:%s@localhost:%d/%s?sslmode=disable",
			dbUser, dbPassword, port, dbName)
		slog.Info("Embedded Postgres provisioned", "key", key, "port", port)
	}

	if strings.Contains(dbURL, "postgres:tc:") {
		slog.Info("'postgres:tc:' detected — provisioning a TestContainer")

		left := strings.TrimPrefix(dbURL, "postgres:tc:")
		imageName := "postgres:17.5"
		if strings.HasPrefix(left, ":") {
			tag := strings.SplitN(left, ":", 2)[0]
			imageName = "postgres:" + tag
		}

		const (
			dbUser     = "test"
			dbPassword = "test"
			dbName     = "test"
		)

		req := testcontainers.ContainerRequest{
			Image:        imageName,
			ExposedPorts: []string{"5432/tcp"},
			Env: map[string]string{
				"POSTGRES_USER":     dbUser,
				"POSTGRES_PASSWORD": dbPassword,
				"POSTGRES_DB":       dbName,
			},
			WaitingFor: wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2),
		}
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			return nil, fmt.Errorf("start testcontainer: %w", err)
		}

		host, err := c.Host(ctx)
		if err != nil {
			_ = c.Terminate(context.Background())
			return nil, fmt.Errorf("container host: %w", err)
		}
		port, err := c.MappedPort(ctx, "5432")
		if err != nil {
			_ = c.Terminate(context.Background())
			return nil, fmt.Errorf("container port: %w", err)
		}

		dbURL = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
			dbUser, dbPassword, host, port.Port(), dbName)
		slog.Info("TestContainer provisioned", "key", key, "host", host, "port", port.Port())
		tcContainer = c
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		if embeddedPG != nil {
			_ = embeddedPG.Stop()
			embeddedPGLock.Lock()
			delete(embeddedPGs, key)
			embeddedPGLock.Unlock()
		}
		if tcContainer != nil {
			_ = tcContainer.Terminate(context.Background())
		}
		return nil, fmt.Errorf("open pool: %w", err)
	}

	return pool, nil
}

// embeddedOptions holds options parsed from the query string of a
// "postgres:embedded:" URL.
type embeddedOptions struct {
	dataPath string
}

// parseEmbeddedOptions extracts options from query parameters appended to
// a "postgres:embedded:" URL. Unrecognized parameters are ignored.
//
// Supported parameters:
//
//	datapath — Postgres data directory (maps to Config.DataPath)
func parseEmbeddedOptions(dbURL string) embeddedOptions {
	const prefix = "postgres:embedded:"
	i := strings.Index(dbURL, prefix)
	if i < 0 {
		return embeddedOptions{}
	}
	suffix := dbURL[i+len(prefix):]
	if !strings.HasPrefix(suffix, "?") {
		return embeddedOptions{}
	}
	q, err := url.ParseQuery(strings.TrimPrefix(suffix, "?"))
	if err != nil {
		slog.Warn("failed to parse embedded postgres query params", "error", err)
		return embeddedOptions{}
	}
	return embeddedOptions{
		dataPath: q.Get("datapath"),
	}
}
