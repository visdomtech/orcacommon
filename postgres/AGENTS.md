# PostgreSQL Utilities

Connection pool management, Atlas-based migration runner, Cloud SQL connector, embedded Postgres provisioning, and TestContainer support. Provides a single `OpenPool` entry point that handles the full lifecycle: connect → migrate → return pool.

## Architecture

```
OpenPool(ctx, dbcfg, migrator)
    │
    ├── CloudSQLInstance set → openCloudSQL() (cloudsqlconn dialer)
    │
    └── Connect(ctx, dbcfg.ResolveURL(), key)
         │
         ├── "postgres:embedded:" prefix
         │    └── Provision embedded Postgres (fergusstrange/embedded-postgres)
         │         ├── Reuse existing instance (check PID file + port liveness)
         │         └── Or start new instance on a free port
         │
         ├── "postgres:tc:" prefix
         │    └── Provision TestContainer (postgres Docker image)
         │
         └── Standard postgres:// URL → pgxpool.New()
    │
    └── runMigrations(ctx, pool, migrator, rebaseline)
         ├── Set search_path to MigrationSchema (default: "public")
         ├── Open Atlas postgres driver (on pinned connection)
         ├── Load migration files from embed.FS into Atlas MemDir
         │    └── Duplicate version prefix detection (clear error)
         ├── Acquire PostgreSQL advisory lock (key: 773492011, 30s timeout)
         │    └── Pinned *sql.Conn for session-level lock ownership
         ├── Rebaseline branch (when DB_REBASELINE_LAST_MIGRATION is set)
         │    ├── Validate migration dir checksum
         │    ├── Require exactly 1 migration file matching configured version
         │    ├── Validate latest history revision matches and is complete
         │    ├── Atomic: rename old table → backup, rename pkey index
         │    ├── Atomic: create fresh table, write baseline revision
         │    └── Return (migration SQL never executes)
         ├── pgRevisions (custom RevisionReadWriter backed by atlas_schema_revisions)
         ├── Optional baseline (IsBaseline predicate → mark first migration as applied)
         │    └── Non-baseline path: WithAllowDirty(true)
         └── Execute pending migrations (non-linear order)
```

## Components

### `DBConfig` (`dbconfig.go`)
Database connection parameters with `env` struct tags (prefix `DB_`). `ResolveURL()` expands a URL template with credential placeholders. `IsEmbeddedPostgres()` and `IsTestContainer()` detect special connection modes. Implements `slog.LogValuer` (password redacted).

### `OpenPool` / `OpenPoolWithKey` (`pool.go`)
Process-wide singleton pool (or keyed pools for multi-database setups). Double-checked locking on a global map. Pool creation flow via `createPool`: connect → migrate → cache on success. On migration failure, the pool is closed and the error propagated — the pool is **not** cached (all-or-nothing). Uses pgxpool defaults (MaxConns = `runtime.NumCPU()`); consumers needing custom pool sizing should configure `pgxpool.Config` directly.

### `ClosePoolWithKey` (`pool.go`)
Runtime teardown for one keyed pool created by `OpenPoolWithKey` — no process exit required.
- **Empty key** → error `non-empty key is required` (mirrors `OpenPoolWithKey`).
- **Unknown key** → silent no-op, returns `nil` (idempotent, safe in `defer`).
- **Known key** → the cached `pgxpool` is removed from `keyedPools` and closed, then the embedded Postgres entry registered under the same key by `Connect` is removed from `embeddedInstances` and stopped through the shared `stopWithForceKill` path (SIGTERM → `pg.Stop()` → SIGKILL fallback → stale `postmaster.pid` cleanup).
- **Ordering** → the pool is closed *before* the embedded instance is stopped, matching graceful-shutdown ordering. Both map deletions happen under `keyedPoolLock` / `embeddedPGLock` (never nested), and the slow work runs outside the locks — so a concurrent `OpenPoolWithKey` may register a fresh pool/instance under the same key while the old one is still being stopped; the old snapshot is stopped exactly once.
- Re-opening the same key afterwards builds a fresh pool. Closing the `"__shared__"` pool (`OpenPool`) is allowed but drops the process-wide singleton; there is no `ClosePool()` wrapper. TestContainer (`postgres:tc:`) containers are **not** terminated.

### `Connect` (`pool.go`)
Low-level connection function. Detects three special URL prefixes:
- **`postgres:embedded:`** — starts an embedded Postgres instance. Query params: `?datapath=`, `?user=`, `?password=`, `?name=`. Reuses existing instances by checking PID file liveness + port. When reusing, `ensureDatabaseExists` creates the target database if absent (embedded-postgres only runs `createdb` during initial `initdb`).
- **`postgres:tc:`** — starts a TestContainer (default image: `postgres:17.5`). Optional image tag: `postgres:tc:16` → `postgres:16`.
- **Standard URL** — connects directly via `pgxpool.New`.

### `Migrator` / `runMigrations` (`migrate.go`)
Atlas-based migration runner:
- **`Migrator`** — bundles `fs.FS` (migration files) with a caller-supplied `IsBaseline` predicate. Constructed via `NewMigrator(migrationFiles fs.FS, isBaseline func(ctx, pool) bool)`. A nil `isBaseline` disables baseline detection.
- **Advisory lock** — `pg_try_advisory_lock(773492011)` with 500ms polling, 30s max wait.
- **`pgRevisions`** — custom `RevisionReadWriter` backed by `atlas_schema_revisions` table (auto-created).
- **`embedDir`** — loads `fs.FS` into an Atlas `MemDir`, detects duplicate version prefixes.
- **Baseline** — when `IsBaseline` returns true, the first migration is recorded without executing SQL.
- **Rebaseline** — when `DBConfig.RebaselineLastMigration` is non-empty, replaces the full migration history with one baseline revision. See [Rebaseline Operation](#rebaseline-operation) below.

### Graceful Shutdown (`pool.go`)
`init()` launches `gracefulShutdown()` goroutine. Consuming apps MUST call
`WaitForGracefulShutdown()` at the end of `main()` to block until cleanup completes.
On SIGTERM/SIGINT:
1. Close all keyed pools (drain connections).
2. Snapshot the embedded PG map under lock, then stop instances in parallel (WaitGroup) outside the lock.
3. Each instance goes through `stopWithForceKill` (three-tier strategy):
   - SIGTERM sent directly to Postgres PID (via postmaster.pid) for instant shutdown initiation.
   - `pg.Stop()` (pg_ctl smart mode) with a configurable `stopTimeout` (default 15s); returns quickly since PG is already stopping. Skipped for reused instances (pg == nil).
   - If stop fails or hangs, the timeout triggers.
   - After stop, the postmaster.pid PID is verified via `CheckPIDFile`.
   - If the process is still alive, `utils.KillEmbeddedPG(pid)` sends SIGKILL.
   - After confirmed kill, the stale `postmaster.pid` is removed.
   - `stopWithForceKill` is reused by Connect() for instance replacement and error-cleanup paths, and by `ClosePoolWithKey` for per-key teardown.
4. Closes `shutdownDone` channel to unblock `WaitForGracefulShutdown()`.
5. Logs shutdown duration on completion.

Per-key teardown (`ClosePoolWithKey`) runs alongside process-wide shutdown: a
service can release one keyed pool and its embedded Postgres at runtime, and
whatever is left when the signal arrives is handled by `gracefulShutdown` (an
already-closed key is a no-op for `ClosePoolWithKey`).

## Configuration

| Env Var (with `DB_` prefix) | Default | Description |
|---|---|---|
| `HOST` | `localhost` | PostgreSQL host |
| `PORT` | `5432` | PostgreSQL port |
| `USER` | — | PostgreSQL username |
| `PASSWORD` | — | PostgreSQL password |
| `NAME` | — | Database name |
| `CLOUD_SQL_INSTANCE` | — | Cloud SQL instance (`project:region:instance`) |
| `MIGRATION_SCHEMA` | `public` | PostgreSQL search_path for migrations |
| `REBASELINE_LAST_MIGRATION` | — (empty) | Enables rebaseline; set to the last applied migration version. Remove after successful rollout. |
| `URL_TEMPLATE` | `postgres:tc://[username]:[password]@[host]:[port]/[database_name]` | Connection URL template |

### Rebaseline Operation

When `DB_REBASELINE_LAST_MIGRATION` is set to a non-empty version string, `runMigrations` performs an atomic rebaseline instead of normal migration execution. This replaces the full migration history with a single baseline revision, preserving the old history as a backup.

**Preconditions** (all checked before any persistent change):
1. Migration directory checksum is valid (`atlas.sum` matches files).
2. Exactly one versioned `.sql` migration file exists in the directory.
3. The migration file version matches the configured `REBASELINE_LAST_MIGRATION`.
4. The `atlas_schema_revisions` table exists and is non-empty.
5. The latest revision version matches the configured version.
6. The latest revision is fully applied (`applied == total`) with no error.

If any precondition fails, a clear error is returned before any history change. The rebaseline does **not** fall back to normal migration execution.

**Atomic operation** (single PostgreSQL transaction):
1. Rename `atlas_schema_revisions` → `atlas_schema_revisions_backup_<timestamp>`.
2. Rename `atlas_schema_revisions_pkey` → `<backup_table>_pkey`.
3. Create a fresh `atlas_schema_revisions` table.
4. Write one baseline revision (`RevisionTypeBaseline`) for the migration file.

**Backup format:** `atlas_schema_revisions_backup_YYYYMMDDTHHMMSSnnnnnnnnnZ` (UTC nanosecond timestamp). The backup retains all old revision rows and the renamed primary-key index.

**Recovery:** Manual. The library does not restore, retain, or delete backups. Operators must remove the `DB_REBASELINE_LAST_MIGRATION` setting after a successful rollout. Schema drift detection, backup restoration, and backup retention remain outside the library.

**Precedence:** When enabled, rebaseline takes precedence over `Migrator.IsBaseline`. An empty setting preserves all current migration and initial baseline behavior.

**Repeated runs:** Each valid enabled run creates a distinct backup. Operators should remove the setting promptly.

### Special URL prefixes
- `postgres:embedded:?datapath=/tmp/pgdata&user=dev&password=dev_only&name=mydb` (local dev only — never use weak credentials for real data)
- `postgres:tc:` (TestContainer, default `postgres:17.5`), `postgres:tc:16` (override to `postgres:16`)
