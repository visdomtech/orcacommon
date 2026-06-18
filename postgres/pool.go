package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"cloud.google.com/go/cloudsqlconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	poolOnce      sync.Once
	sharedPool    *pgxpool.Pool
	sharedPoolErr error
)

// OpenPool returns the process-wide singleton pgxpool connection.
// The caller supplies the DBConfig (typically from AppConfig.DBConfig).
// The pool is created on the first call and reused on subsequent calls.
// A SIGTERM/SIGINT handler is registered to gracefully close the pool on shutdown.
func OpenPool(ctx context.Context, dbcfg DBConfig, fsys fs.FS) (*pgxpool.Pool, error) {
	poolOnce.Do(func() {
		if dbcfg.CloudSQLInstance != "" {
			sharedPool, sharedPoolErr = openCloudSQL(ctx, dbcfg)
		} else {
			sharedPool, sharedPoolErr = Connect(ctx, dbcfg.ResolveURL())
		}
		if sharedPoolErr != nil {
			return
		}
		if sharedPoolErr = runMigrations(ctx, sharedPool, fsys); sharedPoolErr != nil {
			return
		}
		go gracefulShutdown()
	})
	return sharedPool, sharedPoolErr
}

// gracefulShutdown blocks until SIGTERM or SIGINT is received, then closes
// the shared connection pool. It is intended to be launched as a goroutine
// from OpenPool and should not be called directly.
func gracefulShutdown() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	sig := <-ch
	slog.Info("received shutdown signal, closing database pool", "signal", sig)
	if sharedPool != nil {
		sharedPool.Close()
	}
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
// If dbURL starts with "postgres:tc:", it spins up a Testcontainer automatically.
// The testcontainer process lifetime is managed by the Docker daemon; callers
// should invoke pool.Close() when done with the connection.
func Connect(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	if strings.Contains(dbURL, "postgres:tc:") {
		log.Println("'postgres:tc:' detected — provisioning a TestContainer")

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
		slog.Info("TestContainer provisioned", "dbURL", dbURL)
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}

	return pool, nil
}
