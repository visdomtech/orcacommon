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
    └── runMigrations(ctx, pool, migrator)
         ├── Set search_path to MigrationSchema (default: "public")
         ├── Open Atlas postgres driver
         ├── Load migration files from embed.FS into Atlas MemDir
         │    └── Duplicate version prefix detection (clear error)
         ├── Acquire PostgreSQL advisory lock (key: 773492011, 30s timeout)
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
| `URL_TEMPLATE` | `postgres:tc://[username]:[password]@[host]:[port]/[database_name]` | Connection URL template |

### Special URL prefixes
- `postgres:embedded:?datapath=/tmp/pgdata&user=dev&password=dev_only&name=mydb` (local dev only — never use weak credentials for real data)
- `postgres:tc:` (TestContainer, default `postgres:17.5`), `postgres:tc:16` (override to `postgres:16`)
