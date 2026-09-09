package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"cloud.google.com/go/cloudsqlconn"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver for database/sql
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/visdomtech/orcacommon/utils"
)

var (
	keyedPools    = make(map[string]*pgxpool.Pool)
	keyedPoolLock sync.RWMutex

	// embeddedInstance tracks a running embedded Postgres along with the data
	// path used at startup. The data path is needed to read postmaster.pid
	// for force-kill fallback during graceful shutdown.
	embeddedInstances = make(map[string]embeddedInstance)
	embeddedPGLock    sync.Mutex

	// stopTimeout is the maximum time allowed for pg.Stop() (pg_ctl stop)
	// before force-killing the process. Exported as a var for test overrides.
	stopTimeout = 15 * time.Second

	// shutdownDone is closed when gracefulShutdown completes, allowing
	// consuming applications to block in WaitForGracefulShutdown until
	// all cleanup (pool closing, embedded Postgres stop) has finished.
	shutdownDone = make(chan struct{})
)

type embeddedInstance struct {
	pg       *embeddedpostgres.EmbeddedPostgres
	dataPath string
}

func init() {
	go gracefulShutdown()
}

// WaitForGracefulShutdown blocks until the graceful shutdown handler has
// completed all cleanup (closing connection pools and stopping embedded
// Postgres instances). If no shutdown signal has been received, it blocks
// indefinitely.
//
// Consuming applications MUST call this at the end of main (or in their
// signal handler) to prevent the process from exiting before the embedded
// Postgres shutdown sequence completes. Without this call, main may return
// while the shutdown goroutine is still running, causing the Go runtime to
// terminate all goroutines — including the one performing cleanup.
//
//	func main() {
//	    // ... start servers, open pools ...
//	    <-waitForSignal() // app's own signal handling
//	    // ... stop HTTP servers ...
//	    postgres.WaitForGracefulShutdown() // block until pools and embedded PG are cleaned up
//	}
func WaitForGracefulShutdown() {
	<-shutdownDone
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
	if err = runMigrations(ctx, pool, migrator, key, dbcfg.MigrationSchema); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// gracefulShutdown blocks until SIGTERM or SIGINT is received, then closes
// all connection pools and stops any embedded Postgres instances. It is
// intended to be launched as a goroutine from init() and should not be
// called directly.
//
// Embedded Postgres instances are stopped with a timeout: if pg_ctl stop
// does not complete within the deadline, or if the process survives the
// graceful stop, the process is force-killed via SIGKILL.
func gracefulShutdown() {
	defer close(shutdownDone)
	shutdownStart := time.Now()
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
	slog.Info("closed all database connection pools")

	// Stop embedded Postgres instances after pools are closed.
	// Snapshot the map under the lock, then stop outside the lock
	// to minimize contention and allow parallel stop attempts.
	embeddedPGLock.Lock()
	instances := make(map[string]embeddedInstance, len(embeddedInstances))
	for k, v := range embeddedInstances {
		instances[k] = v
	}
	clear(embeddedInstances)
	embeddedPGLock.Unlock()

	slog.Info("stopping embedded Postgres instances", "count", len(instances))
	var wg sync.WaitGroup
	for k, inst := range instances {
		wg.Add(1)
		go func(key string, i embeddedInstance) {
			defer wg.Done()
			stopEmbeddedPG(key, i)
		}(k, inst)
	}
	wg.Wait()

	slog.Info("graceful shutdown complete", "duration", time.Since(shutdownStart))
}

// stopEmbeddedPG stops a single embedded Postgres instance with a timeout
// and force-kill fallback. Delegates to stopWithForceKill.
func stopEmbeddedPG(key string, inst embeddedInstance) {
	stopWithForceKill(key, inst)
}

// stopWithForceKill stops an embedded Postgres instance with a timeout and
// SIGKILL force-kill fallback. It is used by gracefulShutdown, Connect()
// replacement, and Connect() error-cleanup to ensure pg_ctl stop never hangs
// indefinitely and the process is always terminated.
//
// The graceful pg.Stop() is given stopTimeout seconds; if it fails or the
// process survives, SIGKILL is sent via KillEmbeddedPG. After a confirmed
// force-kill, the stale postmaster.pid is removed so the library can restart
// cleanly on the next app launch.
func stopWithForceKill(key string, inst embeddedInstance) {
	// Send SIGTERM directly to the Postgres process to initiate a fast
	// graceful shutdown. This avoids relying solely on pg_ctl stop -w (called
	// by the library's pg.Stop()), which can hang indefinitely when the
	// pg_ctl subprocess does not respond.
	sendPostgresSIGTERM(key, inst.dataPath)

	// Run pg.Stop() with a deadline so a hanging pg_ctl does not block the caller.
	// Note: if the timeout fires, this goroutine is intentionally abandoned —
	// it will unblock once SIGKILL reaps the child process.
	stopErr := make(chan error, 1)
	go func() { stopErr <- inst.pg.Stop() }()

	var stopSucceeded bool
	select {
	case err := <-stopErr:
		if err != nil {
			slog.Warn("graceful stop failed, will attempt force-kill", "key", key, "error", err)
		} else {
			slog.Info("gracefully stopped embedded Postgres", "key", key)
			stopSucceeded = true
		}
	case <-time.After(stopTimeout):
		slog.Warn("graceful stop timed out, will attempt force-kill", "key", key, "timeout", stopTimeout)
	}

	// Verify the process is actually dead by checking the PID file.
	// If alive, force-kill it.
	if inst.dataPath != "" {
		_, alive, pid, err := utils.CheckPIDFile(inst.dataPath)
		if err != nil {
			slog.Warn("could not read postmaster.pid after stop", "key", key, "error", err)
		} else if alive && pid > 0 {
			slog.Warn("embedded Postgres still alive after Stop(), sending SIGKILL", "key", key, "pid", pid)
			if killErr := utils.KillEmbeddedPG(pid); killErr != nil {
				slog.Error("failed to force-kill embedded Postgres", "key", key, "pid", pid, "error", killErr)
			} else {
				slog.Info("force-killed embedded Postgres", "key", key, "pid", pid)
				// Remove stale postmaster.pid so the embedded-postgres library
				// can start cleanly on the next app launch.
				pidFile := filepath.Join(inst.dataPath, "postmaster.pid")
				if rmErr := os.Remove(pidFile); rmErr != nil && !os.IsNotExist(rmErr) {
					slog.Warn("could not remove stale postmaster.pid", "key", key, "path", pidFile, "error", rmErr)
				}
			}
		} else if !stopSucceeded && !alive && pid > 0 {
			// pg.Stop() failed or timed out but the process is already dead.
			// Clean up the stale PID file.
			pidFile := filepath.Join(inst.dataPath, "postmaster.pid")
			_ = os.Remove(pidFile)
		}
	}
}

// sendPostgresSIGTERM reads the PID from the postmaster.pid file and sends
// SIGTERM directly to the Postgres process. This initiates a graceful
// Postgres shutdown (equivalent to pg_ctl stop -m smart) without going
// through the pg_ctl subprocess, which can hang. When called before
// pg.Stop(), it ensures Postgres is already shutting down by the time
// pg_ctl runs, making pg.Stop() return quickly.
//
// On Windows (or when the data path is empty), this is a no-op — the
// library's pg.Stop() is the only shutdown path.
func sendPostgresSIGTERM(key string, dataPath string) {
	if dataPath == "" {
		return
	}
	_, alive, pid, err := utils.CheckPIDFile(dataPath)
	if err != nil {
		slog.Warn("could not read postmaster.pid for SIGTERM", "key", key, "error", err)
		return
	}
	if !alive || pid <= 0 {
		return
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		slog.Warn("could not find Postgres process for SIGTERM", "key", key, "pid", pid, "error", err)
		return
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		slog.Warn("could not send SIGTERM to Postgres", "key", key, "pid", pid, "error", err)
		return
	}
	slog.Info("sent SIGTERM to embedded Postgres", "key", key, "pid", pid)
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
// Query parameters after the prefix are parsed as options (e.g. "?datapath=/tmp/pgdata" sets the
// Postgres data directory via Config.DataPath). Unrecognized parameters are ignored.
// If dbURL starts with "postgres:tc:", it spins up a Testcontainer automatically.
// The testcontainer process lifetime is managed by the Docker daemon; callers
// should invoke pool.Close() when done with the connection.
func Connect(ctx context.Context, dbURL string, key string) (*pgxpool.Pool, error) {
	var embeddedPG *embeddedpostgres.EmbeddedPostgres
	var tcContainer testcontainers.Container

	if IsEmbeddedPostgres(dbURL) {
		slog.Info("'postgres:embedded:' detected — provisioning an embedded Postgres")

		// Parse optional query parameters appended after the prefix.
		// e.g. "postgres:embedded:?datapath=/tmp/pgdata"
		opts := parseEmbeddedOptions(dbURL)
		if opts.dbUser == "" {
			opts.dbUser = "test"
		}
		if opts.dbPassword == "" {
			opts.dbPassword = "test"
		}
		if opts.dbName == "" {
			opts.dbName = "test"
		}

		// Check if an embedded Postgres is already running at the data path.
		// If so, reuse it instead of starting a new instance.
		if running, existingPort := utils.ReuseEmbeddedPG(opts.dataPath); running {
			slog.Info("reusing existing embedded Postgres", "key", key, "dataPath", opts.dataPath, "port", existingPort)
			// When reusing an existing data directory, the target database may not
			// exist (embedded-postgres only creates it during initial initdb).
			// Connect to the system "postgres" database to ensure it exists.
			if err := ensureDatabaseExists("127.0.0.1", existingPort, opts.dbUser, opts.dbPassword, opts.dbName); err != nil {
				return nil, fmt.Errorf("ensure database %q exists: %w", opts.dbName, err)
			}
			dbURL = fmt.Sprintf("postgres://%s:%s@localhost:%d/%s?sslmode=disable",
				opts.dbUser, opts.dbPassword, existingPort, opts.dbName)
		} else {
			port, err := utils.GetFreePort()
			if err != nil {
				return nil, fmt.Errorf("get free port: %w", err)
			}

			// Always resolve the data path explicitly so that stopWithForceKill
			// can read postmaster.pid for force-kill verification. When no
			// custom path is given, create an explicit temp directory.
			effectiveDataPath := opts.dataPath
			if effectiveDataPath == "" {
				effectiveDataPath, err = os.MkdirTemp("", "embedded-pg-*")
				if err != nil {
					return nil, fmt.Errorf("create temp data path: %w", err)
				}
			}

			cfg := embeddedpostgres.DefaultConfig().
				Username(opts.dbUser).
				Password(opts.dbPassword).
				Database(opts.dbName).
				Port(uint32(port)).
				Version(embeddedpostgres.V18).
				DataPath(effectiveDataPath)

			postgres := embeddedpostgres.NewDatabase(cfg)

			if err := postgres.Start(); err != nil {
				return nil, fmt.Errorf("start embedded postgres: %w", err)
			}
			embeddedPG = postgres

			embeddedPGLock.Lock()
			if old, ok := embeddedInstances[key]; ok {
				slog.Warn("replacing existing embedded Postgres entry", "key", key)
				stopWithForceKill(key, old)
			}
			embeddedInstances[key] = embeddedInstance{pg: postgres, dataPath: effectiveDataPath}
			embeddedPGLock.Unlock()

			dbURL = fmt.Sprintf("postgres://%s:%s@localhost:%d/%s?sslmode=disable",
				opts.dbUser, opts.dbPassword, port, opts.dbName)
			slog.Info("Embedded Postgres provisioned", "key", key, "port", port)
		}
	}

	if IsTestContainer(dbURL) {
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
			// Look up the instance we just registered and use the shared
			// stop-with-force-kill path instead of bare pg.Stop().
			embeddedPGLock.Lock()
			inst, exists := embeddedInstances[key]
			if exists {
				delete(embeddedInstances, key)
			}
			embeddedPGLock.Unlock()
			if exists {
				stopWithForceKill(key, inst)
			} else {
				// Fallback: instance was never registered (shouldn't happen).
				_ = embeddedPG.Stop()
			}
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
	dataPath   string
	dbUser     string
	dbPassword string
	dbName     string
}

// ensureDatabaseExists connects to the default "postgres" system database and
// creates targetDB if it does not already exist. This is necessary when reusing
// an existing embedded-postgres data directory, since the library only runs
// createdb during initial initdb.
//
// Note: PostgreSQL does not allow parameterized database names in DDL, so we
// use quoteIdent to safely escape the identifier.
func ensureDatabaseExists(host string, port int, user, password, targetDB string) error {
	rootConnStr := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=postgres sslmode=disable",
		host, port, dsnQuote(user), dsnQuote(password),
	)

	db, err := sql.Open("pgx", rootConnStr)
	if err != nil {
		return fmt.Errorf("connect to root postgres db: %w", err)
	}
	defer db.Close()

	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", targetDB).Scan(&exists); err != nil {
		return fmt.Errorf("query pg_database: %w", err)
	}

	if !exists {
		createSQL := "CREATE DATABASE " + quoteIdent(targetDB)
		if _, err := db.Exec(createSQL); err != nil {
			return fmt.Errorf("create database %q: %w", targetDB, err)
		}
		slog.Info("created database on existing embedded postgres", "database", targetDB)
	}
	return nil
}

// dsnQuote wraps a value in single quotes for use in a PostgreSQL key-value
// connection string, escaping embedded single-quotes and backslashes.
func dsnQuote(v string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
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
		dataPath:   q.Get("datapath"),
		dbUser:     q.Get("user"),
		dbPassword: q.Get("password"),
		dbName:     q.Get("name"),
	}
}
