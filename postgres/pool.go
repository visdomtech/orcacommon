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
	"sync/atomic"
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

// cloudDialer abstracts a Cloud SQL connector for dependency injection.
type cloudDialer interface {
	Dial(ctx context.Context, instance string, opts ...cloudsqlconn.DialOption) (net.Conn, error)
	Close() error
}

// newCloudDialer creates a Cloud SQL dialer. Replaced in tests.
var newCloudDialer = func(ctx context.Context) (cloudDialer, error) {
	return cloudsqlconn.NewDialer(ctx, cloudsqlconn.WithLazyRefresh())
}

// poolRegistry is the process-wide registry of keyed database pools.
var poolRegistry poolRegistryType

// poolRegistryType tracks keyed database pools and their acquired resources.
type poolRegistryType struct {
	mu      sync.RWMutex
	entries map[string]*registryEntry
}

// registryEntry holds one keyed pool and the resources acquired during its
// creation (Cloud SQL dialers, test containers, etc.). The done channel is
// closed once the initial creation attempt (open pool + run migrations) finishes,
// allowing concurrent callers to wait rather than race.
type registryEntry struct {
	mu        sync.Mutex
	pool      *pgxpool.Pool
	resources []acquiredResource
	done      chan struct{} // closed when initial creation completes (success or failure)
}

// acquiredResource is a named cleanup callback attached to a registry entry.
type acquiredResource struct {
	name    string
	release func(context.Context) error
}

func (r *poolRegistryType) entryFor(key string) *registryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.entries[key]
}

func (r *poolRegistryType) newEntry(key string) *registryEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[string]*registryEntry)
	}
	e := &registryEntry{done: make(chan struct{})}
	r.entries[key] = e
	return e
}

func (r *poolRegistryType) remove(key string) *registryEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[key]
	delete(r.entries, key)
	return e
}

// attachAcquired records a resource under an existing entry, standing in for
// the Cloud SQL dialer or TestContainer that Connect() would have provisioned.
func (r *poolRegistryType) attachAcquired(e *registryEntry, name string, release func(context.Context) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resources = append(e.resources, acquiredResource{name: name, release: release})
}

// releaseAll invokes every resource's release callback in reverse acquisition
// order (most recent first). All callbacks run even if one fails.
func (e *registryEntry) releaseAll(ctx context.Context) {
	e.mu.Lock()
	resources := make([]acquiredResource, len(e.resources))
	copy(resources, e.resources)
	e.resources = nil
	e.mu.Unlock()
	for i := len(resources) - 1; i >= 0; i-- {
		if err := resources[i].release(ctx); err != nil {
			slog.Warn("failed to release acquired resource", "resource", resources[i].name, "error", err)
		}
	}
}

var (
	// embeddedInstances tracks running embedded Postgres processes along with
	// the data path and PID needed for graceful shutdown.
	embeddedInstances = make(map[string]embeddedInstance)
	embeddedPGLock    sync.Mutex

	// stopTimeout is the maximum time allowed for Postgres shutdown
	// before force-killing the process. Exported as a var for test overrides.
	stopTimeout = 15 * time.Second

	// shutdownDone is closed when gracefulShutdown completes, allowing
	// consuming applications to block in WaitForGracefulShutdown until
	// all cleanup (pool closing, embedded Postgres stop) has finished.
	shutdownDone = make(chan struct{})

	// shuttingDown is set to true when gracefulShutdown begins.
	// Connect() checks this flag to refuse new embedded instance
	// registrations after shutdown has started.
	shuttingDown atomic.Bool
)

type embeddedInstance struct {
	pid          int    // Postgres process PID; 0 means no process to stop
	dataPath     string // Postgres data directory (for PID file verification and cleanup)
	ownsDataPath bool   // true when Connect auto-created the data directory (cleaned up on stop)
	runtimePath  string // library runtime directory containing binaries and pwfile (cleaned up on stop)
}

func init() {
	go gracefulShutdown()
}

// WaitForGracefulShutdown blocks until the graceful shutdown handler has
// completed all cleanup (closing connection pools and stopping embedded
// Postgres instances). If no shutdown signal has been received, it blocks
// indefinitely.
//
// This function only affects locally embedded Postgres instances (started
// via "postgres:embedded:" URLs). It does NOT stop, restart, or otherwise
// interfere with remote Postgres servers — for those, it merely closes the
// client-side connection pool (a normal disconnect) and returns almost
// instantly.
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

// OpenPoolWithKey returns a keyed pgxpool connection. If the pool is not found,
// it is created with the given key and saved in the registry. The cached pool
// will be returned directly on subsequent calls with the same key.
func OpenPoolWithKey(ctx context.Context, dbcfg DBConfig, migrator *Migrator, key string) (*pgxpool.Pool, error) {
	if key == "" {
		return nil, errors.New("non-empty key is required")
	}

	// Fast path: return cached pool.
	if e := poolRegistry.entryFor(key); e != nil {
		e.mu.Lock()
		pool := e.pool
		e.mu.Unlock()
		if pool != nil {
			return pool, nil
		}
		// Pool is nil: creation may be in progress or failed. Wait for it.
		<-e.done
		e.mu.Lock()
		pool = e.pool
		e.mu.Unlock()
		if pool != nil {
			return pool, nil
		}
		// Creation failed; fall through to slow path.
	}

	// Slow path: create pool.
	// Use the entry's done channel to coordinate with concurrent Open/Close
	// callers. The creating goroutine closes done when finished (success or
	// failure), so others can wait rather than race.
	poolRegistry.mu.Lock()
	e := poolRegistry.entries[key]
	if e != nil {
		e.mu.Lock()
		pool := e.pool
		done := e.done
		e.mu.Unlock()
		if pool != nil {
			poolRegistry.mu.Unlock()
			return pool, nil
		}
		if done != nil {
			poolRegistry.mu.Unlock()
			// Another goroutine is creating; wait then re-enter fast path.
			<-done
			e.mu.Lock()
			pool = e.pool
			e.mu.Unlock()
			if pool != nil {
				return pool, nil
			}
			// Creation failed; fall through to create a new entry.
			poolRegistry.mu.Lock()
		}
	}
	// Create (or replace) the entry with a fresh done channel.
	if poolRegistry.entries == nil {
		poolRegistry.entries = make(map[string]*registryEntry)
	}
	e = &registryEntry{done: make(chan struct{})}
	poolRegistry.entries[key] = e
	poolRegistry.mu.Unlock()

	// Ensure done is closed on every exit path so waiters unblock.
	var pool *pgxpool.Pool
	var err error
	defer close(e.done)

	if dbcfg.CloudSQLInstance != "" {
		pool, err = openCloudSQL(ctx, dbcfg, e)
	} else {
		pool, err = Connect(ctx, dbcfg.ResolveURL(), key)
	}
	if err != nil {
		e.releaseAll(ctx)
		poolRegistry.remove(key)
		return nil, err
	}

	if err = runMigrations(ctx, pool, migrator, key, dbcfg.MigrationSchema); err != nil {
		pool.Close()
		e.releaseAll(ctx)
		poolRegistry.remove(key)
		return nil, err
	}

	// Store pool only after successful migrations. This ensures waiters
	// never see a pool that is about to be closed due to migration failure.
	e.mu.Lock()
	e.pool = pool
	e.mu.Unlock()
	return pool, nil
}

// ClosePoolWithKey releases one keyed database pool created by OpenPoolWithKey.
// It closes the cached pgxpool, releases acquired resources (Cloud SQL dialers,
// test containers) in reverse acquisition order, and stops the embedded Postgres
// instance registered under the same key. The pool is closed before resources
// are released and before the embedded instance is stopped, matching graceful
// shutdown ordering. An empty key returns an error. An unknown key is a silent
// no-op and returns nil. After a successful close, OpenPoolWithKey with the
// same key creates a fresh pool.
//
// Closing the "__shared__" pool (OpenPool) is allowed but drops the
// process-wide singleton; no ClosePool() wrapper is provided.
func ClosePoolWithKey(ctx context.Context, key string) error {
	if key == "" {
		return errors.New("non-empty key is required")
	}

	entry := poolRegistry.remove(key)

	embeddedPGLock.Lock()
	inst, hadInstance := embeddedInstances[key]
	delete(embeddedInstances, key)
	embeddedPGLock.Unlock()

	if entry == nil && !hadInstance {
		slog.Debug("no database pool or embedded Postgres registered for key", "key", key)
		return nil
	}

	// Close pool first so release callbacks observe puddle.ErrClosedPool.
	if entry != nil {
		// Wait for in-flight creation to finish so we don't miss a pool
		// that hasn't been stored yet.
		if entry.done != nil {
			<-entry.done
		}
		entry.mu.Lock()
		pool := entry.pool
		entry.pool = nil
		entry.mu.Unlock()
		if pool != nil {
			pool.Close()
		}
		entry.releaseAll(ctx)
	}

	// Stop embedded Postgres after pool and resources are released.
	if hadInstance {
		stopWithForceKill(key, inst)
	}

	slog.Info("closed keyed database pool", "key", key, "hadPool", entry != nil, "stoppedEmbeddedPostgres", hadInstance)
	return nil
}

// gracefulShutdown blocks until SIGTERM or SIGINT is received, then closes
// all connection pools and stops any embedded Postgres instances. It is
// intended to be launched as a goroutine from init() and should not be
// called directly.
//
// Embedded Postgres instances are stopped with a timeout: if the process
// does not exit within the deadline, it is force-killed via SIGKILL.
func gracefulShutdown() {
	defer close(shutdownDone)
	shutdownStart := time.Now()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	sig := <-ch
	shuttingDown.Store(true)
	slog.Info("received shutdown signal, closing database pools", "signal", sig)

	// Close all pools and their resources first.
	poolRegistry.mu.Lock()
	entries := make(map[string]*registryEntry, len(poolRegistry.entries))
	for k, v := range poolRegistry.entries {
		entries[k] = v
	}
	clear(poolRegistry.entries)
	poolRegistry.mu.Unlock()

	for _, entry := range entries {
		// Wait for in-flight creation to finish.
		if entry.done != nil {
			<-entry.done
		}
		entry.mu.Lock()
		pool := entry.pool
		entry.pool = nil
		entry.mu.Unlock()
		if pool != nil {
			pool.Close()
		}
		entry.releaseAll(context.Background())
	}
	slog.Info("closed all database connection pools", "elapsed", time.Since(shutdownStart))

	// Stop embedded Postgres instances after pools are closed.
	instances := snapshotAndClearEmbeddedInstances()

	slog.Info("stopping embedded Postgres instances", "count", len(instances))
	var wg sync.WaitGroup
	for k, inst := range instances {
		wg.Add(1)
		go func(key string, i embeddedInstance) {
			defer wg.Done()
			stopWithForceKill(key, i)
		}(k, inst)
	}
	wg.Wait()

	// Re-check for stragglers: a concurrent Connect() may have added
	// an instance between our initial snapshot and wg.Wait() returning.
	stragglers := snapshotAndClearEmbeddedInstances()
	for k, inst := range stragglers {
		slog.Warn("stopping straggler embedded Postgres added during shutdown", "key", k)
		stopWithForceKill(k, inst)
	}

	slog.Info("graceful shutdown complete", "duration", time.Since(shutdownStart))
}

// stopWithForceKill stops an embedded Postgres instance with a timeout and
// SIGKILL force-kill fallback. It is used by gracefulShutdown, Connect()
// replacement, and Connect() error-cleanup to ensure Postgres never blocks
// shutdown indefinitely.
//
// SIGTERM is sent via sendPostgresSIGTERM (which verifies the process is
// Postgres before signaling). The process is then given stopTimeout to exit;
// if it survives, SIGKILL is sent. After a confirmed stop, owned directories
// (runtime path, auto-created data path) are removed.
func stopWithForceKill(key string, inst embeddedInstance) {
	start := time.Now()
	sendPostgresSIGTERM(key, inst.dataPath)

	// Wait for the process to exit with a deadline.
	deadline := time.After(stopTimeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	processConfirmedDead := false
	for !processConfirmedDead {
		select {
		case <-deadline:
			slog.Warn("graceful stop timed out, will attempt force-kill", "key", key, "timeout", stopTimeout)
			goto forceKill
		case <-ticker.C:
			if inst.pid > 0 && !utils.IsProcessAlive(inst.pid) {
				slog.Info("embedded Postgres stopped", "key", key, "elapsed", time.Since(start))
				processConfirmedDead = true
			}
			// Also check PID file as fallback (e.g., reused instances).
			if !processConfirmedDead && inst.dataPath != "" {
				_, alive, _, _ := utils.CheckPIDFile(inst.dataPath)
				if !alive {
					processConfirmedDead = true
				}
			}
			// If no PID and no data path, nothing to wait for.
			if inst.pid <= 0 && inst.dataPath == "" {
				processConfirmedDead = true
			}
		}
	}

forceKill:
	if !processConfirmedDead && inst.pid > 0 {
		slog.Warn("embedded Postgres still alive, sending SIGKILL", "key", key, "pid", inst.pid)
		if killErr := utils.KillEmbeddedPG(inst.pid); killErr != nil {
			if errors.Is(killErr, utils.ErrNotPostgresProcess) {
				slog.Warn("PID is not a Postgres process, removing stale PID file only", "key", key, "pid", inst.pid)
			} else {
				slog.Error("failed to force-kill embedded Postgres", "key", key, "pid", inst.pid, "error", killErr)
			}
		} else {
			slog.Info("force-killed embedded Postgres", "key", key, "pid", inst.pid)
		}
	}

	// Final liveness check: only remove files/directories if the process is
	// actually dead. A non-Postgres process at the same PID means we must
	// not touch the directories it may still be using.
	if inst.pid > 0 && utils.IsProcessAlive(inst.pid) {
		slog.Warn("process still alive after stop attempt, skipping directory cleanup", "key", key, "pid", inst.pid)
		return
	}

	// Clean up PID file.
	if inst.dataPath != "" {
		pidFile := filepath.Join(inst.dataPath, "postmaster.pid")
		removeStalePIDFile(key, pidFile)
	}

	// Clean up directories we created.
	if inst.runtimePath != "" {
		if err := os.RemoveAll(inst.runtimePath); err != nil {
			slog.Warn("could not remove runtime directory", "key", key, "path", inst.runtimePath, "error", err)
		}
	}
	if inst.ownsDataPath && inst.dataPath != "" {
		if err := os.RemoveAll(inst.dataPath); err != nil {
			slog.Warn("could not remove auto-created data directory", "key", key, "path", inst.dataPath, "error", err)
		}
	}
	slog.Debug("stopWithForceKill complete", "key", key, "elapsed", time.Since(start))
}

// snapshotAndClearEmbeddedInstances atomically copies the embedded instance
// map and clears the original. Used during graceful shutdown to allow
// stopping instances outside the lock.
func snapshotAndClearEmbeddedInstances() map[string]embeddedInstance {
	embeddedPGLock.Lock()
	snapshot := make(map[string]embeddedInstance, len(embeddedInstances))
	for k, v := range embeddedInstances {
		snapshot[k] = v
	}
	clear(embeddedInstances)
	embeddedPGLock.Unlock()
	return snapshot
}

// removeStalePIDFile removes a postmaster.pid file after the process is
// confirmed dead. Logs a warning if removal fails.
func removeStalePIDFile(key string, pidFile string) {
	if err := os.Remove(pidFile); err != nil && !os.IsNotExist(err) {
		slog.Warn("could not remove stale postmaster.pid", "key", key, "path", pidFile, "error", err)
	}
}

// sendPostgresSIGTERM reads the PID from the postmaster.pid file and sends
// SIGTERM directly to the Postgres process. This initiates a graceful
// Postgres shutdown (equivalent to pg_ctl stop -m smart) without going
// through the pg_ctl subprocess, which can hang. When called before the
// wait loop, it ensures Postgres is already shutting down, making the
// wait return quickly.
//
// On Windows (or when the data path is empty), this is a no-op.
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
		slog.Debug("postmaster.pid references dead process, skipping SIGTERM", "key", key, "pid", pid)
		return
	}
	// Verify the process is actually Postgres before sending SIGTERM,
	// guarding against PID reuse after the original process has exited.
	if !utils.IsPostgresProcess(pid) {
		slog.Warn("PID is not a Postgres process, skipping SIGTERM", "key", key, "pid", pid)
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

func openCloudSQL(ctx context.Context, dbcfg DBConfig, entry *registryEntry) (*pgxpool.Pool, error) {
	d, err := newCloudDialer(ctx)
	if err != nil {
		return nil, fmt.Errorf("new Cloud SQL dialer: %w", err)
	}
	poolRegistry.attachAcquired(entry, "cloudsql dialer", func(context.Context) error {
		return d.Close()
	})

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

// embeddedRuntimePath returns the runtime directory for an embedded Postgres
// instance identified by key. This directory holds the library's extracted
// binaries and password file.
func embeddedRuntimePath(key string) string {
	return filepath.Join(os.TempDir(), "orcacommon-embedded-pg", key)
}

// embeddedBinariesPath returns the directory where the embedded-postgres
// library caches extracted binaries.
func embeddedBinariesPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), ".embedded-postgres-go", "extracted")
	}
	return filepath.Join(home, ".embedded-postgres-go", "extracted")
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
// If no datapath is specified, a temporary directory is created and cleaned up on failure.
//
// Reused embedded Postgres instances (detected via an existing postmaster.pid at
// the data path) are registered for graceful shutdown and will be stopped
// (SIGTERM → SIGKILL) when the application exits via WaitForGracefulShutdown.
//
// If dbURL starts with "postgres:tc:", it spins up a Testcontainer automatically.
// The testcontainer process lifetime is managed by the Docker daemon; callers
// should invoke pool.Close() when done with the connection.
func Connect(ctx context.Context, dbURL string, key string) (*pgxpool.Pool, error) {
	var tcContainer testcontainers.Container
	var effectiveDataPath string
	var embeddedPID int
	var ownsData bool
	var runtimeDir string

	if IsEmbeddedPostgres(dbURL) {
		if shuttingDown.Load() {
			return nil, fmt.Errorf("embedded Postgres unavailable: shutdown in progress")
		}
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
			if err := ensureDatabaseExists("127.0.0.1", existingPort, opts.dbUser, opts.dbPassword, opts.dbName); err != nil {
				return nil, fmt.Errorf("ensure database %q exists: %w", opts.dbName, err)
			}

			// Read the PID for shutdown management.
			_, _, reusedPID, _ := utils.CheckPIDFile(opts.dataPath)

			embeddedPGLock.Lock()
			if shuttingDown.Load() {
				embeddedPGLock.Unlock()
				return nil, fmt.Errorf("embedded Postgres unavailable: shutdown in progress")
			}
			var old *embeddedInstance
			if prev, ok := embeddedInstances[key]; ok {
				slog.Warn("replacing existing embedded Postgres entry", "key", key)
				old = &prev
			}
			embeddedInstances[key] = embeddedInstance{pid: reusedPID, dataPath: opts.dataPath}
			embeddedPGLock.Unlock()
			if old != nil {
				stopWithForceKill(key, *old)
			}

			dbURL = fmt.Sprintf("postgres://%s:%s@localhost:%d/%s?sslmode=disable",
				opts.dbUser, opts.dbPassword, existingPort, opts.dbName)
		} else {
			port, err := utils.GetFreePort()
			if err != nil {
				return nil, fmt.Errorf("get free port: %w", err)
			}

			effectiveDataPath = opts.dataPath
			if effectiveDataPath == "" {
				ownsData = true
				effectiveDataPath, err = os.MkdirTemp("", "embedded-pg-*")
				if err != nil {
					return nil, fmt.Errorf("create temp data path: %w", err)
				}
			}

			// Create the runtime directory for the library's binaries and pwfile.
			runtimeDir = embeddedRuntimePath(key)
			if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
				if ownsData {
					_ = os.RemoveAll(effectiveDataPath)
				}
				return nil, fmt.Errorf("create runtime directory: %w", err)
			}

			cfg := embeddedpostgres.DefaultConfig().
				Username(opts.dbUser).
				Password(opts.dbPassword).
				Database(opts.dbName).
				Port(uint32(port)).
				Version(embeddedpostgres.V18).
				DataPath(effectiveDataPath).
				RuntimePath(runtimeDir)

			postgres := embeddedpostgres.NewDatabase(cfg)

			// Ensure the data path directory exists before probing writability.
			// When the caller supplies a data path, it may not exist yet
			// (first use). The embedded-postgres library would create it
			// during initdb, but our probe runs before that.
			if !ownsData {
				if mkErr := os.MkdirAll(effectiveDataPath, 0o700); mkErr != nil {
					_ = os.RemoveAll(runtimeDir)
					return nil, fmt.Errorf("create data path directory: %w", mkErr)
				}
			}

			// Pre-check data path writability to fail fast instead of
			// hanging in initdb when the path is not writable.
			if probeFile, probeErr := os.Create(filepath.Join(effectiveDataPath, ".orcacommon-probe")); probeErr != nil {
				_ = os.RemoveAll(runtimeDir)
				if ownsData {
					_ = os.RemoveAll(effectiveDataPath)
				}
				return nil, fmt.Errorf("unable to init database: data path not writable: %s: %w", effectiveDataPath, probeErr)
			} else {
				_ = probeFile.Close()
				_ = os.Remove(filepath.Join(effectiveDataPath, ".orcacommon-probe"))
			}

			// Start embedded Postgres with a timeout to avoid hanging
			// indefinitely when initdb fails (e.g., unwritable data path).
			const startTimeout = 30 * time.Second
			startErrCh := make(chan error, 1)
			go func() { startErrCh <- postgres.Start() }()

			var startErr error
			select {
			case startErr = <-startErrCh:
			case <-time.After(startTimeout):
				startErr = fmt.Errorf("embedded Postgres start timed out after %s", startTimeout)
				// Drain the channel so the Start goroutine can exit when it
				// eventually returns, preventing a goroutine leak.
				go func() { <-startErrCh }()
			}

			if startErr != nil {
				// Clean up directories we created.
				_ = os.RemoveAll(runtimeDir)
				if ownsData {
					_ = os.RemoveAll(effectiveDataPath)
				}
				return nil, fmt.Errorf("start embedded postgres: %w", startErr)
			}

			// Read the PID from postmaster.pid for shutdown management.
			_, _, pid, pidErr := utils.CheckPIDFile(effectiveDataPath)
			if pidErr != nil || pid <= 0 {
				// Cannot manage shutdown without PID; stop now.
				_ = postgres.Stop()
				_ = os.RemoveAll(runtimeDir)
				if ownsData {
					_ = os.RemoveAll(effectiveDataPath)
				}
				return nil, fmt.Errorf("read postmaster.pid after start: %w", pidErr)
			}
			embeddedPID = pid

			embeddedPGLock.Lock()
			if shuttingDown.Load() {
				embeddedPGLock.Unlock()
				stopWithForceKill(key, embeddedInstance{pid: pid, dataPath: effectiveDataPath, ownsDataPath: ownsData, runtimePath: runtimeDir})
				return nil, fmt.Errorf("embedded Postgres unavailable: shutdown in progress")
			}
			var old *embeddedInstance
			if prev, ok := embeddedInstances[key]; ok {
				slog.Warn("replacing existing embedded Postgres entry", "key", key)
				old = &prev
			}
			embeddedInstances[key] = embeddedInstance{pid: pid, dataPath: effectiveDataPath, ownsDataPath: ownsData, runtimePath: runtimeDir}
			embeddedPGLock.Unlock()
			if old != nil {
				stopWithForceKill(key, *old)
			}

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
		tcContainer = c

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

		// Register TestContainer for cleanup via the registry.
		if e := poolRegistry.entryFor(key); e != nil {
			poolRegistry.attachAcquired(e, "testcontainer", func(ctx context.Context) error {
				return tcContainer.Terminate(ctx)
			})
		}
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		if embeddedPID > 0 {
			// Look up the instance we just registered and use the shared
			// stop-with-force-kill path instead of bare process kill.
			embeddedPGLock.Lock()
			inst, exists := embeddedInstances[key]
			if exists {
				delete(embeddedInstances, key)
			}
			embeddedPGLock.Unlock()
			if exists {
				stopWithForceKill(key, inst)
			} else {
				stopWithForceKill(key, embeddedInstance{pid: embeddedPID, dataPath: effectiveDataPath, ownsDataPath: ownsData, runtimePath: runtimeDir})
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
